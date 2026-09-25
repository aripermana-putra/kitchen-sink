#!/usr/bin/env bash
# Clones/updates the GitOps repo, registers it as an ArgoCD repo credential,
# and applies the three platform ApplicationSets:
#   - platform-eso                  (installs ESO on every labeled cluster)
#   - platform-clustersecretstore   (installs the ClusterSecretStore)
#   - tenant-provider-config-matrix (fans out ExternalSecret+ProviderConfig
#                                     per (registration, cluster))
#
# Run once per environment. Re-running is safe (kubectl apply is idempotent).

source "$(dirname "$0")/lib.sh"

HUB_CTX=$(k3d_ctx "$HUB_CLUSTER")

if [ -d "$GITOPS_LOCAL_CLONE/.git" ]; then
  git -C "$GITOPS_LOCAL_CLONE" pull --ff-only
else
  git clone "$GITOPS_REPO_URL" "$GITOPS_LOCAL_CLONE"
fi

log "Creating/updating ArgoCD repo credential for ${GITOPS_REPO_URL}..."
kubectl --context "$HUB_CTX" -n argocd delete secret repo-mcucp-306-gitops --ignore-not-found
kubectl --context "$HUB_CTX" -n argocd create secret generic repo-mcucp-306-gitops \
  --from-literal=type=git \
  --from-literal=url="$GITOPS_REPO_URL" \
  --from-literal=username="$(gh api user -q .login --hostname "$GITOPS_REPO_HOST")" \
  --from-literal=password="$(gh_token)"
kubectl --context "$HUB_CTX" -n argocd label secret repo-mcucp-306-gitops \
  "argocd.argoproj.io/secret-type=repository"

log "Applying platform ApplicationSets..."
kubectl --context "$HUB_CTX" apply -f "$GITOPS_LOCAL_CLONE/gitops/platform/eso-appset.yaml"
kubectl --context "$HUB_CTX" apply -f "$GITOPS_LOCAL_CLONE/gitops/platform/clustersecretstore-appset.yaml"
kubectl --context "$HUB_CTX" apply -f "$GITOPS_LOCAL_CLONE/gitops/tenant-matrix-appset.yaml"

log "Done. Check status with: argocd app list"
