package api

import (
	"encoding/json"
	"net/http"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

type playbookEntry struct {
	Name    string `json:"name"`
	BuiltIn bool   `json:"builtIn,omitempty"`
}

type playbookResponse struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	BuiltIn bool   `json:"builtIn,omitempty"`
}

type playbookRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (s *Server) handleListPlaybooks(w http.ResponseWriter, r *http.Request) {
	names, err := kubevirt.ListPlaybooks(s.cfg.PlaybooksDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list playbooks")
		return
	}
	entries := make([]playbookEntry, 0, len(names))
	for _, n := range names {
		entries = append(entries, playbookEntry{Name: n})
	}
	// Append built-in playbooks from embedded FS.
	if builtins, err := s.builtinPlaybooksFS.ReadDir("playbooks"); err == nil {
		for _, e := range builtins {
			if e.IsDir() {
				continue
			}
			entries = append(entries, playbookEntry{Name: e.Name(), BuiltIn: true})
		}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleGetPlaybook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Check built-in playbooks first (mirrors cloud-init template lookup).
	if data, err := s.builtinPlaybooksFS.ReadFile("playbooks/" + name); err == nil {
		writeJSON(w, http.StatusOK, playbookResponse{Name: name, Content: string(data), BuiltIn: true})
		return
	}
	content, err := kubevirt.ReadPlaybook(s.cfg.PlaybooksDir, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "playbook not found")
		return
	}
	writeJSON(w, http.StatusOK, playbookResponse{Name: name, Content: content})
}

func (s *Server) handleCreatePlaybook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req playbookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "name and content are required")
		return
	}
	if _, err := kubevirt.ReadPlaybook(s.cfg.PlaybooksDir, req.Name); err == nil {
		writeError(w, http.StatusConflict, "playbook already exists")
		return
	}
	if err := kubevirt.WritePlaybook(s.cfg.PlaybooksDir, req.Name, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, playbookResponse{Name: req.Name, Content: req.Content})
}

func (s *Server) handleUpdatePlaybook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if _, err := kubevirt.ReadPlaybook(s.cfg.PlaybooksDir, name); err != nil {
		writeError(w, http.StatusNotFound, "playbook not found")
		return
	}
	if err := kubevirt.WritePlaybook(s.cfg.PlaybooksDir, name, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playbookResponse{Name: name, Content: req.Content})
}

func (s *Server) handleDeletePlaybook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := kubevirt.DeletePlaybook(s.cfg.PlaybooksDir, name); err != nil {
		writeError(w, http.StatusNotFound, "playbook not found")
		return
	}
	writeMessage(w, "playbook deleted")
}
