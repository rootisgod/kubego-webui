package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type applyManifestRequest struct {
	Manifest string `json:"manifest"`
}

func (s *Server) handleApplyManifest(w http.ResponseWriter, r *http.Request) {
	var req applyManifestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	applied, err := s.kv().ApplyManifest(ctx, req.Manifest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": applied})
}
