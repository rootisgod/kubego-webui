#!/usr/bin/env bash
# Install the `kubectl` CLI.
#
# Idempotent: if a matching `kubectl` is already on PATH, the script exits
# successfully without reinstalling. Override the version with KUBECTL_VERSION
# (e.g. v1.31.0) and the install directory with KUBECTL_INSTALL_DIR.

set -euo pipefail

KUBECTL_VERSION="${KUBECTL_VERSION:-}"
KUBECTL_INSTALL_DIR="${KUBECTL_INSTALL_DIR:-/usr/local/bin}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m==>\033[0m %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl not installed"

case "$(uname -s)" in
  Linux)  OS="linux"  ;;
  Darwin) OS="darwin" ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             die "unsupported arch: $(uname -m)" ;;
esac

if [[ -z "${KUBECTL_VERSION}" ]]; then
  log "Fetching latest kubectl stable tag"
  KUBECTL_VERSION="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
fi
log "  kubectl ${KUBECTL_VERSION} (${OS}/${ARCH})"

if command -v kubectl >/dev/null 2>&1; then
  CURRENT="$(kubectl version --client -o json 2>/dev/null | grep -o '"gitVersion":[^,]*' | head -1 | cut -d'"' -f4 || true)"
  if [[ "${CURRENT}" == "${KUBECTL_VERSION}" ]]; then
    log "kubectl ${CURRENT} already installed at $(command -v kubectl) — nothing to do"
    exit 0
  fi
  log "kubectl ${CURRENT:-unknown} already installed — replacing with ${KUBECTL_VERSION}"
fi

URL="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/${OS}/${ARCH}/kubectl"
TMP="$(mktemp)"
trap 'rm -f "${TMP}"' EXIT

log "Downloading ${URL}"
curl -fsSL -o "${TMP}" "${URL}"
chmod +x "${TMP}"

if [[ "$(id -u)" -eq 0 ]] || [[ -w "${KUBECTL_INSTALL_DIR}" ]]; then
  SUDO=""
else
  command -v sudo >/dev/null 2>&1 || die "${KUBECTL_INSTALL_DIR} is not writable and sudo is not available"
  SUDO="sudo"
fi

log "Installing to ${KUBECTL_INSTALL_DIR}/kubectl"
${SUDO} install -m 0755 "${TMP}" "${KUBECTL_INSTALL_DIR}/kubectl"

log "Done. $(kubectl version --client)"
