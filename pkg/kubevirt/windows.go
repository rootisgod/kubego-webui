package kubevirt

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"text/template"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// WindowsLaunchRequest captures everything the Windows install flow needs.
// Two of the ISO references are PVC names (from the image upload flow);
// the rest are tuning knobs the UI exposes in a form.
type WindowsLaunchRequest struct {
	Name               string // VM name (RFC 1123 DNS label)
	InstallerISOPVC    string // PVC name of uploaded Windows installer ISO
	VirtioWinISOPVC    string // PVC name of uploaded virtio-win ISO (empty = no drivers)
	Hostname           string // Windows hostname; defaults to VM name
	AdminPassword      string // Administrator password for unattended install
	EnableRDP          bool   // opens Remote Desktop firewall rule + registry flag
	CPUs               int
	MemoryMB           int
	DiskGB             int
	SecureBoot         bool // needed for Windows 11; forces SMM on
	TPM                bool // needed for Windows 11
}

// LaunchWindowsVM provisions the ConfigMap holding autounattend.xml
// and the VirtualMachine CR that boots from the uploaded installer ISO.
// After Windows runs through setup it reboots, comes up on the blank
// root disk with RDP enabled, and the user reaches it via the Connect
// panel (port-forward :3389 + a downloadable .rdp snippet).
func (c *kubevirtClient) LaunchWindowsVM(req WindowsLaunchRequest) (string, error) {
	if err := ValidateVMName(req.Name); err != nil {
		return "", err
	}
	if req.InstallerISOPVC == "" {
		return "", fmt.Errorf("installer_iso is required")
	}
	if req.AdminPassword == "" {
		return "", fmt.Errorf("admin_password is required")
	}
	if req.Hostname == "" {
		req.Hostname = req.Name
	}
	if req.CPUs < 1 {
		req.CPUs = 4
	}
	if req.MemoryMB < 2048 {
		req.MemoryMB = 8192
	}
	if req.DiskGB < 40 {
		req.DiskGB = 40
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Autounattend ConfigMap — KubeVirt converts it to a virtual
	// floppy/CD-ROM that Windows setup consumes during pass 1. The key
	// name "autounattend.xml" is what Windows setup probes for.
	autounattend, err := renderAutounattend(req)
	if err != nil {
		return "", fmt.Errorf("render autounattend: %w", err)
	}
	cmName := req.Name + "-autounattend"
	if err := c.createAutounattendConfigMap(ctx, cmName, autounattend); err != nil {
		return "", err
	}

	vm := buildWindowsVMObject(c.namespace, req, cmName)
	created, err := c.dyn.Resource(vmGVR).Namespace(c.namespace).Create(ctx, vm, metav1.CreateOptions{})
	if err != nil {
		// Roll back the ConfigMap — without the VM to own it, it leaks.
		_ = c.kube.CoreV1().ConfigMaps(c.namespace).Delete(ctx, cmName, metav1.DeleteOptions{})
		return "", fmt.Errorf("create VirtualMachine: %w", err)
	}

	// Own the ConfigMap from the VM so deleting the VM cleans it up.
	if err := c.setConfigMapOwner(ctx, cmName, created); err != nil {
		c.logger.Warn("set autounattend configmap owner-ref failed (cm will outlive VM)",
			"vm", req.Name, "cm", cmName, "err", err)
	}
	return req.Name, nil
}

func (c *kubevirtClient) createAutounattendConfigMap(ctx context.Context, name, xml string) error {
	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": c.namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
			},
		},
		"data": map[string]any{
			"autounattend.xml": xml,
			"unattend.xml":     xml,
		},
	}}
	_, err := c.dyn.Resource(cmGVR).Namespace(c.namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create autounattend ConfigMap: %w", err)
	}
	return nil
}

func (c *kubevirtClient) setConfigMapOwner(ctx context.Context, cmName string, vm *unstructured.Unstructured) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"ownerReferences":[{"apiVersion":%q,"kind":%q,"name":%q,"uid":%q,"controller":true,"blockOwnerDeletion":true}]}}`,
		vm.GetAPIVersion(), vm.GetKind(), vm.GetName(), string(vm.GetUID())))
	_, err := c.kube.CoreV1().ConfigMaps(c.namespace).Patch(ctx, cmName, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// buildWindowsVMObject lays out the VM spec that Windows setup needs:
//   - q35 machine (UEFI needs it)
//   - EFI + SecureBoot + SMM (for Windows 11; Windows 10 tolerates them)
//   - TPM device (Windows 11 hard requirement)
//   - blank root DV on virtio — Windows only sees it after virtio-win
//     drivers are loaded, which is why we attach the driver ISO
//   - two CD-ROMs (installer + virtio-win) on sata, because sata works
//     without drivers
//   - sysprep volume with autounattend.xml from a ConfigMap
//   - masquerade network (same as Linux path)
//
// Firmware UUID is left unset so KubeVirt generates a stable one; setting
// it ourselves would bind this function to the machine-id-hash that
// Windows licensing uses.
func buildWindowsVMObject(namespace string, req WindowsLaunchRequest, autounattendConfigMap string) *unstructured.Unstructured {
	rootDVName := req.Name + "-root"

	disks := []any{
		// Root disk on virtio — faster, but needs virtio-win drivers
		// loaded during setup to be visible.
		map[string]any{
			"name":      "rootdisk",
			"disk":      map[string]any{"bus": "virtio"},
			"bootOrder": int64(3),
		},
		// Installer ISO boots first.
		map[string]any{
			"name":      "installer",
			"cdrom":     map[string]any{"bus": "sata"},
			"bootOrder": int64(1),
		},
	}
	volumes := []any{
		map[string]any{"name": "rootdisk", "dataVolume": map[string]any{"name": rootDVName}},
		map[string]any{"name": "installer", "persistentVolumeClaim": map[string]any{"claimName": req.InstallerISOPVC}},
	}
	if req.VirtioWinISOPVC != "" {
		disks = append(disks, map[string]any{
			"name":      "virtio",
			"cdrom":     map[string]any{"bus": "sata"},
			"bootOrder": int64(2),
		})
		volumes = append(volumes, map[string]any{
			"name":                  "virtio",
			"persistentVolumeClaim": map[string]any{"claimName": req.VirtioWinISOPVC},
		})
	}
	// Sysprep disk — KubeVirt formats the ConfigMap as a tiny CD-ROM
	// that Windows setup looks at in the windowsPE pass.
	disks = append(disks, map[string]any{
		"name":  "sysprep",
		"cdrom": map[string]any{"bus": "sata"},
	})
	volumes = append(volumes, map[string]any{
		"name":    "sysprep",
		"sysprep": map[string]any{"configMap": map[string]any{"name": autounattendConfigMap}},
	})

	// Blank DataVolume for the root disk — Windows setup partitions and
	// formats it. Size is the user-requested value.
	dvTemplate := map[string]any{
		"metadata": map[string]any{
			"name": rootDVName,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/vm":                 req.Name,
				"kubego.io/os":                 "windows",
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
		"cpu": map[string]any{"cores": int64(req.CPUs)},
		"memory": map[string]any{
			"guest": fmt.Sprintf("%dMi", req.MemoryMB),
		},
		"machine": map[string]any{"type": "q35"},
		"firmware": map[string]any{
			"bootloader": map[string]any{
				"efi": map[string]any{
					"secureBoot": req.SecureBoot,
					"persistent": req.SecureBoot,
				},
			},
		},
		"devices": map[string]any{
			"disks": disks,
			"interfaces": []any{
				map[string]any{
					"name":       "default",
					"masquerade": map[string]any{},
					"model":      "e1000e", // virtio-net needs virtio-win drivers; e1000e works out of the box
				},
			},
		},
	}
	// SecureBoot on x86 requires SMM; Windows 11 additionally wants vTPM.
	features := map[string]any{
		"acpi": map[string]any{"enabled": true},
	}
	if req.SecureBoot {
		features["smm"] = map[string]any{"enabled": true}
	}
	domain["features"] = features
	if req.TPM {
		devices := domain["devices"].(map[string]any)
		devices["tpm"] = map[string]any{}
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]any{
			"name":      req.Name,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "kubego",
				"kubego.io/os":                 "windows",
				"kubego.io/release":            "windows",
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

// autounattendTemplate is a trimmed Microsoft autounattend.xml covering
// the windowsPE (disk partitioning + image selection + driver import
// from virtio-win) and oobeSystem (user account, RDP enable) passes.
// Windows picks up autounattend.xml automatically when it finds it at
// the root of any attached volume.
const autounattendTemplate = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>en-US</InputLocale>
      <SystemLocale>en-US</SystemLocale>
      <UILanguage>en-US</UILanguage>
      <UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add">
          <CreatePartitions>
            <CreatePartition wcm:action="add"><Order>1</Order><Type>Primary</Type><Extend>true</Extend></CreatePartition>
          </CreatePartitions>
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
        </Disk>
        <WillShowUI>OnError</WillShowUI>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallTo><DiskID>0</DiskID><PartitionID>1</PartitionID></InstallTo>
          <InstallFrom>
            <MetaData wcm:action="add"><Key>/IMAGE/INDEX</Key><Value>1</Value></MetaData>
          </InstallFrom>
          <WillShowUI>OnError</WillShowUI>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
        <Organization>KubeGo</Organization>
        <FullName>Administrator</FullName>
      </UserData>
      <DriverPaths>
        <PathAndCredentials wcm:action="add" wcm:keyValue="1"><Path>D:\</Path></PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="2"><Path>E:\</Path></PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="3"><Path>F:\</Path></PathAndCredentials>
      </DriverPaths>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <NetworkLocation>Work</NetworkLocation>
        <ProtectYourPC>3</ProtectYourPC>
        <SkipUserOOBE>true</SkipUserOOBE>
        <SkipMachineOOBE>true</SkipMachineOOBE>
      </OOBE>
      <UserAccounts>
        <AdministratorPassword>
          <Value>{{.AdminPassword}}</Value>
          <PlainText>true</PlainText>
        </AdministratorPassword>
      </UserAccounts>
      <ComputerName>{{.Hostname}}</ComputerName>
      <TimeZone>UTC</TimeZone>
      {{if .EnableRDP}}<FirstLogonCommands>
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>powershell.exe -NoProfile -Command "Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name 'fDenyTSConnections' -Value 0"</CommandLine>
        </SynchronousCommand>
        <SynchronousCommand wcm:action="add">
          <Order>2</Order>
          <CommandLine>powershell.exe -NoProfile -Command "Enable-NetFirewallRule -DisplayGroup 'Remote Desktop'"</CommandLine>
        </SynchronousCommand>
      </FirstLogonCommands>{{end}}
    </component>
  </settings>
</unattend>
`

// renderAutounattend XML-escapes user inputs then expands the template.
// html.EscapeString covers the five XML predefined entities — enough for
// the fields we splice in (hostname, admin password) which never contain
// structural markup.
func renderAutounattend(req WindowsLaunchRequest) (string, error) {
	data := struct {
		Hostname      string
		AdminPassword string
		EnableRDP     bool
	}{
		Hostname:      html.EscapeString(req.Hostname),
		AdminPassword: html.EscapeString(req.AdminPassword),
		EnableRDP:     req.EnableRDP,
	}
	tmpl, err := template.New("autounattend").Parse(autounattendTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
