package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

type createImageUploadRequest struct {
	Name   string `json:"name"`    // user-facing display name
	Kind   string `json:"kind"`    // "iso" | "disk"
	SizeGB int    `json:"size_gb"` // DV size; must comfortably fit the payload
}

type createImageImportRequest struct {
	Name   string `json:"name"`    // user-facing display name
	Kind   string `json:"kind"`    // "iso" | "disk"
	SizeGB int    `json:"size_gb"` // DV size; must comfortably fit the fetched payload
	URL    string `json:"url"`     // http(s) source — CDI's importer pod pulls it
}

func (s *Server) handleListImageUploads(w http.ResponseWriter, r *http.Request) {
	out, err := s.kv().ListImageUploads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateImageUpload(w http.ResponseWriter, r *http.Request) {
	var req createImageUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Kind == "" {
		req.Kind = "iso"
	}
	if req.Kind != "iso" && req.Kind != "disk" {
		writeError(w, http.StatusBadRequest, `kind must be "iso" or "disk"`)
		return
	}
	if req.SizeGB < 1 || req.SizeGB > 1024 {
		writeError(w, http.StatusBadRequest, "size_gb must be 1..1024")
		return
	}

	pvcName := kubevirt.ImageUploadPVCName(req.Name)
	if pvcName == "img-" {
		writeError(w, http.StatusBadRequest, "name must contain letters or digits")
		return
	}
	if err := s.kv().CreateImageUpload(pvcName, req.Name, req.Kind, req.SizeGB); err != nil {
		s.eventLog.EmitHTTPEvent(r, "image", "create", pvcName, "failed", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "image", "create", pvcName, "success", req.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"pvc_name": pvcName, "name": req.Name})
}

// handleUploadImageData streams the request body straight into CDI's
// upload proxy. Browsers typically send as `application/octet-stream` so
// the existing body-size middleware lets us through untouched. No
// multipart parse, no buffering — KubeGo acts as a thin TLS-terminating
// tunnel.
func (s *Server) handleUploadImageData(w http.ResponseWriter, r *http.Request) {
	pvcName := r.PathValue("name")
	if pvcName == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	defer r.Body.Close()

	if err := s.kv().UploadImageBytes(r.Context(), pvcName, r.Body, r.ContentLength); err != nil {
		s.eventLog.EmitHTTPEvent(r, "image", "upload", pvcName, "failed", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "image", "upload", pvcName, "success", fmt.Sprintf("bytes=%d", r.ContentLength))
	writeMessage(w, "upload complete")
}

// handleCreateImageImport kicks off a CDI http-source import. Returns
// immediately — the DataVolume's phase advances asynchronously and the
// existing list endpoint surfaces ImportInProgress / Succeeded / Failed.
func (s *Server) handleCreateImageImport(w http.ResponseWriter, r *http.Request) {
	var req createImageImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Kind == "" {
		req.Kind = "iso"
	}
	if req.Kind != "iso" && req.Kind != "disk" {
		writeError(w, http.StatusBadRequest, `kind must be "iso" or "disk"`)
		return
	}
	if req.SizeGB < 1 || req.SizeGB > 1024 {
		writeError(w, http.StatusBadRequest, "size_gb must be 1..1024")
		return
	}

	pvcName := kubevirt.ImageUploadPVCName(req.Name)
	if pvcName == "img-" {
		writeError(w, http.StatusBadRequest, "name must contain letters or digits")
		return
	}
	if err := s.kv().CreateImageImport(pvcName, req.Name, req.Kind, req.SizeGB, req.URL); err != nil {
		s.eventLog.EmitHTTPEvent(r, "image", "import", pvcName, "failed", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "image", "import", pvcName, "success", req.URL)
	writeJSON(w, http.StatusCreated, map[string]any{"pvc_name": pvcName, "name": req.Name})
}

func (s *Server) handleDeleteImageUpload(w http.ResponseWriter, r *http.Request) {
	pvcName := r.PathValue("name")
	if pvcName == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.kv().DeleteImageUpload(pvcName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "image", "delete", pvcName, "success", "")
	writeMessage(w, "image deleted")
}
