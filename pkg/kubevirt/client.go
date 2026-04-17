package kubevirt

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubeVirtAPIGroup is the CRD group that KubeVirt serves. We probe for
// kubevirt.io/v1 at startup (and on reconnect) as a prerequisite check
// — no KubeVirt, no VM ops.
const (
	KubeVirtAPIGroup   = "kubevirt.io"
	KubeVirtAPIVersion = "v1"
)

// Config controls how the driver talks to the cluster. Zero values mean
// "in-cluster first, then $KUBECONFIG/~/.kube/config". Namespace defaults
// to the pod's own namespace when running in-cluster.
type Config struct {
	// Kubeconfig, if set, forces external-kubeconfig mode. When empty,
	// we try rest.InClusterConfig() first and fall back to $KUBECONFIG
	// or $HOME/.kube/config.
	Kubeconfig string
	// Namespace the driver operates in when no explicit namespace is
	// supplied by the caller. M0 ships single-namespace mode; M6 adds
	// multi-namespace support.
	Namespace string
}

// Client is the driver interface KubeGo handlers dispatch through. The
// interface shape was chosen to minimise the handler diff from PassGo
// — most method signatures are preserved verbatim. Methods return
// ErrNotImplemented until the corresponding milestone wires them up.
type Client interface {
	// Lifecycle
	ListVMs() ([]VMInfo, error)
	GetVMInfo(name string) (VMInfo, error)
	LaunchVM(name, release string, cpus, memoryMB, diskGB int, cloudInitFile, networkName string) (string, error)
	CloneVM(source, destName string) (string, error)
	StartVM(name string) error
	StopVM(name string) error
	SuspendVM(name string) error
	DeleteVM(name string, purge bool) error
	StartAll() error
	StopAll() error

	// Exec
	ExecInVM(vmName string, command []string) (string, error)
	ExecInVMWithContext(ctx context.Context, vmName string, command []string) (string, error)
	ExecInVMStreaming(ctx context.Context, vmName string, command []string, onLine func(string)) (string, error)

	// Config / state inspection
	GetVMConfig(name string) (VMConfig, error)
	SetVMCPUs(name string, cpus int) error
	SetVMMemory(name string, memoryMB int) error
	SetVMDisk(name string, diskGB int) error
	GetRawInfo(name string) (string, error)
	GetCloudInitStatus(vmName string) (CloudInitStatus, error)

	// Snapshots
	ListSnapshots(vmName string) ([]SnapshotInfo, error)
	CreateSnapshot(vmName, snapshotName, comment string) error
	RestoreSnapshot(vmName, snapshotName string) error
	DeleteSnapshot(vmName, snapshotName string) error

	// Disks (née Mounts in PassGo; hot-plug PVC attachments in M4)
	ListDisks(vmName string) ([]DiskInfo, error)
	AttachDisk(vmName, source, target string) error
	DetachDisk(vmName, target string) error

	// Discovery
	ListNetworks() ([]NetworkInfo, error)
	FindImages() ([]ImageInfo, error)
	GetAllCloudInitTemplates(configuredDirs []string) ([]TemplateOption, error)

	// File transfer (ugly port; M7)
	TransferFromVM(vmName, remotePath string, w io.Writer) error
	TransferToVM(vmName, remotePath string, r io.Reader) error

	// Cluster-level metrics (replaces PassGo's per-host resource call)
	ClusterResources() (ClusterResources, error)

	// Lifecycle / health
	ProbeKubeVirt(ctx context.Context) error
	Namespace() string
}

// NewClient builds a Client against Kubernetes. It does NOT fail when
// KubeVirt is absent — that's reported via ProbeKubeVirt so the binary
// stays up and the UI can render "KubeVirt not installed" instead of
// crash-looping the pod.
func NewClient(logger *slog.Logger, cfg Config) (Client, error) {
	restCfg, source, err := loadRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build kube rest config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}

	discovery, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build discovery client: %w", err)
	}

	ns := cfg.Namespace
	if ns == "" {
		ns = inferNamespace(cfg.Kubeconfig)
	}
	if ns == "" {
		ns = "default"
	}

	logger.Info("kubevirt driver ready",
		"source", source,
		"namespace", ns,
		"server", restCfg.Host,
	)

	return &unimplementedClient{
		logger:    logger,
		restCfg:   restCfg,
		kube:      kubeClient,
		discovery: discovery,
		namespace: ns,
	}, nil
}

// loadRestConfig tries in-cluster first, then falls back to the supplied
// kubeconfig path, then $KUBECONFIG, then ~/.kube/config.
// Returns the config and a short string identifying which source won —
// useful in startup logs when debugging "why is it talking to the wrong
// cluster".
func loadRestConfig(kubeconfig string) (*rest.Config, string, error) {
	if kubeconfig == "" {
		if c, err := rest.InClusterConfig(); err == nil {
			return c, "in-cluster", nil
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

	c, err := clientCfg.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}

	source := "kubeconfig"
	if kubeconfig != "" {
		source = "kubeconfig:" + kubeconfig
	} else if env := os.Getenv("KUBECONFIG"); env != "" {
		source = "kubeconfig:$KUBECONFIG"
	} else if home, _ := os.UserHomeDir(); home != "" {
		source = "kubeconfig:" + filepath.Join(home, ".kube", "config")
	}
	return c, source, nil
}

// inferNamespace returns the default namespace from:
//  1. the service account token (when running in-cluster)
//  2. the current kubeconfig context
//
// An empty return means "couldn't determine"; the caller substitutes "default".
func inferNamespace(kubeconfig string) string {
	const saNs = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if data, err := os.ReadFile(saNs); err == nil {
		if ns := string(data); ns != "" {
			return ns
		}
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	if ns, _, err := clientCfg.Namespace(); err == nil {
		return ns
	}
	return ""
}
