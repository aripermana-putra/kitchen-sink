#!/usr/bin/env bash
# One-time: creates the GCP service account ESO uses to read Secret Manager,
# grants it secretmanager.secretAccessor, and mints a key. Then, per cluster:
# stores that key as the eso-gcp-sm-key Secret ESO's ClusterSecretStore reads.
#
# This is NOT Workload Identity Federation -- these are local k3d clusters,
# not GKE, so ESO's own GCP Secret Manager auth cannot use WIF the way the
# wif-gcp PoC's provider pod does (that mode requires real GKE node metadata).
# A stored key is the PoC-only, non-production stand-in. See poc-report.md.
#
# Usage:
#   05-setup-gcp-secret-manager-access.sh create-sa
#   05-setup-gcp-secret-manager-access.sh install-key <cluster-name>

source "$(dirname "$0")/lib.sh"

cmd="${1:?usage: create-sa | install-key <cluster-name>}"

case "$cmd" in
  create-sa)
    if ! gcloud iam service-accounts describe "$GCP_SA_EMAIL" --project "$GCP_PROJECT" >/dev/null 2>&1; then
      log "Creating service account ${GCP_SA_NAME}..."
      gcloud iam service-accounts create "$GCP_SA_NAME" \
        --project "$GCP_PROJECT" \
        --display-name "MCUCP-306 ESO PoC (Secret Manager reader)"
    else
      log "Service account ${GCP_SA_EMAIL} already exists."
    fi

    log "Granting roles/secretmanager.secretAccessor..."
    gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
      --member="serviceAccount:${GCP_SA_EMAIL}" \
      --role="roles/secretmanager.secretAccessor" \
      --condition=None >/dev/null

    log "Minting a key at /tmp/mcucp-306-eso-key.json..."
    gcloud iam service-accounts keys create /tmp/mcucp-306-eso-key.json \
      --iam-account="$GCP_SA_EMAIL"
    log "Run 'install-key <cluster-name>' for each Crossplane cluster, then delete the key file."
    ;;

  install-key)
    NAME="${2:?usage: install-key <cluster-name>}"
    CTX=$(k3d_ctx "$NAME")
    [ -f /tmp/mcucp-306-eso-key.json ] || { echo "Run 'create-sa' first" >&2; exit 1; }
    if ! kubectl --context "$CTX" get ns external-secrets >/dev/null 2>&1; then
      kubectl --context "$CTX" create namespace external-secrets
    fi
    kubectl --context "$CTX" -n external-secrets create secret generic eso-gcp-sm-key \
      --from-file=key.json=/tmp/mcucp-306-eso-key.json \
      --dry-run=client -o yaml | kubectl --context "$CTX" apply -f -
    log "eso-gcp-sm-key installed on '${NAME}'."
    ;;

  *)
    echo "unknown command: $cmd" >&2
    exit 1
    ;;
esac
