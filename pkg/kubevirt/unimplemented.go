package kubevirt

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// unimplementedClient is the M0 driver stub. It holds the wired
// Kubernetes clients and the driver's default namespace so future
// milestones can implement methods one file at a time without
// re-plumbing. Every VM-op method returns ErrNotImplemented until it's
// wired up.
//
// Interface compliance is a compile-time check via _ = Client(nil) at
// the bottom of this file.
type unimplementedClient struct {
	logger    *slog.Logger
	restCfg   *rest.Config
	kube      *kubernetes.Clientset
	discovery *discovery.DiscoveryClient
	namespace string
	// kubeContext is the kubeconfig context name (e.g. "kind-kubego-dev")
	// when NewClient loaded from a kubeconfig, empty otherwise.
	// Used by ClusterInfo to name the cluster; not for any routing/auth.
	kubeContext string
}

func (c *unimplementedClient) Namespace() string { return c.namespace }

// ProbeKubeVirt verifies that kubevirt.io/v1 is served by the cluster.
// Called at startup — logs a warning and returns nil on a reachable API
// server that just happens not to have KubeVirt installed yet, so the
// pod keeps running and the UI can render a "KubeVirt not detected"
// banner instead of CrashLoopBackOff.
func (c *unimplementedClient) ProbeKubeVirt(ctx context.Context) error {
	groups, err := c.discovery.ServerGroups()
	if err != nil {
		return fmt.Errorf("list API groups: %w", err)
	}
	for _, g := range groups.Groups {
		if g.Name != KubeVirtAPIGroup {
			continue
		}
		for _, v := range g.Versions {
			if v.Version == KubeVirtAPIVersion {
				c.logger.Info("kubevirt detected",
					"group", g.Name,
					"version", v.Version,
					"preferred", g.PreferredVersion.Version,
				)
				c.probeKubeVirtCR(ctx)
				return nil
			}
		}
		c.logger.Warn("kubevirt group present but v1 missing",
			"group", g.Name,
			"versions", g.Versions,
		)
		return nil
	}
	c.logger.Warn("kubevirt not installed on target cluster — VM operations will return 501 until KubeVirt is installed",
		"api_group_checked", KubeVirtAPIGroup)
	return nil
}

// probeKubeVirtCR tries to find the singleton `KubeVirt` custom
// resource that virt-operator manages, logging its observed version and
// phase. Best-effort — errors are swallowed so ProbeKubeVirt stays a
// warning, not a failure.
func (c *unimplementedClient) probeKubeVirtCR(ctx context.Context) {
	// The KubeVirt CR lives in `kubevirt` (default) or `kubevirt-system`
	// depending on install method. M1 will switch to a typed client via
	// kubevirt.io/client-go; for M0 we just note that it's reachable.
	nss := []string{"kubevirt", "kubevirt-system"}
	for _, ns := range nss {
		pods, err := c.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "kubevirt.io=virt-operator"})
		if err != nil {
			continue
		}
		if len(pods.Items) > 0 {
			c.logger.Info("kubevirt operator pods reachable", "namespace", ns, "count", len(pods.Items))
			return
		}
	}
}

// ----- lifecycle -----

func (c *unimplementedClient) ListVMs() ([]VMInfo, error)             { return nil, ErrNotImplemented }
func (c *unimplementedClient) GetVMInfo(string) (VMInfo, error)       { return VMInfo{}, ErrNotImplemented }
func (c *unimplementedClient) StartVM(string) error                   { return ErrNotImplemented }
func (c *unimplementedClient) StopVM(string) error                    { return ErrNotImplemented }
func (c *unimplementedClient) SuspendVM(string) error                 { return ErrNotImplemented }
func (c *unimplementedClient) DeleteVM(string, bool) error            { return ErrNotImplemented }
func (c *unimplementedClient) StartAll() error                        { return ErrNotImplemented }
func (c *unimplementedClient) StopAll() error                         { return ErrNotImplemented }
func (c *unimplementedClient) LaunchVM(string, string, int, int, int, string, string) (string, error) {
	return "", ErrNotImplemented
}
func (c *unimplementedClient) CloneVM(string, string) (string, error) { return "", ErrNotImplemented }

// ----- exec -----

func (c *unimplementedClient) ExecInVM(string, []string) (string, error) {
	return "", ErrNotImplemented
}
func (c *unimplementedClient) ExecInVMWithContext(context.Context, string, []string) (string, error) {
	return "", ErrNotImplemented
}
func (c *unimplementedClient) ExecInVMStreaming(context.Context, string, []string, func(string)) (string, error) {
	return "", ErrNotImplemented
}

// ----- config / state inspection -----

func (c *unimplementedClient) GetVMConfig(string) (VMConfig, error) {
	return VMConfig{}, ErrNotImplemented
}
func (c *unimplementedClient) SetVMCPUs(string, int) error    { return ErrNotImplemented }
func (c *unimplementedClient) SetVMMemory(string, int) error  { return ErrNotImplemented }
func (c *unimplementedClient) SetVMDisk(string, int) error    { return ErrNotImplemented }
func (c *unimplementedClient) GetRawInfo(string) (string, error) {
	return "", ErrNotImplemented
}
func (c *unimplementedClient) GetCloudInitStatus(string) (CloudInitStatus, error) {
	return CloudInitStatus{Status: "pending"}, ErrNotImplemented
}

// ----- snapshots -----

func (c *unimplementedClient) ListSnapshots(string) ([]SnapshotInfo, error) {
	return nil, ErrNotImplemented
}
func (c *unimplementedClient) CreateSnapshot(string, string, string) error {
	return ErrNotImplemented
}
func (c *unimplementedClient) RestoreSnapshot(string, string) error {
	return ErrNotImplemented
}
func (c *unimplementedClient) DeleteSnapshot(string, string) error {
	return ErrNotImplemented
}

// ----- disks (née mounts) -----

func (c *unimplementedClient) ListDisks(string) ([]DiskInfo, error) {
	return nil, ErrNotImplemented
}
func (c *unimplementedClient) AttachDisk(string, string, string) error { return ErrNotImplemented }
func (c *unimplementedClient) DetachDisk(string, string) error         { return ErrNotImplemented }

// ----- discovery -----

func (c *unimplementedClient) ListNetworks() ([]NetworkInfo, error) {
	return nil, ErrNotImplemented
}
func (c *unimplementedClient) FindImages() ([]ImageInfo, error) {
	return nil, ErrNotImplemented
}

// GetAllCloudInitTemplates is NOT unimplemented — it's filesystem-based
// and works against the app-state PVC without a cluster call. Routed
// here so handlers keep dispatching through the driver interface.
func (c *unimplementedClient) GetAllCloudInitTemplates(configuredDirs []string) ([]TemplateOption, error) {
	return getAllCloudInitTemplates(configuredDirs)
}

// ----- file transfer (M7) -----

func (c *unimplementedClient) TransferFromVM(string, string, io.Writer) error {
	return ErrNotImplemented
}
func (c *unimplementedClient) TransferToVM(string, string, io.Reader) error {
	return ErrNotImplemented
}

// ----- subresources -----

func (c *unimplementedClient) Console(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, ErrNotImplemented
}

// ----- observability -----

func (c *unimplementedClient) VMEvents(string) ([]EventInfo, error) {
	return nil, ErrNotImplemented
}
func (c *unimplementedClient) VMPodLogs(context.Context, string, int64, bool) (io.ReadCloser, error) {
	return nil, ErrNotImplemented
}
func (c *unimplementedClient) FindLauncherPodName(context.Context, string) (string, error) {
	return "", ErrNotImplemented
}

// ----- cluster -----

func (c *unimplementedClient) ClusterResources() (ClusterResources, error) {
	return ClusterResources{}, ErrNotImplemented
}
func (c *unimplementedClient) ClusterInfo() (ClusterInfo, error) {
	return ClusterInfo{}, ErrNotImplemented
}

// Compile-time interface check.
var _ Client = (*unimplementedClient)(nil)
