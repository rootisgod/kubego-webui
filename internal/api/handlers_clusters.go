package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// kindNameRe restricts `kind create cluster --name <N>` input to the
// subset kind itself accepts. Tightened to lowercase-alphanum+dash so
// the resulting `kind-<N>` context name is predictable and safe to
// splice into the KUBECONFIG path.
var kindNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	ctxs, err := s.clusters.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contexts":       ctxs,
		"active":         s.clusters.ActiveContext(),
		"in_cluster":     s.clusters.InCluster(),
		"kind_available": kindAvailable(),
	})
}

type selectClusterRequest struct {
	Context string `json:"context"`
}

func (s *Server) handleSelectCluster(w http.ResponseWriter, r *http.Request) {
	var req selectClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Context = strings.TrimSpace(req.Context)
	if req.Context == "" {
		writeError(w, http.StatusBadRequest, "context is required")
		return
	}
	if err := s.clusters.Select(req.Context); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Probe the newly-active cluster — surfaces KubeVirt install status in
	// the server log; not fatal.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.clusters.Active().ProbeKubeVirt(probeCtx)
	cancel()
	s.logger.Info("cluster context switched", "context", s.clusters.ActiveContext())
	writeJSON(w, http.StatusOK, map[string]any{"active": s.clusters.ActiveContext()})
}

// --- kind create/delete (SSE) ---
//
// The streaming lifetime is the subprocess lifetime. We deliberately do
// not buffer or detach: if the client disconnects mid-create, the
// command is cancelled via r.Context(). This matches how the ansible
// runner would behave if it weren't long-lived — kind create finishes
// in ~60s so resuming is not needed.

type clusterSSEEvent struct {
	Type    string `json:"type"` // "output" | "done" | "error"
	Line    string `json:"line,omitempty"`
	Context string `json:"context,omitempty"`
	Error   string `json:"error,omitempty"`
}

type kindCreateRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleKindCreate(w http.ResponseWriter, r *http.Request) {
	if s.clusters.InCluster() {
		writeError(w, http.StatusPreconditionFailed, "kind create is disabled when running in-cluster")
		return
	}
	if !kindAvailable() {
		writeError(w, http.StatusPreconditionFailed, "kind CLI not found on PATH")
		return
	}
	var req kindCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !kindNameRe.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "name must be lowercase alphanumeric with dashes (2-32 chars)")
		return
	}

	args := []string{"create", "cluster", "--name", req.Name}
	if kcfg := s.clusters.KubeconfigPath(); kcfg != "" {
		args = append(args, "--kubeconfig", kcfg)
	}

	runKindSSE(w, r, "kind", args, func() string {
		contextName := "kind-" + req.Name
		if err := s.clusters.Select(contextName); err != nil {
			return ""
		}
		// Probe the new cluster so the log records KubeVirt install state.
		probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.clusters.Active().ProbeKubeVirt(probeCtx)
		cancel()
		s.logger.Info("kind cluster created and activated", "context", contextName)
		return contextName
	})
}

func (s *Server) handleKindDelete(w http.ResponseWriter, r *http.Request) {
	if s.clusters.InCluster() {
		writeError(w, http.StatusPreconditionFailed, "kind delete is disabled when running in-cluster")
		return
	}
	if !kindAvailable() {
		writeError(w, http.StatusPreconditionFailed, "kind CLI not found on PATH")
		return
	}
	name := r.PathValue("name")
	if !kindNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid kind cluster name")
		return
	}

	args := []string{"delete", "cluster", "--name", name}
	if kcfg := s.clusters.KubeconfigPath(); kcfg != "" {
		args = append(args, "--kubeconfig", kcfg)
	}

	runKindSSE(w, r, "kind", args, func() string {
		contextName := "kind-" + name
		s.clusters.Invalidate(contextName)
		// If the deleted context was active, pick any remaining context.
		if s.clusters.ActiveContext() == contextName {
			ctxs, _ := s.clusters.List()
			for _, c := range ctxs {
				if c.Name != contextName {
					_ = s.clusters.Select(c.Name)
					break
				}
			}
		}
		s.logger.Info("kind cluster deleted", "context", contextName, "active_now", s.clusters.ActiveContext())
		return s.clusters.ActiveContext()
	})
}

// runKindSSE runs a kind subcommand, streaming merged stdout+stderr to
// the SSE response one line at a time. onSuccess runs only when the
// command exits 0; the string it returns is forwarded in the final
// `done` event as the new active context name.
func runKindSSE(w http.ResponseWriter, r *http.Request, name string, args []string, onSuccess func() string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	cmd := exec.CommandContext(r.Context(), name, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	streamLines := func(rc io.Reader, done chan<- struct{}) {
		defer func() { done <- struct{}{} }()
		br := bufio.NewReader(rc)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				writeClusterSSE(w, flusher, clusterSSEEvent{
					Type: "output",
					Line: strings.TrimRight(line, "\r\n"),
				})
			}
			if err != nil {
				return
			}
		}
	}

	done := make(chan struct{}, 2)
	go streamLines(stdout, done)
	go streamLines(stderr, done)
	<-done
	<-done

	if err := cmd.Wait(); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	contextName := ""
	if onSuccess != nil {
		contextName = onSuccess()
	}
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done", Context: contextName})
}

func writeClusterSSE(w http.ResponseWriter, flusher http.Flusher, event clusterSSEEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func kindAvailable() bool {
	_, err := exec.LookPath("kind")
	return err == nil
}
