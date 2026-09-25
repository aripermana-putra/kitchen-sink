#!/usr/bin/env bash
# Runs every check in the "Verifying each step" table in README.md against
# whatever's currently running, and prints PASS/FAIL for each. Safe to run at
# any point -- checks that don't apply yet (e.g. no clusters created) just
# report FAIL/missing rather than erroring out the whole script.
#
# Usage: 08-verify-environment.sh [cluster-name ...]
# Defaults to checking crossplane-1 and crossplane-2 if no names are given.

source "$(dirname "$0")/lib.sh"
set +e   # keep going through failures -- this script reports, it doesn't gate

CLUSTERS=("${@:-crossplane-1 crossplane-2}")
[ "$#" -gt 0 ] && CLUSTERS=("$@")

PASS=0
FAIL=0

check() {
  local desc="$1"; shift
  if "$@" >/tmp/verify-check.out 2>&1; then
    echo "PASS  $desc"
    PASS=$((PASS+1))
  else
    echo "FAIL  $desc"
    sed 's/^/      /' /tmp/verify-check.out | head -3
    FAIL=$((FAIL+1))
  fi
}

echo "=== Environment ==="
check "Colima VM '${COLIMA_PROFILE}' is running" \
  bash -c "colima list 2>/dev/null | grep -q '^${COLIMA_PROFILE}\s.*Running'"
check "Docker network '${DOCKER_NETWORK}' exists" \
  docker network inspect "$DOCKER_NETWORK"

echo
echo "=== ArgoCD hub ==="
HUB_CTX=$(k3d_ctx "$HUB_CLUSTER")
check "Hub cluster API server reachable" \
  kubectl --context "$HUB_CTX" get --raw /healthz
check "ArgoCD namespace exists" \
  kubectl --context "$HUB_CTX" get ns argocd
check "argocd-server deployment Available" \
  kubectl --context "$HUB_CTX" -n argocd wait --for=condition=Available deployment/argocd-server --timeout=5s
check "argocd-application-controller running" \
  bash -c "kubectl --context $HUB_CTX -n argocd get pod argocd-application-controller-0 -o jsonpath='{.status.phase}' | grep -q Running"

for NAME in "${CLUSTERS[@]}"; do
  CTX=$(k3d_ctx "$NAME")
  echo
  echo "=== Cluster: ${NAME} ==="
  check "Cluster API server reachable" \
    kubectl --context "$CTX" get --raw /healthz
  check "Registered with ArgoCD (labeled ${CLUSTER_LABEL_KEY}=${CLUSTER_LABEL_VALUE})" \
    bash -c "kubectl --context $HUB_CTX -n argocd get secret cluster-${NAME} -o jsonpath='{.metadata.labels.${CLUSTER_LABEL_KEY//./\\.}}' | grep -q ${CLUSTER_LABEL_VALUE}"
  check "Crossplane core installed" \
    kubectl --context "$CTX" -n crossplane-system get deployment crossplane
  check "provider-gcp Healthy" \
    bash -c "kubectl --context $CTX get providers.pkg.crossplane.io provider-gcp -o jsonpath='{.status.conditions[?(@.type==\"Healthy\")].status}' | grep -q True"
  check "ESO deployment running" \
    kubectl --context "$CTX" -n external-secrets get deployment external-secrets
  check "eso-gcp-sm-key Secret present" \
    kubectl --context "$CTX" -n external-secrets get secret eso-gcp-sm-key
  check "ClusterSecretStore Valid" \
    bash -c "kubectl --context $CTX get clustersecretstore gcp-secret-manager -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
done

echo
echo "=== ArgoCD Applications ==="
if kubectl --context "$HUB_CTX" -n argocd get applications.argoproj.io >/dev/null 2>&1; then
  kubectl --context "$HUB_CTX" -n argocd get applications.argoproj.io \
    -o custom-columns='NAME:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status'
else
  echo "FAIL  no Applications found (has 06-apply-platform-appsets.sh run?)"
  FAIL=$((FAIL+1))
fi

echo
echo "=== Tenant registrations ==="
for NAME in "${CLUSTERS[@]}"; do
  CTX=$(k3d_ctx "$NAME")
  for es in $(kubectl --context "$CTX" -n crossplane-system get externalsecret -o name 2>/dev/null); do
    check "${es} SecretSynced on ${NAME}" \
      bash -c "kubectl --context $CTX -n crossplane-system get $es -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
  done
  for pc in $(kubectl --context "$CTX" get providerconfig.gcp.upbound.io -o name 2>/dev/null); do
    # provider-family-gcp's ProviderConfig has no status.conditions to check --
    # existence + the ArgoCD Application health above is the signal that exists.
    check "${pc} exists on ${NAME}" \
      kubectl --context "$CTX" get "$pc"
  done
done

echo
echo "=== Result: ${PASS} passed, ${FAIL} failed ==="
rm -f /tmp/verify-check.out
[ "$FAIL" -eq 0 ]
