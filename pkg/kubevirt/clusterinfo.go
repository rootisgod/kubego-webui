package kubevirt

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	kubevirtCRGVR = schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "kubevirts",
	}
	cdiCRGVR = schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "cdis",
	}
)

// ClusterInfo collects cluster metadata for the dashboard. Each lookup
// is best-effort — an unreachable optional component (CDI) omits itself
// rather than failing the whole call. Required lookups (nodes, server
// version) propagate errors.
func (c *kubevirtClient) ClusterInfo() (ClusterInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info := ClusterInfo{
		Context:   c.kubeContext,
		Name:      displayClusterName(c.kubeContext),
		Flavor:    flavorFromContext(c.kubeContext),
		APIServer: c.restCfg.Host,
	}

	if v, err := c.discovery.ServerVersion(); err == nil {
		info.KubernetesVersion = v.GitVersion
	}

	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return info, fmt.Errorf("list nodes: %w", err)
	}
	info.Nodes = make([]NodeInfo, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		info.Nodes = append(info.Nodes, nodeInfoFrom(n))
	}

	info.KubeVirt = c.kubevirtInfo(ctx)
	info.CDI = c.cdiInfo(ctx)
	info.Virtualisation = rollupVirtStatus(info.Nodes, info.KubeVirt)

	return info, nil
}

func nodeInfoFrom(n corev1.Node) NodeInfo {
	out := NodeInfo{
		Name:             n.Name,
		Role:             nodeRole(n),
		OSImage:          n.Status.NodeInfo.OSImage,
		KernelVersion:    n.Status.NodeInfo.KernelVersion,
		ContainerRuntime: n.Status.NodeInfo.ContainerRuntimeVersion,
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			out.Ready = c.Status == corev1.ConditionTrue
		}
	}
	if cpu, ok := n.Status.Allocatable["cpu"]; ok {
		out.CPUs = cpu.Value()
	}
	if mem, ok := n.Status.Allocatable["memory"]; ok {
		out.MemoryMB = mem.Value() / (1024 * 1024)
	}
	if disk, ok := n.Status.Allocatable["ephemeral-storage"]; ok {
		out.DiskMB = disk.Value() / (1024 * 1024)
	}
	if kvm, ok := n.Status.Capacity["devices.kubevirt.io/kvm"]; ok {
		out.KVMCapacity = kvm.Value()
	}
	return out
}

func nodeRole(n corev1.Node) string {
	for k := range n.Labels {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			return strings.TrimPrefix(k, "node-role.kubernetes.io/")
		}
	}
	return "worker"
}

func (c *kubevirtClient) kubevirtInfo(ctx context.Context) KubeVirtInfo {
	cr, err := c.dyn.Resource(kubevirtCRGVR).Namespace("kubevirt").Get(ctx, "kubevirt", metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			c.logger.Warn("get kubevirt CR failed", "err", err)
		}
		return KubeVirtInfo{Installed: false}
	}
	info := KubeVirtInfo{Installed: true}
	info.Version, _, _ = unstructured.NestedString(cr.Object, "status", "observedKubeVirtVersion")
	info.Phase, _, _ = unstructured.NestedString(cr.Object, "status", "phase")
	info.UseEmulation, _, _ = unstructured.NestedBool(cr.Object, "spec", "configuration", "developerConfiguration", "useEmulation")
	return info
}

func (c *kubevirtClient) cdiInfo(ctx context.Context) *CDIInfo {
	cr, err := c.dyn.Resource(cdiCRGVR).Get(ctx, "cdi", metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			c.logger.Warn("get cdi CR failed", "err", err)
		}
		return nil
	}
	out := &CDIInfo{Installed: true}
	out.Version, _, _ = unstructured.NestedString(cr.Object, "status", "observedVersion")
	out.Phase, _, _ = unstructured.NestedString(cr.Object, "status", "phase")
	return out
}

// rollupVirtStatus combines per-node KVM capacity and the KubeVirt
// useEmulation flag into a single "is this cluster fast?" signal. The
// useEmulation flag wins — if true, KubeVirt strips /dev/kvm requests
// from virt-launcher pods regardless of what the node can offer.
func rollupVirtStatus(nodes []NodeInfo, kv KubeVirtInfo) VirtStatus {
	if kv.Installed && kv.UseEmulation {
		return VirtStatus{
			Mode:    "emulation",
			Summary: "Software emulation (KubeVirt useEmulation=true). 10–100× slower than KVM.",
		}
	}
	if len(nodes) == 0 {
		return VirtStatus{Mode: "unknown", Summary: "No nodes reported"}
	}
	var kvm, nonKVM int
	for _, n := range nodes {
		if n.KVMCapacity > 0 {
			kvm++
		} else {
			nonKVM++
		}
	}
	switch {
	case kvm > 0 && nonKVM == 0:
		return VirtStatus{
			Mode:    "kvm",
			Summary: "Hardware virtualisation (KVM) available on all nodes.",
		}
	case kvm > 0 && nonKVM > 0:
		return VirtStatus{
			Mode:    "mixed",
			Summary: fmt.Sprintf("%d node(s) offer KVM, %d do not. VMs may land on software-emulation nodes.", kvm, nonKVM),
		}
	default:
		return VirtStatus{
			Mode:    "emulation",
			Summary: "No node advertises devices.kubevirt.io/kvm. KubeVirt will need useEmulation=true to launch VMs.",
		}
	}
}

// displayClusterName strips the "kind-" prefix for KinD clusters so the
// UI shows a clean name. Unknown contexts return unchanged.
func displayClusterName(ctx string) string {
	if ctx == "" {
		return "cluster"
	}
	if strings.HasPrefix(ctx, "kind-") {
		return strings.TrimPrefix(ctx, "kind-")
	}
	return ctx
}

func flavorFromContext(ctx string) string {
	switch {
	case strings.HasPrefix(ctx, "kind-"):
		return "kind"
	case strings.Contains(ctx, "k3s"):
		return "k3s"
	default:
		return "generic"
	}
}
