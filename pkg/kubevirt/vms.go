package kubevirt

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// kubevirtClient implements the VM-lifecycle methods of Client against a
// KubeVirt-enabled cluster via the dynamic client. Methods not yet ported
// fall through to the embedded *unimplementedClient. Slice A covers
// LaunchVM / ListVMs / GetVMInfo / StartVM / StopVM / DeleteVM only; all
// other methods continue to return ErrNotImplemented.
type kubevirtClient struct {
	*unimplementedClient
	dyn               dynamic.Interface
	sshPrivateKeyPath string
}

var (
	vmGVR = schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}
	vmiGVR = schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}
)

// VMImageLaunchRequest captures every knob the image-based create flow
// exposes. A zero value is not valid — Name is required; unset numeric
// fields fall back to DefaultCPUCores / DefaultRAMMB / DefaultDiskGB.
type VMImageLaunchRequest struct {
	Name          string // VM name (RFC 1123 DNS label)
	Release       string // containerdisk alias, e.g. "24.04" / "ubuntu-24.04"
	CPUs          int
	MemoryMB      int
	DiskGB        int
	CloudInitFile string // optional; path to a rendered cloud-init file
	NetworkName   string // optional; empty = default pod/masquerade
	UEFI          bool   // optional; OVMF firmware without SecureBoot
	ExtraDiskGB   []int  // optional; blank data disks attached in order
}

// LaunchVMFromImage provisions a Secret holding cloud-init user-data (if
// any) and then a VirtualMachine CR whose root disk is a DataVolume
// (PVC backed by CDI, sourced from the containerdisks image). Returns
// the VM's name on success.
//
// The DataVolume is declared via `spec.dataVolumeTemplates` so KubeVirt
// owns its lifecycle — delete the VM and the DV (and its PVC) go with it.
// First boot runs cloud-init's growpart/resize2fs to expand the filesystem
// into whatever disk size the user requested, so a 2 GB Ubuntu image in a
// 16 GB PVC yields a 16 GB usable root disk.
func (c *kubevirtClient) LaunchVMFromImage(req VMImageLaunchRequest) (string, error) {
	if err := ValidateVMName(req.Name); err != nil {
		return "", err
	}
	if req.Release == "" {
		req.Release = DefaultUbuntuRelease
	}
	if req.CPUs <= 0 {
		req.CPUs = DefaultCPUCores
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = DefaultRAMMB
	}
	if req.DiskGB <= 0 {
		req.DiskGB = DefaultDiskGB
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	secretName, err := c.ensureCloudInitSecret(ctx, req.Name, req.CloudInitFile)
	if err != nil {
		return "", fmt.Errorf("write cloud-init secret: %w", err)
	}

	vm := buildVMObject(c.namespace, req, secretName)
	created, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Create(ctx, vm, metav1.CreateOptions{})
	if err != nil {
		// Roll back the secret so a retry with the same name isn't
		// blocked by an orphaned Secret.
		if secretName != "" {
			_ = c.kube.CoreV1().Secrets(c.namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
		}
		return "", fmt.Errorf("create VirtualMachine: %w", err)
	}

	// Re-parent the cloud-init secret to the VM so deleting the VM GC's
	// the secret. We couldn't set the owner-ref at creation time because
	// the VM's UID didn't exist yet.
	if secretName != "" {
		if err := c.setSecretOwner(ctx, secretName, created); err != nil {
			c.logger.Warn("set cloud-init secret owner-ref failed (secret will outlive VM)",
				"vm", req.Name, "secret", secretName, "err", err)
		}
	}

	return req.Name, nil
}

// ensureCloudInitSecret reads cloudInitFile (if non-empty), writes its
// contents to a Secret at `<vm>-cloudinit` under key `userdata`, and
// returns the Secret name. Empty cloudInitFile => empty return, no Secret.
func (c *kubevirtClient) ensureCloudInitSecret(ctx context.Context, vmName, cloudInitFile string) (string, error) {
	if cloudInitFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(cloudInitFile)
	if err != nil {
		return "", fmt.Errorf("read cloud-init file %q: %w", cloudInitFile, err)
	}
	secretName := vmName + "-cloudinit"
	sec := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": c.namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/vm":                 vmName,
			},
		},
		"type": "Opaque",
		"stringData": map[string]any{
			"userdata": string(data),
		},
	}}
	secretGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	_, err = c.dyn.Resource(secretGVR).Namespace(c.namespace).Create(ctx, sec, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return secretName, nil
		}
		return "", err
	}
	return secretName, nil
}

func (c *kubevirtClient) setSecretOwner(ctx context.Context, secretName string, vm *unstructured.Unstructured) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"ownerReferences":[{"apiVersion":%q,"kind":%q,"name":%q,"uid":%q,"controller":true,"blockOwnerDeletion":true}]}}`,
		vm.GetAPIVersion(), vm.GetKind(), vm.GetName(), string(vm.GetUID())))
	_, err := c.kube.CoreV1().Secrets(c.namespace).Patch(ctx, secretName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// buildVMObject returns a VirtualMachine unstructured object with a
// DataVolume-backed root disk (CDI-imported from the containerdisks
// registry image) and an optional cloudInitNoCloud user-data disk. The
// DataVolume is declared via `spec.dataVolumeTemplates` so it inherits
// the VM's lifecycle — delete the VM, the DV and its PVC go with it.
func buildVMObject(namespace string, req VMImageLaunchRequest, cloudInitSecret string) *unstructured.Unstructured {
	rootDVName := req.Name + "-root"
	disks := []any{
		map[string]any{
			"name": "rootdisk",
			"disk": map[string]any{"bus": "virtio"},
		},
	}
	volumes := []any{
		map[string]any{
			"name": "rootdisk",
			"dataVolume": map[string]any{
				"name": rootDVName,
			},
		},
	}
	if cloudInitSecret != "" {
		disks = append(disks, map[string]any{
			"name": "cloudinitdisk",
			"disk": map[string]any{"bus": "virtio"},
		})
		volumes = append(volumes, map[string]any{
			"name": "cloudinitdisk",
			"cloudInitNoCloud": map[string]any{
				"secretRef": map[string]any{"name": cloudInitSecret},
			},
		})
	}

	// DataVolume template: CDI pulls the containerdisk image via its
	// registry importer (pullMethod defaults to "pod") and writes the
	// embedded disk file into a PVC sized to diskGB.
	//
	// accessModes + volumeMode are set explicitly. If omitted, CDI falls
	// back to the cluster's StorageProfile — which for unrecognised
	// provisioners (e.g. rancher.io/local-path on KinD) has no defaults
	// and the DV fails with ErrClaimNotValid. storageClassName is still
	// omitted so the cluster's default SC applies.
	dvTemplates := []any{map[string]any{
		"metadata": map[string]any{
			"name": rootDVName,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/vm":                 req.Name,
			},
		},
		"spec": map[string]any{
			"source": map[string]any{
				"registry": map[string]any{
					"url": "docker://" + ContainerDiskImage(req.Release),
				},
			},
			"storage": map[string]any{
				"accessModes": []any{"ReadWriteOnce"},
				"volumeMode":  "Filesystem",
				"resources": map[string]any{
					"requests": map[string]any{
						"storage": fmt.Sprintf("%dGi", req.DiskGB),
					},
				},
			},
		},
	}}

	// Extra blank data disks. Each becomes its own DataVolume (blank
	// source, same sizing machinery as the root) and is attached to the
	// VM on virtio so the guest sees them as /dev/vdb, /dev/vdc, …
	// Unformatted — the user partitions/mkfs them at first boot.
	for i, gb := range req.ExtraDiskGB {
		if gb <= 0 {
			continue
		}
		dvName := fmt.Sprintf("%s-data%d", req.Name, i+1)
		dvTemplates = append(dvTemplates, map[string]any{
			"metadata": map[string]any{
				"name": dvName,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "kubego",
					"kubego.io/vm":                 req.Name,
				},
			},
			"spec": map[string]any{
				"source": map[string]any{"blank": map[string]any{}},
				"storage": map[string]any{
					"accessModes": []any{"ReadWriteOnce"},
					"volumeMode":  "Filesystem",
					"resources": map[string]any{
						"requests": map[string]any{
							"storage": fmt.Sprintf("%dGi", gb),
						},
					},
				},
			},
		})
		diskName := fmt.Sprintf("datadisk%d", i+1)
		disks = append(disks, map[string]any{
			"name": diskName,
			"disk": map[string]any{"bus": "virtio"},
		})
		volumes = append(volumes, map[string]any{
			"name":       diskName,
			"dataVolume": map[string]any{"name": dvName},
		})
	}

	domain := map[string]any{
		"cpu":    map[string]any{"cores": int64(req.CPUs)},
		"memory": map[string]any{"guest": fmt.Sprintf("%dMi", req.MemoryMB)},
		"devices": map[string]any{
			"disks": disks,
			"interfaces": []any{
				map[string]any{
					"name":       "default",
					"masquerade": map[string]any{},
				},
			},
		},
	}
	// OVMF firmware without SecureBoot. Needed for installers that
	// refuse to boot under SeaBIOS or to rehearse a UEFI install. We
	// leave SecureBoot off so distros with unsigned shims still boot.
	if req.UEFI {
		domain["machine"] = map[string]any{"type": "q35"}
		domain["firmware"] = map[string]any{
			"bootloader": map[string]any{
				"efi": map[string]any{
					"secureBoot": false,
					"persistent": false,
				},
			},
		}
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]any{
			"name":      req.Name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/release":            req.Release,
				"kubego.io/os":                 "linux",
			},
		},
		"spec": map[string]any{
			"runStrategy":         "Always",
			"dataVolumeTemplates": dvTemplates,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"kubevirt.io/vm": req.Name,
					},
				},
				"spec": map[string]any{
					"domain": domain,
					"networks": []any{
						map[string]any{
							"name": "default",
							"pod":  map[string]any{},
						},
					},
					"volumes": volumes,
				},
			},
		},
	}}
}

// ListVMs returns all VMs in the driver's namespace, enriched with VMI
// status (state, IPs) where a VMI exists.
func (c *kubevirtClient) ListVMs() ([]VMInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	vms, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list VirtualMachines: %w", err)
	}
	// Pull the VMI list once and index by name so we don't do N GETs.
	vmis, err := c.dyn.Resource(vmiGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list VirtualMachineInstances: %w", err)
	}
	vmiByName := make(map[string]*unstructured.Unstructured, len(vmis.Items))
	for i := range vmis.Items {
		vmiByName[vmis.Items[i].GetName()] = &vmis.Items[i]
	}

	out := make([]VMInfo, 0, len(vms.Items))
	for i := range vms.Items {
		out = append(out, vmInfoFrom(&vms.Items[i], vmiByName[vms.Items[i].GetName()]))
	}
	return out, nil
}

// GetVMInfo fetches a single VM + its VMI (if any) and returns the merged
// view.
func (c *kubevirtClient) GetVMInfo(name string) (VMInfo, error) {
	if err := ValidateVMName(name); err != nil {
		return VMInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	vm, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return VMInfo{}, fmt.Errorf("get VirtualMachine %q: %w", name, err)
	}
	vmi, err := c.dyn.Resource(vmiGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return VMInfo{}, fmt.Errorf("get VirtualMachineInstance %q: %w", name, err)
	}
	if apierrors.IsNotFound(err) {
		vmi = nil
	}
	return vmInfoFrom(vm, vmi), nil
}

// vmInfoFrom flattens a VM + optional VMI into the wire shape the frontend
// expects. State mapping prefers the VM's status.printableStatus (set by
// virt-controller) over reconstructing the state from runStrategy+phase.
func vmInfoFrom(vm, vmi *unstructured.Unstructured) VMInfo {
	info := VMInfo{
		Name:      vm.GetName(),
		Namespace: vm.GetNamespace(),
		State:     "Unknown",
	}
	if r, ok, _ := unstructured.NestedString(vm.Object, "status", "printableStatus"); ok && r != "" {
		info.State = r
	} else {
		// Fall back to runStrategy when the VM has no status yet (just
		// created, controller hasn't observed it).
		if rs, ok, _ := unstructured.NestedString(vm.Object, "spec", "runStrategy"); ok {
			if rs == "Halted" {
				info.State = "Stopped"
			} else {
				info.State = "Starting"
			}
		}
	}

	if rel, ok, _ := unstructured.NestedString(vm.Object, "metadata", "labels", "kubego.io/release"); ok {
		info.Release = rel
	}
	if os, ok, _ := unstructured.NestedString(vm.Object, "metadata", "labels", "kubego.io/os"); ok {
		info.OS = os
	}

	// CPU cores and memory request are read from spec; the VMI mirrors
	// them but VMs may exist without a VMI (stopped).
	if cores, ok, _ := unstructured.NestedInt64(vm.Object, "spec", "template", "spec", "domain", "cpu", "cores"); ok {
		info.CPUs = fmt.Sprintf("%d", cores)
	}
	if guest, ok, _ := unstructured.NestedString(vm.Object, "spec", "template", "spec", "domain", "memory", "guest"); ok {
		info.MemoryTotal = guest
	}
	// Root-disk size from the VM's dataVolumeTemplates entry. This is
	// the PVC request size, not the filesystem-visible size — cloud-init
	// growpart expands into it on first boot.
	if dvs, ok, _ := unstructured.NestedSlice(vm.Object, "spec", "dataVolumeTemplates"); ok && len(dvs) > 0 {
		if m, isMap := dvs[0].(map[string]any); isMap {
			if size, ok, _ := unstructured.NestedString(m, "spec", "storage", "resources", "requests", "storage"); ok {
				info.DiskTotal = size
			}
		}
	}

	if vmi != nil {
		info.IPv4 = extractVMIIPs(vmi)
	}
	return info
}

func extractVMIIPs(vmi *unstructured.Unstructured) []string {
	ifaces, ok, _ := unstructured.NestedSlice(vmi.Object, "status", "interfaces")
	if !ok {
		return nil
	}
	var out []string
	for _, raw := range ifaces {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if ip, ok := m["ipAddress"].(string); ok && ip != "" && !strings.Contains(ip, ":") {
			out = append(out, ip)
		}
	}
	return out
}

// StartVM patches runStrategy to Always.
func (c *kubevirtClient) StartVM(name string) error {
	return c.setRunStrategy(name, "Always")
}

// StopVM patches runStrategy to Halted. KubeVirt tears down the VMI.
func (c *kubevirtClient) StopVM(name string) error {
	return c.setRunStrategy(name, "Halted")
}

func (c *kubevirtClient) setRunStrategy(name, strategy string) error {
	if err := ValidateVMName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	patch := []byte(fmt.Sprintf(`{"spec":{"runStrategy":%q,"running":null}}`, strategy))
	_, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patch runStrategy=%s on VM %q: %w", strategy, name, err)
	}
	return nil
}

// DeleteVM deletes the VM CR. The root DataVolume is declared via
// `spec.dataVolumeTemplates`, so KubeVirt owns it (and the PVC CDI
// provisioned from it) and both are GC'd along with the VM. `purge`
// is currently a no-op — PVC retention on delete is a follow-up once
// snapshot/export lands (see M4).
func (c *kubevirtClient) DeleteVM(name string, purge bool) error {
	if err := ValidateVMName(name); err != nil {
		return err
	}
	_ = purge // owned-DV lifecycle always tracks the VM today
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualMachine %q: %w", name, err)
	}
	return nil
}
