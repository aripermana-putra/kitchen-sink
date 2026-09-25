#!/usr/bin/env bash
# Verifies every CLI tool this PoC needs is installed, and offers to install
# what's missing via Homebrew. Run this first.

source "$(dirname "$0")/lib.sh"

MISSING=()
for cmd in colima docker k3d kubectl helm argocd gcloud gh git; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    MISSING+=("$cmd")
  fi
done

if [ ${#MISSING[@]} -eq 0 ]; then
  log "All required tools are installed."
else
  log "Missing tools: ${MISSING[*]}"
  log "Install with: brew install ${MISSING[*]}"
  exit 1
fi

log "Checking gcloud auth..."
if ! gcloud auth print-identity-token >/dev/null 2>&1; then
  log "gcloud is not authenticated. If this fails with an SSL error on a"
  log "Netskope-inspected corporate network, see ../gcloud-netskope-tls-fix/"
  log "before running: gcloud auth login"
fi

log "Checking gh auth for ${GITOPS_REPO_HOST}..."
if ! GH_HOST="$GITOPS_REPO_HOST" gh auth status >/dev/null 2>&1; then
  log "Not authenticated to ${GITOPS_REPO_HOST}. Run: gh auth login --hostname ${GITOPS_REPO_HOST}"
  exit 1
fi

log "Prerequisites OK."
