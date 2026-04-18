package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Machine Check surfaces host-level prerequisites the server depends on:
// the external CLIs we shell out to (kind, kubectl, docker, task) and
// the linux kernel sysctls that bite kind clusters in practice. Read
// via GET /host/check; transient /proc/sys writes via POST /host/sysctl.

type toolCheck struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Required  bool   `json:"required"`
	Reason    string `json:"reason"` // why we care about this tool
	Error     string `json:"error,omitempty"`
}

type sysctlCheck struct {
	Key         string `json:"key"`
	Current     string `json:"current,omitempty"`
	Recommended string `json:"recommended"`
	Ok          bool   `json:"ok"`
	Reason      string `json:"reason"`
	Error       string `json:"error,omitempty"`
}

type hostCheckResponse struct {
	OS          string        `json:"os"`
	InCluster   bool          `json:"in_cluster"`
	CanApply    bool          `json:"can_apply"` // linux && running as root && not in-cluster
	ApplyReason string        `json:"apply_reason,omitempty"`
	Tools       []toolCheck   `json:"tools"`
	Sysctls     []sysctlCheck `json:"sysctls"`
	PersistHint string        `json:"persist_hint,omitempty"`
}

// Recommended sysctl values. Numbers match kind's documented minimums
// for running more than one cluster on a single host.
// https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files
var sysctlTargets = []struct {
	Key      string
	ProcPath string
	Target   int64
	Reason   string
}{
	{
		Key:      "fs.inotify.max_user_instances",
		ProcPath: "/proc/sys/fs/inotify/max_user_instances",
		Target:   512,
		Reason:   "Each kind cluster's control plane consumes inotify instances; the Ubuntu default of 128 is too low for >1 cluster and causes coredns/virt-operator watchers to fail silently.",
	},
	{
		Key:      "fs.inotify.max_user_watches",
		ProcPath: "/proc/sys/fs/inotify/max_user_watches",
		Target:   524288,
		Reason:   "Low watch limits make kubelet/containerd watchers thrash on busy clusters.",
	},
}

// Tools checked at GET /host/check. Versions come from a short-timeout
// subprocess, so a missing binary degrades gracefully.
var toolTargets = []struct {
	Name     string
	Args     []string // version arguments
	Required bool
	Reason   string
}{
	{"kind", []string{"version"}, true, "Required by the UI's Create KinD Cluster flow."},
	{"kubectl", []string{"version", "--client=true"}, true, "Required to install KubeVirt + CDI into a freshly-created KinD cluster."},
	{"docker", []string{"--version"}, true, "kind runs its nodes as Docker containers."},
	{"task", []string{"--version"}, false, "Optional — the repo's Taskfile targets are convenience wrappers."},
}

func (s *Server) handleHostCheck(w http.ResponseWriter, r *http.Request) {
	inCluster := s.clusters.InCluster()
	resp := hostCheckResponse{
		OS:        runtime.GOOS,
		InCluster: inCluster,
		Tools:     checkTools(r.Context()),
		Sysctls:   checkSysctls(),
	}

	// CanApply gates the Apply button: we need to be on linux, running
	// as root, and NOT in-cluster (a pod wouldn't have host /proc/sys
	// access anyway).
	switch {
	case inCluster:
		resp.CanApply = false
		resp.ApplyReason = "disabled when the server is running in-cluster"
	case runtime.GOOS != "linux":
		resp.CanApply = false
		resp.ApplyReason = fmt.Sprintf("sysctl apply is linux-only (server is %s)", runtime.GOOS)
	case os.Geteuid() != 0:
		resp.CanApply = false
		resp.ApplyReason = "the server process must run as root to write /proc/sys/fs/inotify/*"
	default:
		resp.CanApply = true
		resp.PersistHint = "Transient apply writes /proc/sys. To persist across reboots: " +
			"sudo tee /etc/sysctl.d/99-kubego.conf <<EOF\n" +
			"fs.inotify.max_user_instances=512\n" +
			"fs.inotify.max_user_watches=524288\n" +
			"EOF\n" +
			"sudo sysctl --system"
	}

	writeJSON(w, http.StatusOK, resp)
}

type sysctlApplyResult struct {
	Key      string `json:"key"`
	Applied  bool   `json:"applied"`
	NewValue string `json:"new_value,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleHostSysctlApply(w http.ResponseWriter, r *http.Request) {
	if s.clusters.InCluster() {
		writeError(w, http.StatusPreconditionFailed, "sysctl apply is disabled when running in-cluster")
		return
	}
	if runtime.GOOS != "linux" {
		writeError(w, http.StatusPreconditionFailed, "sysctl apply is linux-only")
		return
	}
	if os.Geteuid() != 0 {
		writeError(w, http.StatusPreconditionFailed, "the server process must run as root to write /proc/sys/fs/inotify/*")
		return
	}

	results := make([]sysctlApplyResult, 0, len(sysctlTargets))
	for _, t := range sysctlTargets {
		res := sysctlApplyResult{Key: t.Key}
		target := strconv.FormatInt(t.Target, 10)
		if err := os.WriteFile(t.ProcPath, []byte(target+"\n"), 0o644); err != nil {
			res.Error = err.Error()
		} else {
			// Read back so the UI can display the post-apply value in
			// case the kernel clamped it below the requested number.
			if b, err := os.ReadFile(t.ProcPath); err == nil {
				res.NewValue = strings.TrimSpace(string(b))
			} else {
				res.NewValue = target
			}
			res.Applied = true
		}
		results = append(results, res)
	}

	s.logger.Info("host sysctls applied", "results", results)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func checkTools(ctx context.Context) []toolCheck {
	out := make([]toolCheck, 0, len(toolTargets))
	for _, t := range toolTargets {
		c := toolCheck{Name: t.Name, Required: t.Required, Reason: t.Reason}
		path, err := exec.LookPath(t.Name)
		if err != nil {
			c.Available = false
			if t.Required {
				c.Error = "not found on PATH"
			}
			out = append(out, c)
			continue
		}
		c.Available = true
		c.Path = path
		c.Version = firstVersionLine(ctx, t.Name, t.Args)
		out = append(out, c)
	}
	return out
}

// firstVersionLine runs `<name> <args...>` with a short timeout and
// returns the first non-empty line of merged stdout+stderr. Empty
// string on failure — the UI just shows "unknown" in that case.
func firstVersionLine(ctx context.Context, name string, args []string) string {
	sub, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sub, name, args...)
	output, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func checkSysctls() []sysctlCheck {
	out := make([]sysctlCheck, 0, len(sysctlTargets))
	for _, t := range sysctlTargets {
		c := sysctlCheck{
			Key:         t.Key,
			Recommended: strconv.FormatInt(t.Target, 10),
			Reason:      t.Reason,
		}
		if runtime.GOOS != "linux" {
			c.Error = "linux-only sysctl"
			out = append(out, c)
			continue
		}
		b, err := os.ReadFile(t.ProcPath)
		if err != nil {
			c.Error = err.Error()
			out = append(out, c)
			continue
		}
		current := strings.TrimSpace(string(b))
		c.Current = current
		n, err := strconv.ParseInt(current, 10, 64)
		if err != nil {
			c.Error = "non-numeric current value: " + current
		} else {
			c.Ok = n >= t.Target
		}
		out = append(out, c)
	}
	return out
}

