#!/usr/bin/env bash
# Creates one Crossplane k3d cluster on the shared PoC Docker network.
#
# Usage: 02-create-cluster.sh <cluster-name> <api-port>
# Example: 02-create-cluster.sh crossplane-1 6551

source "$(dirname "$0")/lib.sh"

NAME="${1:?usage: 02-create-cluster.sh <cluster-name> <api-port>}"
API_PORT="${2:?usage: 02-create-cluster.sh <cluster-name> <api-port>}"

docker context use "colima-${COLIMA_PROFILE}" >/dev/null

if k3d cluster list 2>/dev/null | grep -q "^${NAME}\s"; then
  log "Cluster '${NAME}' already exists."
  exit 0
fi

log "Creating cluster '${NAME}' on network '${DOCKER_NETWORK}' (API port ${API_PORT})..."
k3d cluster create "$NAME" --network "$DOCKER_NETWORK" --api-port "$API_PORT" --wait --timeout 180s

log "Cluster '${NAME}' created. Context: $(k3d_ctx "$NAME")"
