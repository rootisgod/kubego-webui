package kubevirt

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// LinuxISOLaunchRequest is the minimum a Linux installer ISO needs: a
// PVC reference to the uploaded ISO and a sizing for the blank root
// disk. Unlike the Windows path there is no unattended-install
// ConfigMap — the user drives the installer interactively via VNC
// (Graphics tab). Firmware defaults to BIOS/SeaBIOS; UEFI is opt-in for
// distros that need it (recent Debian/Ubuntu netboot, most "Secure Boot
// Ready" images) or just to rehearse a UEFI install.
type LinuxISOLaunchRequest struct {
	Name            string // VM name (RFC 1123 DNS label)
	InstallerISOPVC string // PVC name of uploaded Linux installer ISO
	CPUs            int
	MemoryMB        int
	DiskGB          int
	UEFI            bool // false → SeaBIOS; true → OVMF (no SecureBoot, so distros without signed shims still boot)
}

// LaunchLinuxISOVM creates a VM that boots an uploaded Linux installer
// ISO against a blank DataVolume. The installer writes the OS onto the
// root disk; once it reboots, the CD-ROM remains attached (harmless —
// boot order puts the installed disk first). Deleting the VM GCs the
// blank DV along with it.
func (c *kubevirtClient) LaunchLinuxISOVM(req LinuxISOLaunchRequest) (string, error) {
	if err := ValidateVMName(req.Name); err != nil {
		return "", err
	}
	if req.InstallerISOPVC == "" {
		return "", fmt.Errorf("installer_iso is required")
	}
	if req.CPUs < 1 {
		req.CPUs = DefaultCPUCores
	}
	if req.MemoryMB < 512 {
		req.MemoryMB = 2048
	}
	if req.DiskGB < 8 {
		req.DiskGB = 20
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vm := buildLinuxISOVMObject(c.namespace, req)
	if _, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Create(ctx, vm, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("create VirtualMachine: %w", err)
	}
	return req.Name, nil
}

// buildLinuxISOVMObject wires a VM whose root disk is a blank DV and
// whose CD-ROM is the uploaded ISO PVC. Boot order installs-from-CD on
// first boot, then the root disk takes over once the ISO finishes.
func buildLinuxISOVMObject(namespace string, req LinuxISOLaunchRequest) *unstructured.Unstructured {
	rootDVName := req.Name + "-root"

	disks := []any{
		// Root disk first in bootOrder so post-install reboots come up
		// from the installed OS — QEMU will skip it on the first boot
		// because it's blank, and fall through to the CD-ROM.
		map[string]any{
			"name":      "rootdisk",
			"disk":      map[string]any{"bus": "virtio"},
			"bootOrder": int64(1),
		},
		map[string]any{
			"name":      "installer",
			"cdrom":     map[string]any{"bus": "sata"},
			"bootOrder": int64(2),
		},
	}
	volumes := []any{
		map[string]any{"name": "rootdisk", "dataVolume": map[string]any{"name": rootDVName}},
		map[string]any{"name": "installer", "persistentVolumeClaim": map[string]any{"claimName": req.InstallerISOPVC}},
	}

	dvTemplate := map[string]any{
		"metadata": map[string]any{
			"name": rootDVName,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/vm":                 req.Name,
				"kubego.io/os":                 "linux",
			},
		},
		"spec": map[string]any{
			"source": map[string]any{"blank": map[string]any{}},
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
	if req.UEFI {
		domain["machine"] = map[string]any{"type": "q35"}
		// OVMF without SecureBoot — the installer's bootloader does not
		// need to be Microsoft-signed, so this matches how most Linux
		// ISOs expect UEFI firmware.
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
				"kubego.io/os":                 "linux",
				"kubego.io/release":            "iso",
			},
		},
		"spec": map[string]any{
			"runStrategy":         "Always",
			"dataVolumeTemplates": []any{dvTemplate},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"kubevirt.io/vm": req.Name,
					},
				},
				"spec": map[string]any{
					"domain": domain,
					"networks": []any{
						map[string]any{"name": "default", "pod": map[string]any{}},
					},
					"volumes": volumes,
				},
			},
		},
	}}
}
