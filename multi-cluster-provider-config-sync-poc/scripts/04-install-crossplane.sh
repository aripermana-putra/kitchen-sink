#!/usr/bin/env bash
# Installs Crossplane core + provider-family-gcp on a cluster. The
# ProviderConfig CRD (gcp.upbound.io/v1beta1) only exists after
# provider-family-gcp reports Healthy=True -- ArgoCD-rendered ProviderConfig
# objects fail to sync until this finishes.
#
# Usage: 04-install-crossplane.sh <cluster-name>

source "$(dirname "$0")/lib.sh"

NAME="${1:?usage: 04-install-crossplane.sh <cluster-name>}"
CTX=$(k3d_ctx "$NAME")

helm repo add crossplane-stable "$CROSSPLANE_CHART_REPO" >/dev/null 2>&1 || true
helm repo update crossplane-stable >/dev/null

if ! kubectl --context "$CTX" get ns crossplane-system >/dev/null 2>&1; then
  kubectl --context "$CTX" create namespace crossplane-system
fi

if ! helm --kube-context "$CTX" status crossplane -n crossplane-system >/dev/null 2>&1; then
  log "Installing Crossplane core on '${NAME}'..."
  helm --kube-context "$CTX" install crossplane crossplane-stable/crossplane \
    -n crossplane-system --wait --timeout 180s
else
  log "Crossplane core already installed on '${NAME}'."
fi

if ! kubectl --context "$CTX" get providers.pkg.crossplane.io provider-gcp >/dev/null 2>&1; then
  log "Installing provider-family-gcp on '${NAME}'..."
  cat <<EOF | kubectl --context "$CTX" apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-gcp
spec:
  package: ${PROVIDER_GCP_PACKAGE}
EOF
fi

log "Waiting for provider-gcp to become healthy..."
for i in $(seq 1 30); do
  HEALTHY=$(kubectl --context "$CTX" get providers.pkg.crossplane.io provider-gcp \
    -o jsonpath='{.status.conditions[?(@.type=="Healthy")].status}' 2>/dev/null || true)
  [ "$HEALTHY" = "True" ] && { log "provider-gcp is healthy on '${NAME}'."; exit 0; }
  sleep 10
done
echo "provider-gcp never became healthy on '${NAME}'" >&2
exit 1
