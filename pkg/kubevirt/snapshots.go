package kubevirt

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// KubeVirt snapshot API lives in its own group. We target v1beta1 which
// has been the stable version since KubeVirt 1.0 — older clusters on
// v1alpha1 will need to upgrade before these calls succeed.
var (
	vmSnapshotGVR = schema.GroupVersionResource{
		Group:    "snapshot.kubevirt.io",
		Version:  "v1beta1",
		Resource: "virtualmachinesnapshots",
	}
	vmRestoreGVR = schema.GroupVersionResource{
		Group:    "snapshot.kubevirt.io",
		Version:  "v1beta1",
		Resource: "virtualmachinerestores",
	}
)

// The CR name is vm-scoped so two VMs can hold snapshots with the same
// user-facing name without colliding in the namespace. The label keeps
// listing cheap (single selector, no string parsing).
const (
	snapshotCRPrefix    = "-snap-"
	snapshotVMLabel     = "kubego.io/vm"
	snapshotNameLabel   = "kubego.io/snapshot-name"
	snapshotCommentAnno = "kubego.io/comment"
)

func snapshotCRName(vmName, snapshotName string) string {
	return vmName + snapshotCRPrefix + snapshotName
}

func (c *kubevirtClient) ListSnapshots(vmName string) ([]SnapshotInfo, error) {
	if err := ValidateVMName(vmName); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := c.dyn.Resource(vmSnapshotGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: snapshotVMLabel + "=" + vmName,
	})
	if err != nil {
		return nil, fmt.Errorf("list VirtualMachineSnapshots: %w", err)
	}

	out := make([]SnapshotInfo, 0, len(list.Items))
	for i := range list.Items {
		s := &list.Items[i]
		name := s.GetLabels()[snapshotNameLabel]
		if name == "" {
			// Snapshot was created outside KubeGo — fall back to deriving
			// a display name by stripping the VM prefix.
			name = strings.TrimPrefix(s.GetName(), vmName+snapshotCRPrefix)
		}
		created := ""
		if ts := s.GetCreationTimestamp(); !ts.IsZero() {
			created = ts.UTC().Format(time.RFC3339)
		}
		out = append(out, SnapshotInfo{
			Instance: vmName,
			Name:     name,
			Comment:  s.GetAnnotations()[snapshotCommentAnno],
			Created:  created,
		})
	}
	// Stable order: newest first so the UI renders a coherent list even
	// before it sorts client-side.
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

func (c *kubevirtClient) CreateSnapshot(vmName, snapshotName, comment string) error {
	if err := ValidateVMName(vmName); err != nil {
		return err
	}
	if err := ValidateVMName(snapshotName); err != nil {
		return fmt.Errorf("invalid snapshot name: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	snap := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": vmSnapshotGVR.Group + "/" + vmSnapshotGVR.Version,
		"kind":       "VirtualMachineSnapshot",
		"metadata": map[string]any{
			"name":      snapshotCRName(vmName, snapshotName),
			"namespace": c.namespace,
			"labels": map[string]any{
				snapshotVMLabel:                vmName,
				snapshotNameLabel:              snapshotName,
				"app.kubernetes.io/managed-by": "kubego",
			},
			"annotations": map[string]any{
				snapshotCommentAnno: comment,
			},
		},
		"spec": map[string]any{
			"source": map[string]any{
				"apiGroup": "kubevirt.io",
				"kind":     "VirtualMachine",
				"name":     vmName,
			},
		},
	}}

	_, err := c.dyn.Resource(vmSnapshotGVR).Namespace(c.namespace).Create(ctx, snap, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("snapshot %q already exists on VM %q", snapshotName, vmName)
		}
		return fmt.Errorf("create VirtualMachineSnapshot: %w", err)
	}
	return nil
}

// RestoreSnapshot requires the VM to be stopped — KubeVirt's snapshot
// controller refuses to touch a running VMI and returns a confusing
// condition. We pre-check to give a cleaner error instead.
//
// Restore CRs are one-shot: the controller marks them Complete and leaves
// them lying around. We name the CR with a timestamp suffix so consecutive
// restores of the same snapshot don't collide.
func (c *kubevirtClient) RestoreSnapshot(vmName, snapshotName string) error {
	if err := ValidateVMName(vmName); err != nil {
		return err
	}
	if err := ValidateVMName(snapshotName); err != nil {
		return fmt.Errorf("invalid snapshot name: %w", err)
	}
	vm, err := c.GetVMInfo(vmName)
	if err != nil {
		return err
	}
	if vm.State != "Stopped" {
		return fmt.Errorf("VM %q must be stopped to restore a snapshot (current state: %s)", vmName, vm.State)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	restoreName := fmt.Sprintf("%s-restore-%s-%d", vmName, snapshotName, time.Now().Unix())
	restore := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": vmRestoreGVR.Group + "/" + vmRestoreGVR.Version,
		"kind":       "VirtualMachineRestore",
		"metadata": map[string]any{
			"name":      restoreName,
			"namespace": c.namespace,
			"labels": map[string]any{
				snapshotVMLabel:                vmName,
				snapshotNameLabel:              snapshotName,
				"app.kubernetes.io/managed-by": "kubego",
			},
		},
		"spec": map[string]any{
			"target": map[string]any{
				"apiGroup": "kubevirt.io",
				"kind":     "VirtualMachine",
				"name":     vmName,
			},
			"virtualMachineSnapshotName": snapshotCRName(vmName, snapshotName),
		},
	}}

	_, err = c.dyn.Resource(vmRestoreGVR).Namespace(c.namespace).Create(ctx, restore, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create VirtualMachineRestore: %w", err)
	}
	return nil
}

func (c *kubevirtClient) DeleteSnapshot(vmName, snapshotName string) error {
	if err := ValidateVMName(vmName); err != nil {
		return err
	}
	if err := ValidateVMName(snapshotName); err != nil {
		return fmt.Errorf("invalid snapshot name: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := c.dyn.Resource(vmSnapshotGVR).Namespace(c.namespace).Delete(ctx, snapshotCRName(vmName, snapshotName), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VirtualMachineSnapshot: %w", err)
	}
	return nil
}
