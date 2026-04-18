package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/rootisgod/kubego-webui/internal/config"
	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.kv().ListNetworks()
	if err != nil {
		// Networks can fail on some platforms; return empty list rather than error
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, networks)
}

func (s *Server) handleListCloudInitTemplates(w http.ResponseWriter, r *http.Request) {
	var dirs []string
	if s.cfg.CloudInitDir != "" {
		dirs = append(dirs, s.cfg.CloudInitDir)
	}
	templates, err := s.kv().GetAllCloudInitTemplates(dirs)
	if err != nil {
		templates = nil
	}

	// Add built-in templates from embedded FS
	entries, _ := s.builtinTemplatesFS.ReadDir("cloud-init")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		templates = append(templates, kubevirt.TemplateOption{
			Label:   entry.Name(),
			Path:    "builtin:" + entry.Name(),
			BuiltIn: true,
		})
	}

	if templates == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleFindImages(w http.ResponseWriter, r *http.Request) {
	images, err := s.kv().FindImages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, images)
}

type versionResponse struct {
	Version    string `json:"version"`
	BuildTime  string `json:"build_time"`
	GitCommit  string `json:"git_commit"`
	Hostname   string `json:"hostname"`
	ServerTime string `json:"server_time"`
	Timezone   string `json:"timezone"`
}

func (s *Server) handleClusterResources(w http.ResponseWriter, r *http.Request) {
	res, err := s.kv().ClusterResources()
	if err != nil {
		s.logger.Warn("cluster resources", "error", err)
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.kv().ClusterInfo()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleGetVMDefaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.VMDefaults)
}

func (s *Server) handleUpdateVMDefaults(w http.ResponseWriter, r *http.Request) {
	var req config.VMDefaults
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.CPUs < 1 {
		req.CPUs = 1
	}
	if req.MemoryMB < 512 {
		req.MemoryMB = 512
	}
	if req.DiskGB < 1 {
		req.DiskGB = 1
	}
	s.cfg.VMDefaults = &req
	if err := s.cfg.Save(s.configPath); err != nil {
		s.logger.Error("failed to save config", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to save configuration")
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.VMDefaults)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	now := time.Now()
	zone, _ := now.Zone()
	writeJSON(w, http.StatusOK, versionResponse{
		Version:    s.version,
		BuildTime:  s.buildTime,
		GitCommit:  s.gitCommit,
		Hostname:   hostname,
		ServerTime: now.Format(time.RFC3339),
		Timezone:   zone,
	})
}
