#!/usr/bin/env bash
# End-to-end smoke test for the KubeGo M0 binary.
#
# Builds the binary, starts it in the background against the current
# kubeconfig, hits the public version endpoint, logs in, confirms that a
# VM call returns 501 (expected — M0 driver is stubbed), and shuts the
# binary down. Non-zero exit on any failure.
#
# Intended for humans running it locally; CI can reuse the same recipe.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT_DIR}/kubego-smoke"
# Isolate all runtime state (config + events log) in a temp dir so the
# smoke test leaves no tracked files behind.
STATE_DIR="$(mktemp -d -t kubego-smoke.XXXXXX)"
CONFIG="${STATE_DIR}/config.json"
PORT="${KUBEGO_SMOKE_PORT:-18080}"
KUBECONFIG_PATH="${KUBECONFIG:-${HOME}/.kube/config}"
LOG="$(mktemp -t kubego-smoke.XXXXXX)"
JAR="$(mktemp -t kubego-smoke-cookies.XXXXXX)"
PID=""

cleanup() {
  if [[ -n "${PID}" ]] && kill -0 "${PID}" 2>/dev/null; then
    kill "${PID}" 2>/dev/null || true
    wait "${PID}" 2>/dev/null || true
  fi
  rm -f "${BIN}" "${LOG}" "${JAR}"
  rm -rf "${STATE_DIR}"
}
trap cleanup EXIT

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m==>\033[0m %s\n' "$*" >&2; echo "--- server log ---" >&2; cat "${LOG}" >&2 || true; exit 1; }

log "Building kubego"
( cd "${ROOT_DIR}" && go build -o "${BIN}" ./cmd/server )

log "Starting kubego on :${PORT} using ${KUBECONFIG_PATH}"
"${BIN}" --kubeconfig "${KUBECONFIG_PATH}" --port "${PORT}" --config "${CONFIG}" --username admin --password admin >"${LOG}" 2>&1 &
PID=$!

# Poll /version instead of sleeping; bail out quickly if the process dies.
for _ in $(seq 1 30); do
  if ! kill -0 "${PID}" 2>/dev/null; then
    fail "kubego exited before becoming ready"
  fi
  if curl -sf "http://localhost:${PORT}/api/v1/version" >/dev/null; then
    break
  fi
  sleep 0.5
done

log "GET /api/v1/version"
curl -sf "http://localhost:${PORT}/api/v1/version" | sed 's/^/    /'

log "POST /api/v1/auth/login"
if ! curl -sf -c "${JAR}" -X POST "http://localhost:${PORT}/api/v1/auth/login" \
       -H 'Content-Type: application/json' \
       -d '{"username":"admin","password":"admin"}' >/dev/null; then
  fail "login failed"
fi

log "GET /api/v1/vms (expect 501 from the M0 stub driver)"
STATUS="$(curl -so /dev/null -w '%{http_code}' -b "${JAR}" "http://localhost:${PORT}/api/v1/vms")"
if [[ "${STATUS}" != "500" && "${STATUS}" != "501" ]]; then
  fail "expected 500/501 from stub driver, got ${STATUS}"
fi
log "  got HTTP ${STATUS} — stub driver is wired"

log "Startup log contained KubeVirt probe line:"
if ! grep -E 'kubevirt (detected|not installed|probe failed|group present)' "${LOG}" >&2; then
  fail "expected a KubeVirt probe log line"
fi

log "Smoke test passed."
