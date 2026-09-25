#!/usr/bin/env bash
# Registers a k3d cluster with the ArgoCD hub: creates a service account +
# cluster-admin binding + long-lived token on the target cluster, then
# creates the corresponding ArgoCD cluster Secret on the hub, using the
# k3d server container's in-Docker-network name as the address (not the
# kubeconfig's host-only 0.0.0.0:<port> address, which the hub's ArgoCD pod
# cannot reach).
#
# Usage: 03-register-cluster-with-argocd.sh <cluster-name>
# Example: 03-register-cluster-with-argocd.sh crossplane-1

source "$(dirname "$0")/lib.sh"

NAME="${1:?usage: 03-register-cluster-with-argocd.sh <cluster-name>}"
CTX=$(k3d_ctx "$NAME")
HUB_CTX=$(k3d_ctx "$HUB_CLUSTER")

log "Creating argocd-manager service account on '${NAME}'..."
kubectl --context "$CTX" create serviceaccount argocd-manager -n kube-system \
  --dry-run=client -o yaml | kubectl --context "$CTX" apply -f -

kubectl --context "$CTX" create clusterrolebinding argocd-manager-cluster-admin \
  --clusterrole=cluster-admin --serviceaccount=kube-system:argocd-manager \
  --dry-run=client -o yaml | kubectl --context "$CTX" apply -f -

cat <<EOF | kubectl --context "$CTX" apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: argocd-manager-token
  namespace: kube-system
  annotations:
    kubernetes.io/service-account.name: argocd-manager
type: kubernetes.io/service-account-token
EOF

log "Waiting for the token to populate..."
for i in $(seq 1 15); do
  TOKEN=$(kubectl --context "$CTX" -n kube-system get secret argocd-manager-token \
    -o jsonpath='{.data.token}' 2>/dev/null | base64 -d || true)
  [ -n "$TOKEN" ] && break
  sleep 2
done
[ -n "$TOKEN" ] || { echo "Token never populated" >&2; exit 1; }

log "Creating ArgoCD cluster Secret for '${NAME}' on the hub..."
cat <<EOF | kubectl --context "$HUB_CTX" apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: cluster-${NAME}
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    ${CLUSTER_LABEL_KEY}: ${CLUSTER_LABEL_VALUE}
type: Opaque
stringData:
  name: ${NAME}
  server: https://k3d-${NAME}-server-0:6443
  config: |
    {
      "bearerToken": "${TOKEN}",
      "tlsClientConfig": { "insecure": true }
    }
EOF

log "Cluster '${NAME}' registered with ArgoCD."
