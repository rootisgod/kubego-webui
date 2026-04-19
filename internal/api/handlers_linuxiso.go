package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

type createLinuxISOVMRequest struct {
	Name            string `json:"name"`
	InstallerISOPVC string `json:"installer_iso"` // PVC name from the upload flow
	CPUs            int    `json:"cpus"`
	MemoryMB        int    `json:"memory_mb"`
	DiskGB          int    `json:"disk_gb"`
	UEFI            bool   `json:"uefi"`
}

// handleCreateLinuxISOVM kicks off a Linux install from an uploaded ISO.
// The install itself runs inside the guest — the user drives it via the
// Graphics tab (VNC). Returns 202 Accepted immediately.
func (s *Server) handleCreateLinuxISOVM(w http.ResponseWriter, r *http.Request) {
	var req createLinuxISOVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		req.Name = kubevirt.RandomVMName()
	}
	if err := kubevirt.ValidateVMName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.InstallerISOPVC == "" {
		writeError(w, http.StatusBadRequest, "installer_iso is required — upload a Linux ISO via /images/uploads first")
		return
	}

	kvReq := kubevirt.LinuxISOLaunchRequest{
		Name:            req.Name,
		InstallerISOPVC: req.InstallerISOPVC,
		CPUs:            req.CPUs,
		MemoryMB:        req.MemoryMB,
		DiskGB:          req.DiskGB,
		UEFI:            req.UEFI,
	}

	s.launches.start(req.Name)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.launches.fail(req.Name, fmt.Sprintf("panic: %v", rec))
			}
		}()
		if _, err := s.kv().LaunchLinuxISOVM(kvReq); err != nil {
			s.logger.Error("linux ISO VM launch failed", "name", req.Name, "err", err)
			s.launches.fail(req.Name, err.Error())
			s.eventLog.EmitEvent("vm", "create_linux_iso", "user", req.Name, "failed", err.Error())
			return
		}
		s.launches.complete(req.Name)
		s.eventLog.EmitEvent("vm", "create_linux_iso", "user", req.Name, "success", "")
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"name": req.Name, "message": "Linux ISO VM launch started"})
}
