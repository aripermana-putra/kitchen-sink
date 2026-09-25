#!/usr/bin/env bash
# Simulates UCP backend's two writes when a tenant registers a GCP provider
# config: one write to GCP Secret Manager (the real WIF external_account
# credential config JSON -- the same shape validated in the wif-gcp PoC, not
# a flat stand-in), one write to the tenant registry in Git (identifiers +
# the one literal field ProviderConfig.spec needs -- no credential material,
# and no copy of anything that's inside the Secret Manager payload).
# ArgoCD's matrix ApplicationSet picks up the new registry file on its own
# and fans it out to every registered cluster.
#
# Usage: 07-onboard-tenant.sh <tenant> <registration-id> <gcp-project-number> <gcp-sa-email>
# Example: 07-onboard-tenant.sh acme acme-proj-123 123456789012 acme-proj-123@proj-123.iam.gserviceaccount.com
#
# <gcp-project-number> is the tenant's real GCP project number (from
# `gcloud projects describe <project-id> --format='value(projectNumber)'`).
# For a fictional/non-existent tenant project, any numeric placeholder works
# -- this PoC validates the sync mechanism, not real WIF authentication.

source "$(dirname "$0")/lib.sh"

TENANT="${1:?usage: 07-onboard-tenant.sh <tenant> <registration-id> <gcp-project-number> <gcp-sa-email>}"
REG_ID="${2:?missing registration-id}"
GCP_PROJECT_NUMBER="${3:?missing gcp-project-number}"
GCP_SA="${4:?missing gcp-sa-email}"
GCP_PROJECT_ID="${5:-$GCP_PROJECT_NUMBER}"   # optional: real project ID for ProviderConfig.spec.projectID, defaults to the number
SECRET_NAME="${REG_ID}-config"

log "Constructing the external_account credential config and writing it to GCP Secret Manager (${SECRET_NAME})..."
TMP=$(mktemp)
cat > "$TMP" <<EOF
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/${GCP_PROJECT_NUMBER}/locations/global/workloadIdentityPools/${WIF_POOL_ID}/providers/${WIF_PROVIDER_ID}",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/${GCP_SA}:generateAccessToken",
  "credential_source": {
    "file": "${WIF_TOKEN_FILE_PATH}",
    "format": { "type": "text" }
  }
}
EOF
if gcloud secrets describe "$SECRET_NAME" --project "$GCP_PROJECT" >/dev/null 2>&1; then
  gcloud secrets versions add "$SECRET_NAME" --project "$GCP_PROJECT" --data-file "$TMP"
else
  gcloud secrets create "$SECRET_NAME" --project "$GCP_PROJECT" \
    --replication-policy automatic --data-file "$TMP"
fi
rm -f "$TMP"

log "Writing registry entry (no credential material, no copy of the secret payload) and pushing to Git..."
if [ -d "$GITOPS_LOCAL_CLONE/.git" ]; then
  git -C "$GITOPS_LOCAL_CLONE" pull --ff-only
else
  git clone "$GITOPS_REPO_URL" "$GITOPS_LOCAL_CLONE"
fi

# gcpProjectId is a literal ProviderConfig.spec field -- Crossplane has no way
# to source it from a Secret, so it's the one field that legitimately lives
# here rather than inside the Secret Manager payload.
cat > "${GITOPS_LOCAL_CLONE}/registry/${REG_ID}.json" <<EOF
{
  "tenant": "${TENANT}",
  "registrationId": "${REG_ID}",
  "gcpProjectId": "${GCP_PROJECT_ID}",
  "secretManagerSecretName": "${SECRET_NAME}"
}
EOF

git -C "$GITOPS_LOCAL_CLONE" add "registry/${REG_ID}.json"
git -C "$GITOPS_LOCAL_CLONE" -c user.email="poc@mcucp-306" -c user.name="MCUCP-306 PoC" \
  commit -m "feat: onboard ${TENANT} provider-config registration ${REG_ID}"
git -C "$GITOPS_LOCAL_CLONE" push \
  "https://$(gh api user -q .login --hostname "$GITOPS_REPO_HOST"):$(gh_token)@${GITOPS_REPO_HOST}/${GITOPS_REPO_ORG}/${GITOPS_REPO_NAME}.git" main:main

log "Onboarded. The matrix ApplicationSet's git generator will pick this up on its next"
log "poll (a few minutes) and render pc-${REG_ID}-<cluster> for every registered cluster."
log "Check with: argocd app list"
