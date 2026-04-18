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
	dyn dynamic.Interface
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

// UbuntuContainerDiskImage is the default root-disk source for LaunchVM in
// Slice A. We use the KubeVirt containerdisks org's Ubuntu image, keyed by
// release. containerDisk is ephemeral — disk changes are lost on pod
// restart. A follow-on slice swaps this for a DataVolume+PVC once CDI is
// in the bring-up path.
func UbuntuContainerDiskImage(release string) string {
	if release == "" {
		release = DefaultUbuntuRelease
	}
	return "quay.io/containerdisks/ubuntu:" + release
}

// LaunchVM provisions a Secret holding cloud-init user-data (if any) and
// then a VirtualMachine CR wired to a containerDisk. Returns the VM's name
// on success.
func (c *kubevirtClient) LaunchVM(name, release string, cpus, memoryMB, diskGB int, cloudInitFile, networkName string) (string, error) {
	if err := ValidateVMName(name); err != nil {
		return "", err
	}
	if release == "" {
		release = DefaultUbuntuRelease
	}
	if cpus <= 0 {
		cpus = DefaultCPUCores
	}
	if memoryMB <= 0 {
		memoryMB = DefaultRAMMB
	}
	// diskGB is intentionally ignored in Slice A — containerDisk's size is
	// the image's size. We surface it on the create API so the UI contract
	// is stable once DataVolume-backed disks land.
	_ = diskGB

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	secretName, err := c.ensureCloudInitSecret(ctx, name, cloudInitFile)
	if err != nil {
		return "", fmt.Errorf("write cloud-init secret: %w", err)
	}

	vm := buildVMObject(c.namespace, name, release, cpus, memoryMB, secretName)
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
				"vm", name, "secret", secretName, "err", err)
		}
	}

	return name, nil
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
// containerDisk root and (optional) cloudInitNoCloud user-data. The spec
// is deliberately minimal — defaults rely on KubeVirt-side defaulting for
// interface model, feature gates, etc.
func buildVMObject(namespace, name, release string, cpus, memoryMB int, cloudInitSecret string) *unstructured.Unstructured {
	disks := []any{
		map[string]any{
			"name": "containerdisk",
			"disk": map[string]any{"bus": "virtio"},
		},
	}
	volumes := []any{
		map[string]any{
			"name": "containerdisk",
			"containerDisk": map[string]any{
				"image": UbuntuContainerDiskImage(release),
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

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/release":            release,
			},
		},
		"spec": map[string]any{
			"runStrategy": "Always",
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"kubevirt.io/vm": name,
					},
				},
				"spec": map[string]any{
					"domain": map[string]any{
						"cpu":    map[string]any{"cores": int64(cpus)},
						"memory": map[string]any{"guest": fmt.Sprintf("%dMi", memoryMB)},
						"devices": map[string]any{
							"disks": disks,
							"interfaces": []any{
								map[string]any{
									"name":       "default",
									"masquerade": map[string]any{},
								},
							},
						},
					},
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

	// CPU cores and memory request are read from spec; the VMI mirrors
	// them but VMs may exist without a VMI (stopped).
	if cores, ok, _ := unstructured.NestedInt64(vm.Object, "spec", "template", "spec", "domain", "cpu", "cores"); ok {
		info.CPUs = fmt.Sprintf("%d", cores)
	}
	if guest, ok, _ := unstructured.NestedString(vm.Object, "spec", "template", "spec", "domain", "memory", "guest"); ok {
		info.MemoryTotal = guest
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

// DeleteVM deletes the VM CR. In Slice A the VM has no PVCs (containerDisk
// only) so `purge` is informational; once DataVolumes land, purge=true
// will delete the owned DataVolume+PVC and purge=false will retain them.
func (c *kubevirtClient) DeleteVM(name string, purge bool) error {
	if err := ValidateVMName(name); err != nil {
		return err
	}
	_ = purge // containerDisk VMs have no persistent storage to preserve
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualMachine %q: %w", name, err)
	}
	return nil
}
