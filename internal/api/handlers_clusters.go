package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

const (
	kubeVirtStableURL = "https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt"
	cdiReleasesURL    = "https://api.github.com/repos/kubevirt/containerized-data-importer/releases/latest"
)

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
// command is cancelled via r.Context(). Create bundles KubeVirt+CDI
// install after `kind create` so the resulting cluster lands ready for
// VM workloads instead of showing the KubeVirt-absent banner.

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
	if !kubectlAvailable() {
		writeError(w, http.StatusPreconditionFailed, "kubectl CLI not found on PATH (required to install KubeVirt)")
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	setSSEHeaders(w, flusher)

	// Step 1: `kind create cluster`
	kindArgs := []string{"create", "cluster", "--name", req.Name}
	if kcfg := s.clusters.KubeconfigPath(); kcfg != "" {
		kindArgs = append(kindArgs, "--kubeconfig", kcfg)
	}
	if err := streamCommand(r.Context(), w, flusher, "kind", kindArgs...); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	contextName := "kind-" + req.Name
	if err := s.clusters.Select(contextName); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: "activate new context: " + err.Error()})
		return
	}
	s.logger.Info("kind cluster created, starting KubeVirt install", "context", contextName)

	// Step 2..N: install KubeVirt + CDI into the new context.
	if err := s.installKubeVirtIntoKind(r.Context(), w, flusher, contextName); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	// Final probe so the server log reflects the newly-installed state.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.clusters.Active().ProbeKubeVirt(probeCtx)
	cancel()

	s.logger.Info("kind cluster ready", "context", contextName)
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done", Context: contextName})
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	setSSEHeaders(w, flusher)

	args := []string{"delete", "cluster", "--name", name}
	if kcfg := s.clusters.KubeconfigPath(); kcfg != "" {
		args = append(args, "--kubeconfig", kcfg)
	}
	if err := streamCommand(r.Context(), w, flusher, "kind", args...); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

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
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done", Context: s.clusters.ActiveContext()})
}

// installKubeVirtIntoKind runs the post-create KubeVirt + CDI install
// chain against kctx. Mirrors scripts/kind-up.sh: operator + CR for
// KubeVirt, always-on useEmulation (UI-created KinD nodes never mount
// /dev/kvm), operator + CR for CDI, then waits for both to reach
// Available. Streams every subcommand's output as SSE output events.
func (s *Server) installKubeVirtIntoKind(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, kctx string) error {
	streamPhase(w, flusher, "Resolving KubeVirt + CDI release versions")
	kvVer, err := resolveKubeVirtVersion(ctx)
	if err != nil {
		return fmt.Errorf("resolve kubevirt version: %w", err)
	}
	streamLine(w, flusher, "  KubeVirt "+kvVer)
	cdiVer, err := resolveCDIVersion(ctx)
	if err != nil {
		return fmt.Errorf("resolve cdi version: %w", err)
	}
	streamLine(w, flusher, "  CDI "+cdiVer)

	kcfg := s.clusters.KubeconfigPath()
	steps := []struct {
		label string
		args  []string
	}{
		{
			"Installing KubeVirt operator",
			[]string{"apply", "-f", fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-operator.yaml", kvVer)},
		},
		{
			"Installing KubeVirt custom resource",
			[]string{"apply", "-f", fmt.Sprintf("https://github.com/kubevirt/kubevirt/releases/download/%s/kubevirt-cr.yaml", kvVer)},
		},
		{
			"Enabling software emulation (UI-created KinD nodes have no /dev/kvm)",
			[]string{"-n", "kubevirt", "patch", "kubevirt", "kubevirt", "--type=merge",
				"--patch", `{"spec":{"configuration":{"developerConfiguration":{"useEmulation":true}}}}`},
		},
		{
			"Installing CDI operator",
			[]string{"apply", "-f", fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-operator.yaml", cdiVer)},
		},
		{
			"Installing CDI custom resource",
			[]string{"apply", "-f", fmt.Sprintf("https://github.com/kubevirt/containerized-data-importer/releases/download/%s/cdi-cr.yaml", cdiVer)},
		},
		{
			"Waiting for KubeVirt to become Available (can take several minutes on first install)",
			[]string{"-n", "kubevirt", "wait", "--for=condition=Available", "--timeout=10m", "kv/kubevirt"},
		},
		{
			"Waiting for CDI to become Available",
			[]string{"-n", "cdi", "wait", "--for=condition=Available", "--timeout=10m", "cdi/cdi"},
		},
	}

	for _, step := range steps {
		streamPhase(w, flusher, step.label)
		kargs := []string{"--context", kctx}
		if kcfg != "" {
			kargs = append(kargs, "--kubeconfig", kcfg)
		}
		kargs = append(kargs, step.args...)
		if err := streamCommand(ctx, w, flusher, "kubectl", kargs...); err != nil {
			return fmt.Errorf("%s: %w", step.label, err)
		}
	}
	return nil
}

// resolveKubeVirtVersion honours $KUBEVIRT_VERSION for parity with
// scripts/kind-up.sh, else fetches the prow-published stable tag.
func resolveKubeVirtVersion(ctx context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("KUBEVIRT_VERSION")); v != "" {
		return v, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kubeVirtStableURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", kubeVirtStableURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("empty kubevirt stable.txt")
	}
	return v, nil
}

// resolveCDIVersion honours $CDI_VERSION, else reads latest GitHub release.
func resolveCDIVersion(ctx context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("CDI_VERSION")); v != "" {
		return v, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdiReleasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", cdiReleasesURL, resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", fmt.Errorf("cdi release response missing tag_name")
	}
	return body.TagName, nil
}

// streamCommand runs name+args, forwarding merged stdout+stderr to the
// SSE response one line at a time. Returns an error on non-zero exit
// (or subprocess start failure) — the caller is responsible for
// emitting the `error` event.
func streamCommand(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
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

	return cmd.Wait()
}

func setSSEHeaders(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()
}

// streamPhase emits a visually-separated phase marker line. Kept as an
// `output` event so the existing frontend renders it inline without
// needing a new event type.
func streamPhase(w http.ResponseWriter, flusher http.Flusher, label string) {
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "output", Line: ""})
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "output", Line: "==> " + label})
}

func streamLine(w http.ResponseWriter, flusher http.Flusher, line string) {
	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "output", Line: line})
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

func kubectlAvailable() bool {
	_, err := exec.LookPath("kubectl")
	return err == nil
}
