package api

import (
	"encoding/json"
	"net/http"

	"github.com/rootisgod/kubego-webui/pkg/kubevirt"
)

// Pinned controller tag — the kind-provider manifest is keyed off this
// URL, and auto-resolving "latest" makes reproducible installs painful.
// Bump when a new stable arrives; verified against KinD 0.24+.
const ingressNginxManifestURL = "https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.11.2/deploy/static/provider/kind/deploy.yaml"

func (s *Server) handleIngressStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.kv().IngressControllerStatus()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleIngressInstall streams a kubectl apply + wait chain for
// ingress-nginx's KinD preset. The control-plane node is labelled
// `ingress-ready=true` first so the manifest's nodeSelector matches —
// otherwise the controller pod sits Pending forever.
func (s *Server) handleIngressInstall(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	setSSEHeaders(w, flusher)

	kctx := s.clusters.ActiveContext()
	if kctx == "" || kctx == kubevirt.InClusterContext {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: "ingress install is only supported for external contexts (kubectl-backed)"})
		return
	}
	kcfg := s.clusters.KubeconfigPath()

	kArgs := func(extra ...string) []string {
		base := []string{"--context", kctx}
		if kcfg != "" {
			base = append(base, "--kubeconfig", kcfg)
		}
		return append(base, extra...)
	}

	ctx := r.Context()

	streamPhase(w, flusher, "Labelling nodes with ingress-ready=true (required by the KinD preset)")
	if err := streamCommand(ctx, w, flusher, "kubectl", kArgs("label", "nodes", "--all", "ingress-ready=true", "--overwrite")...); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	streamPhase(w, flusher, "Applying ingress-nginx (KinD preset)")
	if err := streamCommand(ctx, w, flusher, "kubectl", kArgs("apply", "-f", ingressNginxManifestURL)...); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	streamPhase(w, flusher, "Waiting for the controller to become Ready")
	if err := streamCommand(ctx, w, flusher, "kubectl", kArgs("wait",
		"--namespace", "ingress-nginx",
		"--for=condition=ready", "pod",
		"--selector=app.kubernetes.io/component=controller",
		"--timeout=5m")...); err != nil {
		writeClusterSSE(w, flusher, clusterSSEEvent{Type: "error", Error: err.Error()})
		return
	}

	writeClusterSSE(w, flusher, clusterSSEEvent{Type: "done"})
}

func (s *Server) handleListVMIngresses(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	out, err := s.kv().ListVMIngresses(vm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type exposeVMRequest struct {
	RemotePort int `json:"remote_port"`
}

func (s *Server) handleCreateVMIngress(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	var req exposeVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RemotePort <= 0 || req.RemotePort > 65535 {
		writeError(w, http.StatusBadRequest, "remote_port must be 1..65535")
		return
	}
	info, err := s.kv().ExposeVMPort(vm, req.RemotePort)
	if err != nil {
		s.eventLog.EmitHTTPEvent(r, "vm", "ingress_expose", vm, "failed", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "vm", "ingress_expose", vm, "success", info.Hostname)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleDeleteVMIngress(w http.ResponseWriter, r *http.Request) {
	vm, ok := validVMName(w, r, "name")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.kv().DeleteVMIngress(vm, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.eventLog.EmitHTTPEvent(r, "vm", "ingress_remove", vm, "success", id)
	writeMessage(w, "ingress exposure removed")
}
