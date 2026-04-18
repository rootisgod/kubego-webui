#!/usr/bin/env bash
# Create a KinD cluster and install KubeVirt for KubeGo development.
#
# Idempotent: re-running against an existing cluster re-applies the
# KubeVirt manifests and waits for readiness instead of failing.
#
# Requires: kind, kubectl, curl. KVM is not required — KubeVirt runs in
# software-emulation mode inside KinD nodes (see useEmulation patch below).
# That is slow but sufficient for wiring tests; do not run real workloads
# under this setup.

set -euo pipefail

CLUSTER_NAME="${KUBEGO_KIND_CLUSTER:-kubego-dev}"
KUBEVIRT_VERSION="${KUBEVIRT_VERSION:-}"
CDI_VERSION="${CDI_VERSION:-}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m==>\033[0m %s\n' "$*" >&2; exit 1; }

command -v kind >/dev/null 2>&1 || die "kind not installed — https://kind.sigs.k8s.io/docs/user/quick-start/"
command -v kubectl >/dev/null 2>&1 || die "kubectl not installed"
command -v curl >/dev/null 2>&1 || die "curl not installed"

if [[ -z "${KUBEVIRT_VERSION}" ]]; then
  log "Fetching latest KubeVirt release tag"
  KUBEVIRT_VERSION="$(curl -fsSL https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)"
  log "  KubeVirt ${KUBEVIRT_VERSION}"
fi

if [[ -z "${CDI_VERSION}" ]]; then
  log "Fetching latest CDI release tag"
  CDI_VERSION="$(curl -fsSL https://api.github.com/repos/kubevirt/containerized-data-importer/releases/latest | grep -o '"tag_name":[^,]*' | head -1 | cut -d'"' -f4)"
  log "  CDI ${CDI_VERSION}"
fi

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  log "KinD cluster '${CLUSTER_NAME}' already exists — skipping create"
else
  log "Creating KinD cluster '${CLUSTER_NAME}'"
  kind create cluster --name "${CLUSTER_NAME}"
fi

kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null

log "Installing KubeVirt operator"
kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-operator.yaml"

log "Installing KubeVirt custom resource"
kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${KUBEVIRT_VERSION}/kubevirt-cr.yaml"

log "Enabling software emulation (KinD nodes have no /dev/kvm)"
kubectl -n kubevirt patch kubevirt kubevirt --type=merge --patch '{"spec":{"configuration":{"developerConfiguration":{"useEmulation":true}}}}'

log "Installing CDI operator (DataVolume support)"
kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-operator.yaml"
kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${CDI_VERSION}/cdi-cr.yaml"

log "Waiting for KubeVirt to become Available (can take ~5 minutes on first install)"
kubectl -n kubevirt wait --for=condition=Available --timeout=10m kv/kubevirt

log "Waiting for CDI to become Available"
kubectl -n cdi wait --for=condition=Available --timeout=10m cdi/cdi

log "KubeVirt installed:"
kubectl -n kubevirt get kubevirt kubevirt -o jsonpath='  version: {.status.observedKubeVirtVersion}{"\n"}'

cat <<EOF

Ready. Next steps:

  task run         # build + run the KubeGo server against this cluster
  task smoke       # end-to-end smoke test
  task kind:down   # tear down the cluster when done

EOF
