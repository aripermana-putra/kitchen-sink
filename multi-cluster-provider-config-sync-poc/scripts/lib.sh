#!/usr/bin/env bash
# Shared config and helpers for the MCUCP-306 PoC scripts.
# Source this from every other script: `source "$(dirname "$0")/lib.sh"`

set -euo pipefail

COLIMA_PROFILE="mcucp-poc"
DOCKER_NETWORK="mcucp-poc-net"
HUB_CLUSTER="argocd-hub"
HUB_API_PORT="6550"

GCP_PROJECT="sub-gcp-ucp-clsd-sandbox"
GCP_SA_NAME="mcucp-306-eso-poc"
GCP_SA_EMAIL="${GCP_SA_NAME}@${GCP_PROJECT}.iam.gserviceaccount.com"

GITOPS_REPO_HOST="github.com"
GITOPS_REPO_ORG="clsd-ucp"
GITOPS_REPO_NAME="mcucp-306-provider-config-gitops"
GITOPS_REPO_URL="https://${GITOPS_REPO_HOST}/${GITOPS_REPO_ORG}/${GITOPS_REPO_NAME}.git"
GITOPS_LOCAL_CLONE="${GITOPS_LOCAL_CLONE:-/tmp/mcucp-306-gitops}"

CROSSPLANE_CHART_REPO="https://charts.crossplane.io/stable"
PROVIDER_GCP_PACKAGE="xpkg.upbound.io/upbound/provider-family-gcp:v2.6.0"

# WIF pool/provider naming convention every tenant is expected to use in their
# own GCP project when following UCP's onboarding instructions (see the
# wif-gcp PoC). Fixed names, not tenant-specific -- only the project number
# and target SA email vary per registration.
WIF_POOL_ID="ucp-wif-pool"
WIF_PROVIDER_ID="ucp-k8s-provider"
WIF_TOKEN_FILE_PATH="/var/run/secrets/tokens/gcp-token"

CLUSTER_LABEL_KEY="mcucp.io/role"
CLUSTER_LABEL_VALUE="crossplane"

k3d_ctx() { echo "k3d-$1"; }

log() { echo "==> $*" >&2; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "Missing required command: $1" >&2; exit 1; }
}

argocd_port_forward() {
  pkill -f "port-forward svc/argocd-server" 2>/dev/null || true
  kubectl --context "$(k3d_ctx "$HUB_CLUSTER")" -n argocd port-forward svc/argocd-server 8443:443 \
    >/tmp/argocd-pf.log 2>&1 &
  disown
  sleep 3
}

argocd_cli_login() {
  argocd_port_forward
  local pw
  pw=$(kubectl --context "$(k3d_ctx "$HUB_CLUSTER")" -n argocd get secret argocd-initial-admin-secret \
    -o jsonpath='{.data.password}' | base64 -d)
  argocd login localhost:8443 --username admin --password "$pw" --insecure
}

gh_token() {
  GH_HOST="$GITOPS_REPO_HOST" gh auth token
}
