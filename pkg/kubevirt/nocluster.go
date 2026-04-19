package kubevirt

import (
	"context"
	"errors"
	"io"
)

// NoActiveClusterContext is the sentinel active-context value when the
// registry has no usable cluster at startup (empty kubeconfig, no
// in-cluster config). Handlers that depend on a live Client will get
// ErrNoActiveCluster until the user creates or selects a real context.
const NoActiveClusterContext = ""

// ErrNoActiveCluster is returned by noClusterClient methods and signals
// "create a cluster or select an existing context before retrying".
var ErrNoActiveCluster = errors.New("no active cluster: create one via the cluster switcher or populate your kubeconfig")

// noClusterClient is the Client installed when NewRegistry found no
// usable contexts. Every VM-scoped method returns ErrNoActiveCluster;
// ProbeKubeVirt and Namespace are benign so startup logs and the /host
// panel still render.
type noClusterClient struct{}

func (noClusterClient) Namespace() string                      { return "default" }
func (noClusterClient) ProbeKubeVirt(context.Context) error    { return nil }
func (noClusterClient) ListVMs() ([]VMInfo, error)             { return nil, ErrNoActiveCluster }
func (noClusterClient) GetVMInfo(string) (VMInfo, error)       { return VMInfo{}, ErrNoActiveCluster }
func (noClusterClient) StartVM(string) error                   { return ErrNoActiveCluster }
func (noClusterClient) StopVM(string) error                    { return ErrNoActiveCluster }
func (noClusterClient) SuspendVM(string) error                 { return ErrNoActiveCluster }
func (noClusterClient) DeleteVM(string, bool) error            { return ErrNoActiveCluster }
func (noClusterClient) StartAll() error                        { return ErrNoActiveCluster }
func (noClusterClient) StopAll() error                         { return ErrNoActiveCluster }
func (noClusterClient) LaunchVM(string, string, int, int, int, string, string) (string, error) {
	return "", ErrNoActiveCluster
}
func (noClusterClient) CloneVM(string, string) (string, error) { return "", ErrNoActiveCluster }

func (noClusterClient) ExecInVM(string, []string) (string, error) {
	return "", ErrNoActiveCluster
}
func (noClusterClient) ExecInVMWithContext(context.Context, string, []string) (string, error) {
	return "", ErrNoActiveCluster
}
func (noClusterClient) ExecInVMStreaming(context.Context, string, []string, func(string)) (string, error) {
	return "", ErrNoActiveCluster
}

func (noClusterClient) GetVMConfig(string) (VMConfig, error)   { return VMConfig{}, ErrNoActiveCluster }
func (noClusterClient) SetVMCPUs(string, int) error            { return ErrNoActiveCluster }
func (noClusterClient) SetVMMemory(string, int) error          { return ErrNoActiveCluster }
func (noClusterClient) SetVMDisk(string, int) error            { return ErrNoActiveCluster }
func (noClusterClient) GetRawInfo(string) (string, error)      { return "", ErrNoActiveCluster }
func (noClusterClient) GetCloudInitStatus(string) (CloudInitStatus, error) {
	return CloudInitStatus{}, ErrNoActiveCluster
}

func (noClusterClient) ListSnapshots(string) ([]SnapshotInfo, error)   { return nil, ErrNoActiveCluster }
func (noClusterClient) CreateSnapshot(string, string, string) error    { return ErrNoActiveCluster }
func (noClusterClient) RestoreSnapshot(string, string) error           { return ErrNoActiveCluster }
func (noClusterClient) DeleteSnapshot(string, string) error            { return ErrNoActiveCluster }

func (noClusterClient) ListDisks(string) ([]DiskInfo, error)           { return nil, ErrNoActiveCluster }
func (noClusterClient) AttachDisk(string, string, string) error        { return ErrNoActiveCluster }
func (noClusterClient) DetachDisk(string, string) error                { return ErrNoActiveCluster }

func (noClusterClient) ListNetworks() ([]NetworkInfo, error)           { return nil, ErrNoActiveCluster }
func (noClusterClient) FindImages() ([]ImageInfo, error)               { return nil, ErrNoActiveCluster }

// Cloud-init templates are filesystem-backed and cluster-agnostic, so
// honour them even without an active cluster.
func (noClusterClient) GetAllCloudInitTemplates(configuredDirs []string) ([]TemplateOption, error) {
	return getAllCloudInitTemplates(configuredDirs)
}

func (noClusterClient) TransferFromVM(string, string, io.Writer) error { return ErrNoActiveCluster }
func (noClusterClient) TransferToVM(string, string, io.Reader) error   { return ErrNoActiveCluster }

func (noClusterClient) Console(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, ErrNoActiveCluster
}
func (noClusterClient) VNC(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, ErrNoActiveCluster
}

func (noClusterClient) VMEvents(string) ([]EventInfo, error) { return nil, ErrNoActiveCluster }
func (noClusterClient) VMPodLogs(context.Context, string, int64, bool) (io.ReadCloser, error) {
	return nil, ErrNoActiveCluster
}
func (noClusterClient) FindLauncherPodName(context.Context, string) (string, error) {
	return "", ErrNoActiveCluster
}
func (noClusterClient) StartPortForward(context.Context, string, int) (int, func(), error) {
	return 0, nil, ErrNoActiveCluster
}

func (noClusterClient) IngressControllerStatus() (IngressControllerStatus, error) {
	return IngressControllerStatus{}, ErrNoActiveCluster
}
func (noClusterClient) ListVMIngresses(string) ([]IngressInfo, error) {
	return nil, ErrNoActiveCluster
}
func (noClusterClient) ExposeVMPort(string, int) (IngressInfo, error) {
	return IngressInfo{}, ErrNoActiveCluster
}
func (noClusterClient) DeleteVMIngress(string, string) error { return ErrNoActiveCluster }

func (noClusterClient) ListImageUploads() ([]ImageUpload, error) {
	return nil, ErrNoActiveCluster
}
func (noClusterClient) CreateImageUpload(string, string, string, int) error {
	return ErrNoActiveCluster
}
func (noClusterClient) CreateImageImport(string, string, string, int, string) error {
	return ErrNoActiveCluster
}
func (noClusterClient) UploadImageBytes(context.Context, string, io.Reader, int64) error {
	return ErrNoActiveCluster
}
func (noClusterClient) DeleteImageUpload(string) error { return ErrNoActiveCluster }
func (noClusterClient) LaunchWindowsVM(WindowsLaunchRequest) (string, error) {
	return "", ErrNoActiveCluster
}

func (noClusterClient) ClusterResources() (ClusterResources, error) {
	return ClusterResources{}, ErrNoActiveCluster
}
func (noClusterClient) ClusterInfo() (ClusterInfo, error) {
	return ClusterInfo{}, ErrNoActiveCluster
}

var _ Client = noClusterClient{}
