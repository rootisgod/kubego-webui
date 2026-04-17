#!/usr/bin/env bash
# Delete the KubeGo KinD development cluster.

set -euo pipefail

CLUSTER_NAME="${KUBEGO_KIND_CLUSTER:-kubego-dev}"

if ! command -v kind >/dev/null 2>&1; then
  echo "kind not installed — nothing to do." >&2
  exit 0
fi

if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  echo "==> Deleting KinD cluster '${CLUSTER_NAME}'"
  kind delete cluster --name "${CLUSTER_NAME}"
else
  echo "==> No KinD cluster named '${CLUSTER_NAME}' to delete"
fi
