#!/usr/bin/env bash
# Tears down everything this PoC created locally. Does NOT delete the GCP
# service account, secrets, or the GitHub repo -- remove those manually if
# you're done with them for good (see README.md "Teardown" section).

source "$(dirname "$0")/lib.sh"

for name in "$HUB_CLUSTER" crossplane-1 crossplane-2; do
  if k3d cluster list 2>/dev/null | grep -q "^${name}\s"; then
    log "Deleting cluster '${name}'..."
    k3d cluster delete "$name"
  fi
done

if colima list 2>/dev/null | grep -q "^${COLIMA_PROFILE}\s"; then
  read -r -p "Stop and delete Colima profile '${COLIMA_PROFILE}'? [y/N] " ans
  if [[ "$ans" =~ ^[Yy]$ ]]; then
    colima stop --profile "$COLIMA_PROFILE"
    colima delete --profile "$COLIMA_PROFILE" --force
  fi
fi

log "Local teardown complete."
log "Remaining outside this machine: GCP service account (${GCP_SA_EMAIL}), any"
log "GCP Secret Manager secrets created, and the GitHub repo ${GITOPS_REPO_URL}."
