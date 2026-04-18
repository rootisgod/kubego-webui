package kubevirt

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EventInfo is a flattened kubernetes Event scoped to a single VM. We
// merge events across the three objects that matter at creation time
// — VirtualMachine, VirtualMachineInstance, and the virt-launcher Pod
// — and sort newest first so the UI can render a plain feed without
// per-row expand/collapse machinery.
type EventInfo struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Object  string    `json:"object"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	Count   int32     `json:"count,omitempty"`
}

// VMEvents returns cluster events for the VM, its VMI (if running), and
// the virt-launcher pod (if scheduled). Events that don't relate to any
// of those are filtered out.
func (c *kubevirtClient) VMEvents(vmName string) ([]EventInfo, error) {
	if err := ValidateVMName(vmName); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Find the virt-launcher pod name(s) for the VM. Tolerate "no pod"
	// during creation — the events list still has VM/VMI events.
	launcherPods, err := c.findLauncherPods(ctx, vmName)
	if err != nil {
		return nil, err
	}
	podNames := make(map[string]bool, len(launcherPods))
	for _, p := range launcherPods {
		podNames[p.Name] = true
	}

	ev, err := c.kube.CoreV1().Events(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	out := make([]EventInfo, 0, 16)
	for _, e := range ev.Items {
		if !matchesVM(e.InvolvedObject, vmName, podNames) {
			continue
		}
		t := eventTime(e)
		out = append(out, EventInfo{
			Time:    t,
			Type:    e.Type,
			Reason:  e.Reason,
			Object:  e.InvolvedObject.Name,
			Kind:    e.InvolvedObject.Kind,
			Message: e.Message,
			Count:   e.Count,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// VMPodLogs streams logs from the virt-launcher pod's compute container.
// Returns ErrNotRunning (wrapped) when no launcher pod exists yet —
// callers should surface this as "VM not running" rather than 500.
// Caller owns the returned ReadCloser.
func (c *kubevirtClient) VMPodLogs(ctx context.Context, vmName string, tailLines int64, follow bool) (io.ReadCloser, error) {
	if err := ValidateVMName(vmName); err != nil {
		return nil, err
	}
	// Independent context for the launcher lookup so cancellation of the
	// caller's ctx doesn't poison the short pre-flight query.
	lookupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pods, err := c.findLauncherPods(lookupCtx, vmName)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no virt-launcher pod yet for VM %q", vmName)
	}
	// Pick the most-recently created pod (handles stale Terminating ones
	// sticking around after a restart).
	pod := pickNewest(pods)

	opts := &corev1.PodLogOptions{
		Container: "compute",
		Follow:    follow,
	}
	if tailLines > 0 {
		opts.TailLines = &tailLines
	}
	req := c.kube.CoreV1().Pods(c.namespace).GetLogs(pod.Name, opts)
	return req.Stream(ctx)
}

func (c *kubevirtClient) findLauncherPods(ctx context.Context, vmName string) ([]corev1.Pod, error) {
	// vm.kubevirt.io/name is set by virt-controller on the VMI spec, and
	// virt-launcher copies VMI labels onto its pod. This is more reliable
	// than kubevirt.io/vm (which is only set if we set it on our own
	// VMI template).
	sel := fmt.Sprintf("kubevirt.io=virt-launcher,vm.kubevirt.io/name=%s", vmName)
	pods, err := c.kube.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list launcher pods: %w", err)
	}
	return pods.Items, nil
}

func pickNewest(pods []corev1.Pod) corev1.Pod {
	newest := pods[0]
	for _, p := range pods[1:] {
		if p.CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = p
		}
	}
	return newest
}

func matchesVM(obj corev1.ObjectReference, vmName string, podNames map[string]bool) bool {
	switch obj.Kind {
	case "VirtualMachine", "VirtualMachineInstance":
		return obj.Name == vmName
	case "Pod":
		return podNames[obj.Name] || strings.HasPrefix(obj.Name, "virt-launcher-"+vmName+"-")
	}
	return false
}

// eventTime prefers LastTimestamp (for repeated events), falling back to
// EventTime (used by the newer events.k8s.io API) and then FirstTimestamp.
func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.FirstTimestamp.Time
}
