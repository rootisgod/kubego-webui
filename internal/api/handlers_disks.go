package api

import (
	"encoding/json"
	"net/http"
)

// Disks replace PassGo's Mounts. Multipass bind-mounted a host directory
// into a VM; KubeVirt has no such primitive and instead exposes hot-plug
// PVCs as virtual disks. M4 implements attach/detach against the
// VirtualMachineInstance hotplugVolumes API — until then these return
// 501 via the driver's ErrNotImplemented.

func (s *Server) handleListDisks(w http.ResponseWriter, r *http.Request) {
	name, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	disks, err := s.kv().ListDisks(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, disks)
}

type attachDiskRequest struct {
	// Source is the PVC name (or, in v2, a StorageClass-qualified reference)
	// to attach. Kept loosely shaped so M4 can reuse the field for a
	// volumeName + claimName tuple.
	Source string `json:"source"`
	// Target is the in-guest disk name (e.g. "data1"). Corresponds to
	// spec.template.spec.domain.devices.disks[].name.
	Target string `json:"target"`
}

func (s *Server) handleAttachDisk(w http.ResponseWriter, r *http.Request) {
	vmName, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	var req attachDiskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Source == "" || req.Target == "" {
		writeError(w, http.StatusBadRequest, "source and target are required")
		return
	}
	if err := s.kv().AttachDisk(vmName, req.Source, req.Target); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeMessage(w, "disk attached")
}

type detachDiskRequest struct {
	Target string `json:"target"`
}

func (s *Server) handleDetachDisk(w http.ResponseWriter, r *http.Request) {
	vmName, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	var req detachDiskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if err := s.kv().DetachDisk(vmName, req.Target); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeMessage(w, "disk detached")
}
