#!/usr/bin/env bash
# Starts the single Colima VM this PoC runs in, fixes the inotify limits
# that break running more than ~2 k3s clusters worth of containers, creates
# the shared Docker network, and creates the ArgoCD hub cluster.
#
# Idempotent: safe to re-run.

source "$(dirname "$0")/lib.sh"

if ! colima list 2>/dev/null | grep -q "^${COLIMA_PROFILE}\s.*Running"; then
  log "Starting Colima VM '${COLIMA_PROFILE}'..."
  colima start --profile "$COLIMA_PROFILE" --cpu 4 --memory 8 --disk 40
else
  log "Colima VM '${COLIMA_PROFILE}' already running."
fi

docker context use "colima-${COLIMA_PROFILE}"

CURRENT_LIMIT=$(colima ssh --profile "$COLIMA_PROFILE" -- sysctl -n fs.inotify.max_user_instances)
if [ "$CURRENT_LIMIT" -lt 8192 ]; then
  log "fs.inotify.max_user_instances is ${CURRENT_LIMIT}, too low for 3 k3s clusters."
  log "Bumping it and persisting across VM restarts..."
  colima ssh --profile "$COLIMA_PROFILE" -- sudo sh -c "
    sysctl -w fs.inotify.max_user_instances=8192
    grep -q inotify.max_user_instances /etc/sysctl.conf || echo 'fs.inotify.max_user_instances=8192' >> /etc/sysctl.conf
    mkdir -p /etc/systemd/system.conf.d
    printf '[Manager]\nDefaultLimitNOFILE=1048576\n' > /etc/systemd/system.conf.d/limits.conf
  "
  log "Restarting Colima VM to apply the systemd limit change..."
  colima stop --profile "$COLIMA_PROFILE"
  colima start --profile "$COLIMA_PROFILE" --cpu 4 --memory 8 --disk 40
  docker context use "colima-${COLIMA_PROFILE}"
fi

if ! docker network inspect "$DOCKER_NETWORK" >/dev/null 2>&1; then
  log "Creating Docker network '${DOCKER_NETWORK}'..."
  docker network create "$DOCKER_NETWORK"
else
  log "Docker network '${DOCKER_NETWORK}' already exists."
fi

if ! k3d cluster list 2>/dev/null | grep -q "^${HUB_CLUSTER}\s"; then
  log "Creating ArgoCD hub cluster '${HUB_CLUSTER}'..."
  k3d cluster create "$HUB_CLUSTER" --network "$DOCKER_NETWORK" \
    --api-port "$HUB_API_PORT" -p "8080:80@loadbalancer" --wait --timeout 180s
else
  log "Hub cluster '${HUB_CLUSTER}' already exists."
fi

HUB_CTX=$(k3d_ctx "$HUB_CLUSTER")
if ! kubectl --context "$HUB_CTX" get ns argocd >/dev/null 2>&1; then
  log "Installing ArgoCD in '${HUB_CLUSTER}'..."
  kubectl --context "$HUB_CTX" create namespace argocd
  # Plain `kubectl apply` rejects the ApplicationSet CRD here: its
  # last-applied-configuration annotation exceeds the 262144-byte limit.
  kubectl --context "$HUB_CTX" apply -n argocd \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml \
    --server-side --force-conflicts
  kubectl --context "$HUB_CTX" wait --for=condition=Available deployment --all -n argocd --timeout=180s
else
  log "ArgoCD already installed in '${HUB_CLUSTER}'."
fi

log "Environment bootstrap complete."
log "Log in to ArgoCD CLI with: source scripts/lib.sh && argocd_cli_login"
