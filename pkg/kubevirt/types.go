package kubevirt

// VMInfo represents full details about a virtual machine.
// Fields are shaped to match PassGo's wire format so the existing
// frontend renders until it is ported to the KubeVirt-native shape.
type VMInfo struct {
	Name           string     `json:"name"`
	Namespace      string     `json:"namespace"`
	State          string     `json:"state"`
	Snapshots      int        `json:"snapshots"`
	IPv4           []string   `json:"ipv4"`
	Release        string     `json:"release"`
	ImageHash      string     `json:"image_hash"`
	CPUs           string     `json:"cpus"`
	Load           string     `json:"load"`
	DiskUsage      string     `json:"disk_usage"`
	DiskTotal      string     `json:"disk_total"`
	MemoryUsage    string     `json:"memory_usage"`
	MemoryTotal    string     `json:"memory_total"`
	MemoryUsageRaw int64      `json:"memory_usage_raw"`
	MemoryTotalRaw int64      `json:"memory_total_raw"`
	DiskUsageRaw   int64      `json:"disk_usage_raw"`
	DiskTotalRaw   int64      `json:"disk_total_raw"`
	Disks          []DiskInfo `json:"disks"`
	// OS is the kubego.io/os label, used by the frontend to filter
	// Linux vs Windows VMs. Empty for VMs created before the label
	// existed — the frontend treats empty as Linux.
	OS string `json:"os,omitempty"`
}

// SnapshotInfo represents a VirtualMachineSnapshot CR.
type SnapshotInfo struct {
	Instance string   `json:"instance"`
	Name     string   `json:"name"`
	Parent   string   `json:"parent"`
	Comment  string   `json:"comment"`
	Created  string   `json:"created,omitempty"`
	Children []string `json:"children,omitempty"`
}

// DiskInfo represents a disk attached to a VM (renamed from multipass MountInfo).
// In M0 the fields still mirror the multipass mount shape; M4 reshapes this
// to a PVC-hotplug-native record once disks are implemented.
type DiskInfo struct {
	SourcePath string   `json:"source_path"`
	TargetPath string   `json:"target_path"`
	UIDMaps    []string `json:"uid_maps"`
	GIDMaps    []string `json:"gid_maps"`
}

// NetworkInfo represents a cluster network choice surfaced at VM-create time.
// For M0 this will enumerate NetworkAttachmentDefinitions plus a synthetic
// "pod" entry.
type NetworkInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// TemplateOption represents a selectable cloud-init template.
type TemplateOption struct {
	Label   string `json:"label"`
	Path    string `json:"path"`
	BuiltIn bool   `json:"builtIn,omitempty"`
}

// ImageInfo represents an entry in the VM-launch image catalogue.
// Sourced from app config in M0; populated from DataVolume source URLs
// later.
type ImageInfo struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	OS      string   `json:"os"`
	Release string   `json:"release"`
	Remote  string   `json:"remote"`
	Version string   `json:"version"`
	Type    string   `json:"type"`
}

// VMConfig holds the spec-level resource configuration of a VM
// (CPU/memory/disk). Readable even when the VM is stopped.
type VMConfig struct {
	CPUs     int   `json:"cpus"`
	MemoryMB int64 `json:"memory_mb"`
	DiskGB   int64 `json:"disk_gb"`
}

// CloudInitStatus reports the cloud-init run state inside a guest.
// Populated via qemu-guest-agent exec in M5.
type CloudInitStatus struct {
	Status string   `json:"status"`
	Detail string   `json:"detail"`
	Errors []string `json:"errors,omitempty"`
	Output string   `json:"output,omitempty"`
}

// ClusterResources replaces PassGo's HostResources. In-cluster mode
// aggregates across nodes; external-kubeconfig mode reports the cluster
// the operator is pointed at.
type ClusterResources struct {
	TotalCPUs     int     `json:"total_cpus"`
	LoadAvg1      float64 `json:"load_avg_1"`
	LoadAvg5      float64 `json:"load_avg_5"`
	LoadAvg15     float64 `json:"load_avg_15"`
	TotalMemoryMB int64   `json:"total_memory_mb"`
	UsedMemoryMB  int64   `json:"used_memory_mb"`
	TotalDiskMB   int64   `json:"total_disk_mb"`
	UsedDiskMB    int64   `json:"used_disk_mb"`
}

// ClusterInfo is the read-only metadata page shown on the cluster
// dashboard. "KVM status" is derived — a cluster runs at native speed
// only when every node advertises the kvm device resource AND the
// KubeVirt CR has not forced software emulation.
type ClusterInfo struct {
	Name              string        `json:"name"`
	Context           string        `json:"context"`
	Flavor            string        `json:"flavor"` // "kind", "k3s", "generic"
	APIServer         string        `json:"api_server"`
	KubernetesVersion string        `json:"kubernetes_version"`
	Nodes             []NodeInfo    `json:"nodes"`
	KubeVirt          KubeVirtInfo  `json:"kubevirt"`
	CDI               *CDIInfo      `json:"cdi,omitempty"`
	Virtualisation    VirtStatus    `json:"virtualisation"`
}

type NodeInfo struct {
	Name             string `json:"name"`
	Ready            bool   `json:"ready"`
	Role             string `json:"role"`
	CPUs             int64  `json:"cpus"`
	MemoryMB         int64  `json:"memory_mb"`
	DiskMB           int64  `json:"disk_mb"`
	KVMCapacity      int64  `json:"kvm_capacity"`
	OSImage          string `json:"os_image"`
	KernelVersion    string `json:"kernel_version"`
	ContainerRuntime string `json:"container_runtime"`
}

type KubeVirtInfo struct {
	Installed    bool   `json:"installed"`
	Version      string `json:"version"`
	Phase        string `json:"phase"`
	UseEmulation bool   `json:"use_emulation"`
}

type CDIInfo struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Phase     string `json:"phase"`
}

// VirtStatus rolls up "is this cluster fast?" into a single signal the
// UI can colour-code: kvm | emulation | mixed (some nodes advertise
// kvm, others don't) | none.
type VirtStatus struct {
	Mode    string `json:"mode"`    // "kvm" | "emulation" | "mixed" | "unknown"
	Summary string `json:"summary"` // human-readable explanation
}
