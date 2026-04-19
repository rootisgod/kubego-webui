package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type createPortForwardRequest struct {
	RemotePort int    `json:"remote_port"`
	Protocol   string `json:"protocol"` // hint: "ssh"|"http"|"tcp"
	Label      string `json:"label"`
}

func (s *Server) handleListPortForwards(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.portForwards.ListForVM(vm))
}

func (s *Server) handleCreatePortForward(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	var req createPortForwardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RemotePort <= 0 || req.RemotePort > 65535 {
		writeError(w, http.StatusBadRequest, "remote_port must be between 1 and 65535")
		return
	}

	// StartPortForward waits on the apiserver + the spdy handshake —
	// 15s is long enough for a cold cluster and short enough to not tie
	// up a connection forever if the VM has no running pod.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	entry, err := s.portForwards.EnsureOpen(ctx, vm, req.RemotePort, req.Protocol, req.Label)
	if err != nil {
		s.eventLog.EmitHTTPEvent(r, "vm", "port_forward_open", vm, "failed", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "vm", "port_forward_open", vm, "success", "remote_port="+strconv.Itoa(req.RemotePort))
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) handleDeletePortForward(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.portForwards.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "vm", "port_forward_close", vm, "success", "id="+id)
	writeMessage(w, "port-forward closed")
}

