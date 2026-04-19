package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

type createWindowsVMRequest struct {
	Name               string `json:"name"`
	InstallerISOPVC    string `json:"installer_iso"`     // PVC name from the upload flow
	VirtioWinISOPVC    string `json:"virtio_win_iso"`    // optional; strongly recommended
	Hostname           string `json:"hostname"`
	AdminPassword      string `json:"admin_password"`
	EnableRDP          bool   `json:"enable_rdp"`
	CPUs               int    `json:"cpus"`
	MemoryMB           int    `json:"memory_mb"`
	DiskGB             int    `json:"disk_gb"`
	SecureBoot         bool   `json:"secure_boot"`
	TPM                bool   `json:"tpm"`
}

// handleCreateWindowsVM kicks off an unattended Windows install. Returns
// 202 Accepted immediately — the install itself runs inside the guest
// over the next several minutes, observable via the Graphics tab.
func (s *Server) handleCreateWindowsVM(w http.ResponseWriter, r *http.Request) {
	var req createWindowsVMRequest
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
		writeError(w, http.StatusBadRequest, "installer_iso is required — upload a Windows ISO via /images/uploads first")
		return
	}
	if req.AdminPassword == "" {
		writeError(w, http.StatusBadRequest, "admin_password is required")
		return
	}

	kvReq := kubevirt.WindowsLaunchRequest{
		Name:            req.Name,
		InstallerISOPVC: req.InstallerISOPVC,
		VirtioWinISOPVC: req.VirtioWinISOPVC,
		Hostname:        req.Hostname,
		AdminPassword:   req.AdminPassword,
		EnableRDP:       req.EnableRDP,
		CPUs:            req.CPUs,
		MemoryMB:        req.MemoryMB,
		DiskGB:          req.DiskGB,
		SecureBoot:      req.SecureBoot,
		TPM:             req.TPM,
	}

	s.launches.start(req.Name)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.launches.fail(req.Name, fmt.Sprintf("panic: %v", rec))
			}
		}()
		if _, err := s.kv().LaunchWindowsVM(kvReq); err != nil {
			s.logger.Error("windows VM launch failed", "name", req.Name, "err", err)
			s.launches.fail(req.Name, err.Error())
			s.eventLog.EmitEvent("vm", "create_windows", "user", req.Name, "failed", err.Error())
			return
		}
		s.launches.complete(req.Name)
		s.eventLog.EmitEvent("vm", "create_windows", "user", req.Name, "success", "")
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"name": req.Name, "message": "Windows VM launch started"})
}
