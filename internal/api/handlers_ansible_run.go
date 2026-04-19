package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

type ansibleRunRequest struct {
	Playbook string   `json:"playbook"`
	VMs      []string `json:"vms"`
	Groups   []string `json:"groups,omitempty"`
}

type ansibleStatusResponse struct {
	Installed    bool   `json:"installed"`
	Version      string `json:"version,omitempty"`
	Error        string `json:"error,omitempty"`
	PlaybooksDir string `json:"playbooks_dir"`
	SSHKeyPath   string `json:"ssh_key_path,omitempty"`
}

type ansibleOutputEvent struct {
	Type     string `json:"type"`
	Content  string `json:"content,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

type ansibleRunStatusResponse struct {
	Active   bool     `json:"active"`
	Playbook string   `json:"playbook,omitempty"`
	VMs      []string `json:"vms,omitempty"`
	Status   string   `json:"status,omitempty"`
	ExitCode int      `json:"exit_code,omitempty"`
}

func (s *Server) handleAnsibleStatus(w http.ResponseWriter, r *http.Request) {
	path, err := exec.LookPath("ansible-playbook")
	if err != nil {
		writeJSON(w, http.StatusOK, ansibleStatusResponse{
			Installed:    false,
			Error:        "ansible-playbook not found in PATH",
			PlaybooksDir: s.cfg.PlaybooksDir,
		})
		return
	}

	out, err := exec.Command(path, "--version").Output()
	version := ""
	if err == nil {
		lines := strings.SplitN(string(out), "\n", 2)
		if len(lines) > 0 {
			version = strings.TrimSpace(lines[0])
		}
	}

	// Resolve effective SSH key path
	sshKeyPath := ""
	if s.cfg.VMDefaults != nil {
		sshKeyPath = s.cfg.VMDefaults.SSHPrivateKey
	}
	if sshKeyPath == "" {
		sshKeyPath = ""
	}

	writeJSON(w, http.StatusOK, ansibleStatusResponse{
		Installed:    true,
		Version:      version,
		PlaybooksDir: s.cfg.PlaybooksDir,
		SSHKeyPath:   sshKeyPath,
	})
}

// handleRunPlaybook starts a playbook run. The process runs in the background
// and survives client disconnects. Use GET /ansible/run/output to stream output.
func (s *Server) handleRunPlaybook(w http.ResponseWriter, r *http.Request) {
	// Check if already running
	if current := s.ansibleRunner.getCurrent(); current != nil {
		current.mu.Lock()
		status := current.Status
		current.mu.Unlock()
		if status == "running" {
			writeError(w, http.StatusConflict, "a playbook is already running")
			return
		}
	}

	var req ansibleRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Playbook == "" {
		writeError(w, http.StatusBadRequest, "playbook is required")
		return
	}
	if len(req.VMs) == 0 && len(req.Groups) == 0 {
		writeError(w, http.StatusBadRequest, "at least one VM or group must be selected")
		return
	}

	ansiblePath, err := exec.LookPath("ansible-playbook")
	if err != nil {
		writeError(w, http.StatusBadRequest, "ansible-playbook not found in PATH")
		return
	}

	playbookPath, playbookCleanup, err := s.resolvePlaybookPath(req.Playbook)
	if err != nil {
		writeError(w, http.StatusNotFound, "playbook not found")
		return
	}

	// Resolve target VMs
	targetVMs := make([]string, len(req.VMs))
	copy(targetVMs, req.VMs)

	if len(req.Groups) > 0 {
		s.cfgMu.Lock()
		for vm, group := range s.cfg.VMGroups {
			for _, g := range req.Groups {
				if group == g {
					found := false
					for _, t := range targetVMs {
						if t == vm {
							found = true
							break
						}
					}
					if !found {
						targetVMs = append(targetVMs, vm)
					}
				}
			}
		}
		s.cfgMu.Unlock()
	}

	// Start one kubectl port-forward per target VM so ansible-playbook
	// can reach them on 127.0.0.1:<random> even when the VM's pod-CIDR
	// IP isn't routable from this host (the default on KinD).
	pfCtx, pfCancel := context.WithTimeout(r.Context(), 15*time.Second)
	portForwards, pfCleanup, err := s.startVMPortForwards(pfCtx, targetVMs)
	pfCancel()
	if err != nil {
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		writeError(w, http.StatusInternalServerError, "failed to start port-forwards: "+err.Error())
		return
	}

	// Generate inventory
	user := "ubuntu"
	sshKey := ""
	if s.cfg.VMDefaults != nil {
		sshKey = s.cfg.VMDefaults.SSHPrivateKey
	}
	inventory, err := s.generateInventoryYAML(targetVMs, user, sshKey, portForwards)
	if err != nil {
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		writeError(w, http.StatusInternalServerError, "failed to generate inventory")
		return
	}

	tmpFile, err := os.CreateTemp("", "kubego-ansible-inventory-*.yml")
	if err != nil {
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		writeError(w, http.StatusInternalServerError, "failed to create temp inventory")
		return
	}
	if _, err := tmpFile.WriteString(inventory); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		writeError(w, http.StatusInternalServerError, "failed to write inventory")
		return
	}
	tmpFile.Close()

	cmd := exec.Command(ansiblePath, "-i", tmpFile.Name(), playbookPath)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_FORCE_COLOR=true",
		"ANSIBLE_HOST_KEY_CHECKING=False",
	)

	invPath := tmpFile.Name()
	s.ansibleRunner.start(req.Playbook, targetVMs, cmd, func() {
		pfCleanup()
		os.Remove(invPath)
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
	})

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "playbook": req.Playbook})
}

// handleAnsibleRunOutput streams output from the current run via SSE.
// Replays buffered output first, then streams new lines as they arrive.
func (s *Server) handleAnsibleRunOutput(w http.ResponseWriter, r *http.Request) {
	run := s.ansibleRunner.getCurrent()
	if run == nil {
		writeError(w, http.StatusNotFound, "no ansible run")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Subscribe and get buffered output
	buffered, ch := run.subscribe()

	// Replay buffered output
	for _, line := range buffered {
		sendSSEEvent(w, flusher, ansibleOutputEvent{Type: "output", Content: line})
	}

	// If already finished, send done and return
	if ch == nil {
		run.mu.Lock()
		exitCode := run.ExitCode
		run.mu.Unlock()
		sendSSEEvent(w, flusher, ansibleOutputEvent{Type: "done", ExitCode: exitCode})
		return
	}

	// Stream new output until run completes or client disconnects
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			run.unsubscribe(ch)
			return
		case line, ok := <-ch:
			if !ok {
				// Channel closed = run finished
				run.mu.Lock()
				exitCode := run.ExitCode
				run.mu.Unlock()
				sendSSEEvent(w, flusher, ansibleOutputEvent{Type: "done", ExitCode: exitCode})
				return
			}
			sendSSEEvent(w, flusher, ansibleOutputEvent{Type: "output", Content: line})
		}
	}
}

// handleAnsibleRunStatus returns the current run state without streaming.
func (s *Server) handleAnsibleRunStatus(w http.ResponseWriter, r *http.Request) {
	run := s.ansibleRunner.getCurrent()
	if run == nil {
		writeJSON(w, http.StatusOK, ansibleRunStatusResponse{Active: false})
		return
	}
	run.mu.Lock()
	resp := ansibleRunStatusResponse{
		Active:   true,
		Playbook: run.Playbook,
		VMs:      run.VMs,
		Status:   run.Status,
		ExitCode: run.ExitCode,
	}
	run.mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// handleCancelAnsibleRun cancels the current running playbook.
func (s *Server) handleCancelAnsibleRun(w http.ResponseWriter, r *http.Request) {
	if s.ansibleRunner.cancel() {
		writeMessage(w, "run cancelled")
	} else {
		writeError(w, http.StatusNotFound, "no running playbook to cancel")
	}
}

// handleClearAnsibleRun clears the completed run so a new one can start.
func (s *Server) handleClearAnsibleRun(w http.ResponseWriter, r *http.Request) {
	s.ansibleRunner.clear()
	writeMessage(w, "run cleared")
}

// handleAnsibleRunQueue returns the current queue of pending ansible runs.
func (s *Server) handleAnsibleRunQueue(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ansibleRunner.getQueue())
}

// handleClearAnsibleRunQueue removes all pending queue entries.
func (s *Server) handleClearAnsibleRunQueue(w http.ResponseWriter, r *http.Request) {
	s.ansibleRunner.clearQueue()
	writeMessage(w, "queue cleared")
}

// startPlaybookRun builds the inventory and starts an ansible-playbook execution.
// Used by both handleRunPlaybook and the queue's startFunc.
func (s *Server) startPlaybookRun(playbook string, vms []string) {
	ansiblePath, err := exec.LookPath("ansible-playbook")
	if err != nil {
		s.logger.Error("ansible-playbook not found for queued run", "playbook", playbook)
		return
	}

	playbookPath, playbookCleanup, err := s.resolvePlaybookPath(playbook)
	if err != nil {
		s.logger.Error("playbook not found for queued run", "playbook", playbook, "err", err)
		return
	}

	pfCtx, pfCancel := context.WithTimeout(context.Background(), 15*time.Second)
	portForwards, pfCleanup, err := s.startVMPortForwards(pfCtx, vms)
	pfCancel()
	if err != nil {
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		s.logger.Error("failed to start port-forwards for queued run", "err", err)
		return
	}

	user := "ubuntu"
	sshKey := ""
	if s.cfg.VMDefaults != nil {
		sshKey = s.cfg.VMDefaults.SSHPrivateKey
	}
	inventory, err := s.generateInventoryYAML(vms, user, sshKey, portForwards)
	if err != nil {
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		s.logger.Error("failed to generate inventory for queued run", "err", err)
		return
	}

	tmpFile, err := os.CreateTemp("", "kubego-ansible-inventory-*.yml")
	if err != nil {
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		s.logger.Error("failed to create temp inventory for queued run", "err", err)
		return
	}
	if _, err := tmpFile.WriteString(inventory); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		pfCleanup()
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
		s.logger.Error("failed to write inventory for queued run", "err", err)
		return
	}
	tmpFile.Close()

	cmd := exec.Command(ansiblePath, "-i", tmpFile.Name(), playbookPath)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_FORCE_COLOR=true",
		"ANSIBLE_HOST_KEY_CHECKING=False",
	)

	invPath := tmpFile.Name()
	s.ansibleRunner.start(playbook, vms, cmd, func() {
		pfCleanup()
		os.Remove(invPath)
		if playbookCleanup != "" {
			os.Remove(playbookCleanup)
		}
	})
}

// resolvePlaybookPath returns a filesystem path that ansible-playbook can
// invoke. For built-in playbooks it writes the embedded content to a
// temp file; the returned `cleanupPath` (possibly empty) should be added
// to ansibleRunner.start's cleanup list so the temp file is removed
// after the run finishes. For user playbooks cleanupPath is "".
func (s *Server) resolvePlaybookPath(name string) (path string, cleanupPath string, err error) {
	if data, err := s.builtinPlaybooksFS.ReadFile("playbooks/" + name); err == nil {
		tmp, terr := os.CreateTemp("", "kubego-builtin-playbook-*.yml")
		if terr != nil {
			return "", "", terr
		}
		if _, werr := tmp.Write(data); werr != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", "", werr
		}
		tmp.Close()
		return tmp.Name(), tmp.Name(), nil
	}
	if _, err := kubevirt.ReadPlaybook(s.cfg.PlaybooksDir, name); err != nil {
		return "", "", err
	}
	return filepath.Join(s.cfg.PlaybooksDir, name), "", nil
}

func sendSSEEvent(w http.ResponseWriter, flusher http.Flusher, event ansibleOutputEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}
