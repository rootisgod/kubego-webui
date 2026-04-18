package kubevirt

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterResources aggregates node capacity and pod/PVC requests across the
// cluster. There is no cluster-wide load average — metrics.k8s.io would be
// needed for live CPU usage and it isn't installed on KinD by default. We
// substitute "sum of pod CPU requests" for load_avg_* so the UI's ratio-to-
// capacity card still renders something meaningful.
func (c *kubevirtClient) ClusterResources() (ClusterResources, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterResources{}, err
	}

	var totalCPU, totalMemMB, totalDiskMB int64
	for _, n := range nodes.Items {
		alloc := n.Status.Allocatable
		if cpu, ok := alloc["cpu"]; ok {
			totalCPU += cpu.Value()
		}
		if mem, ok := alloc["memory"]; ok {
			totalMemMB += mem.Value() / (1024 * 1024)
		}
		if disk, ok := alloc["ephemeral-storage"]; ok {
			totalDiskMB += disk.Value() / (1024 * 1024)
		}
	}

	pods, err := c.kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterResources{}, err
	}
	var usedCPUMilli, usedMemMB int64
	for _, p := range pods.Items {
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			if cpu, ok := ctr.Resources.Requests["cpu"]; ok {
				usedCPUMilli += cpu.MilliValue()
			}
			if mem, ok := ctr.Resources.Requests["memory"]; ok {
				usedMemMB += mem.Value() / (1024 * 1024)
			}
		}
	}

	pvcs, err := c.kube.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterResources{}, err
	}
	var usedDiskMB int64
	for _, p := range pvcs.Items {
		if p.Status.Phase != "Bound" {
			continue
		}
		if q, ok := p.Spec.Resources.Requests["storage"]; ok {
			usedDiskMB += q.Value() / (1024 * 1024)
		}
	}

	loadCores := float64(usedCPUMilli) / 1000
	return ClusterResources{
		TotalCPUs:     int(totalCPU),
		LoadAvg1:      loadCores,
		LoadAvg5:      loadCores,
		LoadAvg15:     loadCores,
		TotalMemoryMB: totalMemMB,
		UsedMemoryMB:  usedMemMB,
		TotalDiskMB:   totalDiskMB,
		UsedDiskMB:    usedDiskMB,
	}, nil
}
