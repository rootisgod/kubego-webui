#!/usr/bin/env bash
# Install the `kind` CLI (Kubernetes in Docker).
#
# Idempotent: if a matching `kind` is already on PATH, the script exits
# successfully without reinstalling. Override the version with KIND_VERSION
# and the install directory with KIND_INSTALL_DIR.

set -euo pipefail

KIND_VERSION="${KIND_VERSION:-}"
KIND_INSTALL_DIR="${KIND_INSTALL_DIR:-/usr/local/bin}"

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

if [[ -z "${KIND_VERSION}" ]]; then
  log "Fetching latest kind release tag"
  KIND_VERSION="$(curl -fsSL https://api.github.com/repos/kubernetes-sigs/kind/releases/latest | grep -o '"tag_name":[^,]*' | head -1 | cut -d'"' -f4)"
fi
log "  kind ${KIND_VERSION} (${OS}/${ARCH})"

if command -v kind >/dev/null 2>&1; then
  CURRENT="$(kind version 2>/dev/null | awk '{print $2}' || true)"
  if [[ "${CURRENT}" == "${KIND_VERSION}" ]]; then
    log "kind ${CURRENT} already installed at $(command -v kind) — nothing to do"
    exit 0
  fi
  log "kind ${CURRENT:-unknown} already installed — replacing with ${KIND_VERSION}"
fi

URL="https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-${OS}-${ARCH}"
TMP="$(mktemp)"
trap 'rm -f "${TMP}"' EXIT

log "Downloading ${URL}"
curl -fsSL -o "${TMP}" "${URL}"
chmod +x "${TMP}"

if [[ "$(id -u)" -eq 0 ]] || [[ -w "${KIND_INSTALL_DIR}" ]]; then
  SUDO=""
else
  command -v sudo >/dev/null 2>&1 || die "${KIND_INSTALL_DIR} is not writable and sudo is not available"
  SUDO="sudo"
fi

log "Installing to ${KIND_INSTALL_DIR}/kind"
${SUDO} install -m 0755 "${TMP}" "${KIND_INSTALL_DIR}/kind"

log "Done. $(kind version)"
