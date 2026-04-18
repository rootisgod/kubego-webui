#!/usr/bin/env bash
# Install the `k9s` CLI (terminal UI for Kubernetes).
#
# Idempotent: if a matching `k9s` is already on PATH, the script exits
# successfully without reinstalling. Override the version with K9S_VERSION
# (e.g. v0.32.5) and the install directory with K9S_INSTALL_DIR.

set -euo pipefail

K9S_VERSION="${K9S_VERSION:-}"
K9S_INSTALL_DIR="${K9S_INSTALL_DIR:-/usr/local/bin}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m==>\033[0m %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl not installed"
command -v tar  >/dev/null 2>&1 || die "tar not installed"

case "$(uname -s)" in
  Linux)  OS_TAG="Linux"  ;;
  Darwin) OS_TAG="Darwin" ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  ARCH_TAG="amd64" ;;
  aarch64|arm64) ARCH_TAG="arm64" ;;
  *)             die "unsupported arch: $(uname -m)" ;;
esac

if [[ -z "${K9S_VERSION}" ]]; then
  log "Fetching latest k9s release tag"
  K9S_VERSION="$(curl -fsSL https://api.github.com/repos/derailed/k9s/releases/latest | grep -o '"tag_name":[^,]*' | head -1 | cut -d'"' -f4)"
fi
log "  k9s ${K9S_VERSION} (${OS_TAG}/${ARCH_TAG})"

if command -v k9s >/dev/null 2>&1; then
  CURRENT="$(k9s version --short 2>/dev/null | awk '/Version/ {print $2}' || true)"
  if [[ "${CURRENT}" == "${K9S_VERSION}" ]]; then
    log "k9s ${CURRENT} already installed at $(command -v k9s) — nothing to do"
    exit 0
  fi
  log "k9s ${CURRENT:-unknown} already installed — replacing with ${K9S_VERSION}"
fi

URL="https://github.com/derailed/k9s/releases/download/${K9S_VERSION}/k9s_${OS_TAG}_${ARCH_TAG}.tar.gz"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

log "Downloading ${URL}"
curl -fsSL -o "${TMPDIR}/k9s.tar.gz" "${URL}"
tar -xzf "${TMPDIR}/k9s.tar.gz" -C "${TMPDIR}" k9s
chmod +x "${TMPDIR}/k9s"

if [[ "$(id -u)" -eq 0 ]] || [[ -w "${K9S_INSTALL_DIR}" ]]; then
  SUDO=""
else
  command -v sudo >/dev/null 2>&1 || die "${K9S_INSTALL_DIR} is not writable and sudo is not available"
  SUDO="sudo"
fi

log "Installing to ${K9S_INSTALL_DIR}/k9s"
${SUDO} install -m 0755 "${TMPDIR}/k9s" "${K9S_INSTALL_DIR}/k9s"

log "Done. $(k9s version --short 2>/dev/null || k9s version)"
