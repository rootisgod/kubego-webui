package api

import (
	"io"
	"net/http"
	"strconv"
)

func (s *Server) handleListVMEvents(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	events, err := s.kv.VMEvents(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// handleGetVMPodLogs returns the virt-launcher compute container's logs
// as text/plain. Query params:
//
//	tail (int, default 500)  — last N lines to return; 0 = all
//
// Streaming / follow mode is not wired yet — the UI polls this on a
// timer. Switching to SSE would be the natural next step.
func (s *Server) handleGetVMPodLogs(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	tail := int64(500)
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			tail = n
		}
	}

	stream, err := s.kv.VMPodLogs(r.Context(), name, tail, false)
	if err != nil {
		// 404 is the right signal here — the UI shows "no pod yet"
		// rather than a red error banner during VM boot.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, stream)
}
