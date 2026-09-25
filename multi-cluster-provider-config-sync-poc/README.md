# Multi-Cluster ProviderConfig Sync PoC (MCUCP-306)

Validates whether an ArgoCD `ApplicationSet` matrix generator (cluster generator ×
tenant registry generator) automatically extends `ExternalSecret` + `ProviderConfig`
pairs to a newly added Crossplane cluster, with no new Git commit and no UCP backend
involvement.

Design doc: `docs/projects/universal-control-plane-ucp/pocs/multi-cluster-provider-config-sync/design.md`
in the `ucp-docs-collection` repo.

GitOps source repo (chart + `ApplicationSet`s + tenant registry): `https://ghe.rakuten-it.com/clsd-ucp/mcucp-306-provider-config-gitops`

## Environment actually used

The design called for 3 separate Colima VMs (1 ArgoCD hub + 2 Crossplane clusters),
each with its own `--kubernetes` k3s. **That does not work**: macOS's Virtualization.framework
shared vmnet (`--network-address`) assigns each VM a routable IP but isolates
VM-to-VM traffic (only VM↔host works) — client isolation, similar to Wi-Fi AP
isolation. Bridged mode (`--network-mode bridged`) also failed to get a DHCP lease
on this corporate network.

**What works:** one Colima VM running Docker, with [k3d](https://k3d.io) creating
each "cluster" as a set of containers on a single shared Docker network
(`mcucp-poc-net`). Containers on the same Docker network reach each other by
container name — no VM networking involved at all.

```
Colima VM "mcucp-poc" (docker only, 4 CPU / 8GB)
└── Docker network: mcucp-poc-net
    ├── k3d cluster "argocd-hub"     (context: k3d-argocd-hub)
    ├── k3d cluster "crossplane-1"   (context: k3d-crossplane-1)
    └── k3d cluster "crossplane-2"   (context: k3d-crossplane-2, added mid-PoC)
```

ArgoCD runs in `argocd-hub` and reaches the other two clusters' API servers at
`https://k3d-<cluster>-server-0:6443` (the k3d server container's name on the
shared Docker network).

## Prerequisites

- `colima`, `k3d`, `argocd` CLI, `helm`, `kubectl` (all via `brew install`)
- `gcloud` authenticated against `sub-gcp-ucp-clsd-sandbox` (for the GCP Secret
  Manager side — see [Known blocker](#known-blocker-gcloud-ssl) below)
- Write access to `ghe.rakuten-it.com/clsd-ucp/mcucp-306-provider-config-gitops`

## Setup — from scratch

### 1. Start the Colima VM

```sh
colima start --profile mcucp-poc --cpu 4 --memory 8 --disk 40
docker context use colima-mcucp-poc
```

If other Colima profiles are already running, free memory first — `docker+k3s`
profiles are ~3GB+ each and this one wants 8GB:

```sh
colima list
colima stop --profile <unrelated-profile>
```

### 2. Create the shared Docker network and the hub + Cluster 1

```sh
docker network create mcucp-poc-net

k3d cluster create argocd-hub --network mcucp-poc-net --api-port 6550 \
  -p "8080:80@loadbalancer" --wait --timeout 120s

k3d cluster create crossplane-1 --network mcucp-poc-net --api-port 6551 \
  --wait --timeout 180s
```

If cluster creation fails with `inotify_init: too many open files` in
`docker logs k3d-<name>-server-0`, the Colima VM's inotify limits are too low for
running multiple k3s clusters. Fix inside the VM and restart Colima once:

```sh
colima ssh --profile mcucp-poc -- sudo sh -c "
  sysctl -w fs.inotify.max_user_instances=8192
  echo 'fs.inotify.max_user_instances=8192' >> /etc/sysctl.conf
  mkdir -p /etc/systemd/system.conf.d
  printf '[Manager]\nDefaultLimitNOFILE=1048576\n' > /etc/systemd/system.conf.d/limits.conf
"
colima stop --profile mcucp-poc
colima start --profile mcucp-poc --cpu 4 --memory 8 --disk 40
```

k3d clusters restart automatically with the VM (Docker's own restart policy) —
no need to recreate them after a Colima restart.

### 3. Install ArgoCD in the hub cluster

The stock ArgoCD install manifest's `applicationsets.argoproj.io` CRD has a
`last-applied-configuration` annotation over 262144 bytes — plain `kubectl apply`
rejects it. Use server-side apply:

```sh
kubectl --context k3d-argocd-hub create namespace argocd
kubectl --context k3d-argocd-hub apply -n argocd \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml \
  --server-side --force-conflicts
kubectl --context k3d-argocd-hub wait --for=condition=Available deployment --all -n argocd --timeout=180s
```

### 4. Log in with the ArgoCD CLI

```sh
kubectl --context k3d-argocd-hub -n argocd port-forward svc/argocd-server 8443:443 &
ARGOCD_PW=$(kubectl --context k3d-argocd-hub -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
argocd login localhost:8443 --username admin --password "$ARGOCD_PW" --insecure
```

Don't print `$ARGOCD_PW` — pipe it straight into `argocd login`.

### 5. Register a Crossplane cluster with ArgoCD

`argocd cluster add <context>` determines the target server address **from the
local kubeconfig's `server:` field**, which for a k3d cluster is
`https://0.0.0.0:<mapped-port>` — a host-only address ArgoCD's pod (running
inside a different k3d cluster's containers) cannot reach. `argocd cluster add`
still does the useful part (creates the `argocd-manager` ServiceAccount + a
long-lived bearer token on the target cluster) before failing on the final
version check — that's fine, the token is what we need:

```sh
argocd cluster add k3d-crossplane-1 --name crossplane-1 --yes
# fails at the end with "dial tcp 0.0.0.0:6551: connection refused" — expected,
# the ServiceAccount + token secret (argocd-manager-long-lived-token in
# kube-system) were already created on crossplane-1 by this point.
```

Then create the ArgoCD cluster `Secret` by hand with the correct in-network
address (the k3d server container's name) and `insecure: true` (the k3s
server's TLS cert has no SAN for the Docker-network hostname):

```sh
TOKEN=$(kubectl --context k3d-crossplane-1 -n kube-system get secret argocd-manager-long-lived-token -o jsonpath='{.data.token}' | base64 -d)
cat <<EOF | kubectl --context k3d-argocd-hub apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: cluster-crossplane-1
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
type: Opaque
stringData:
  name: crossplane-1
  server: https://k3d-crossplane-1-server-0:6443
  config: |
    {
      "bearerToken": "$TOKEN",
      "tlsClientConfig": { "insecure": true }
    }
EOF
```

Don't echo `$TOKEN` to the terminal — build the secret in the same command
substitution that reads it.

Verify with `argocd cluster list` — status should eventually show `Successful`.

### 6. Install Crossplane + provider-gcp on the target cluster

The `ProviderConfig` CRD only exists once `provider-family-gcp` is installed —
`ApplicationSet`-rendered `ProviderConfig` objects fail to sync until this is
done.

```sh
helm repo add crossplane-stable https://charts.crossplane.io/stable
kubectl --context k3d-crossplane-1 create namespace crossplane-system
helm --kube-context k3d-crossplane-1 install crossplane crossplane-stable/crossplane \
  -n crossplane-system --wait --timeout 180s

cat <<'EOF' | kubectl --context k3d-crossplane-1 apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-gcp
spec:
  package: xpkg.upbound.io/upbound/provider-family-gcp:v2.6.0
EOF
```

Wait for `kubectl get providers.pkg.crossplane.io provider-gcp` to report
`HEALTHY=True` before syncing any Application that renders a `ProviderConfig`.

### 7. Apply the platform bootstrap and tenant matrix ApplicationSets

From the `mcucp-306-provider-config-gitops` repo (`gitops/` directory):

```sh
kubectl --context k3d-argocd-hub apply -f gitops/platform/eso-appset.yaml
kubectl --context k3d-argocd-hub apply -f gitops/platform/clustersecretstore-appset.yaml
kubectl --context k3d-argocd-hub apply -f gitops/tenant-matrix-appset.yaml
```

These use the `clusters: {}` generator with no filter, which also matches
ArgoCD's built-in `in-cluster` entry (the hub cluster itself) — harmless for this
PoC (ESO gets installed on the hub too) but worth a label selector if reused
beyond a PoC.

### 8. Register a repo credential for the GitOps repo

ArgoCD needs a credential for the private GHE repo. HTTPS + PAT was simpler than
SSH deploy keys for this PoC:

```sh
kubectl --context k3d-argocd-hub -n argocd create secret generic repo-mcucp-306-gitops \
  --from-literal=type=git \
  --from-literal=url=https://ghe.rakuten-it.com/clsd-ucp/mcucp-306-provider-config-gitops.git \
  --from-literal=username=<github-username> \
  --from-literal=password="$(gh auth token --hostname ghe.rakuten-it.com)"
kubectl --context k3d-argocd-hub -n argocd label secret repo-mcucp-306-gitops \
  "argocd.argoproj.io/secret-type=repository"
```

### 9. Onboard a tenant registration

Add a parameter file to `registry/` in the GitOps repo and push:

```json
{
  "tenant": "acme",
  "registrationId": "acme-proj-123",
  "gcpProjectId": "proj-123",
  "gcpServiceAccountEmail": "acme-proj-123@proj-123.iam.gserviceaccount.com",
  "secretManagerSecretName": "acme-proj-123-config"
}
```

The tenant matrix `ApplicationSet`'s git file generator polls on its own
schedule (~1-3 min) — wait, or `argocd app get <appset-generated-app> --hard-refresh`
after confirming the `Application` exists via `argocd app list`.

### 10. Add a second cluster and confirm backfill — the actual research question

Repeat steps 2 (cluster creation only), 5, and 6 for `crossplane-2`
(`--api-port 6552`). **Do not touch the tenant registry or any `ApplicationSet`.**
Within one matrix-generator reconcile pass, `argocd app list` should show a new
`pc-acme-proj-123-crossplane-2` Application, generated from the exact same
registry entry, with zero new commits.

This is what was observed — see [Result](#result).

## Result

- `pc-acme-proj-123-crossplane-1` and `pc-acme-proj-123-crossplane-2` both exist,
  both rendered from the single `registry/acme-proj-123.json` entry, both
  `Synced`, with a `ProviderConfig` in `Healthy` status on each cluster.
- Cluster 2 required **no new commit and no registry change** — only cluster
  creation + ArgoCD registration + Crossplane/provider-gcp install (the fixed
  per-cluster bootstrap steps, not tenant-specific work).
- This confirms the PoC's hypothesis: the matrix `ApplicationSet` fan-out
  mechanism backfills existing tenant registrations onto newly added clusters
  automatically.

## Known blocker: gcloud SSL

`ExternalSecret` on both clusters is stuck in `SecretSyncedError` — the
`ClusterSecretStore`'s `eso-gcp-sm-key` Secret (a GCP service account key for
ESO's own Secret Manager access) was never created, because `gcloud iam
service-accounts create` fails on this machine with:

```
SSLError(SSLCertVerificationError(1, '[SSL: CERTIFICATE_VERIFY_FAILED]
certificate verify failed: self-signed certificate in certificate chain'))
```

This is a corporate TLS-inspecting proxy injecting a root CA that the
configured `core/custom_ca_certs_file` bundle doesn't validate cleanly under
the current OpenSSL. Not something to route around blindly — needs a fixed CA
bundle or a working `gcloud auth login` from a shell where the corporate proxy
cert chain validates.

**What still needs to happen once gcloud works:**

1. `gcloud iam service-accounts create mcucp-306-eso-poc --project sub-gcp-ucp-clsd-sandbox`
2. Grant it `roles/secretmanager.secretAccessor`, scoped to the PoC's secrets if
   using IAM Conditions.
3. `gcloud iam service-accounts keys create key.json --iam-account=...`
4. `kubectl create secret generic eso-gcp-sm-key -n external-secrets --from-file=key.json` on **both** clusters.
5. Create `sub-gcp-ucp-clsd-sandbox`'s `acme-proj-123-config` secret in GCP Secret
   Manager with the `gcp_project_id` / `gcp_service_account_email` fields the
   `ExternalSecret` template expects.

This is also a documented PoC-only deviation from the design: a **local, non-GKE**
cluster can't use ESO's native GCP Workload Identity auth mode (`workloadIdentity`
in `ClusterSecretStore` requires real GKE node metadata) — same finding the
`wif-gcp` PoC already reached for Crossplane's own provider pod. Production runs
on GKE, where `workloadIdentity` applies normally.

## Also fixed along the way

- **ESO API version**: the installed chart (`external-secrets` 0.10.4) serves
  `ClusterSecretStore`/`ExternalSecret` at `external-secrets.io/v1beta1`, not
  `v1` — the chart templates and platform manifest were written against `v1`
  first and had to be corrected.
- **ArgoCD's cached API resource list** goes stale after installing a new CRD
  (Crossplane/provider-gcp) on a target cluster mid-session — `argocd app get
  <app> --hard-refresh` before re-syncing.

## Directories

- `chart/` — the shared Helm chart rendering `ExternalSecret` + `ProviderConfig`
  per provider-config registration (pushed to the GitOps repo, not duplicated
  here — see the GitOps repo for the current version)
- `gitops/` — the platform bootstrap and tenant matrix `ApplicationSet`s (same —
  canonical copy lives in the GitOps repo)
- `scripts/` — placeholder for teardown/rebuild helper scripts (not yet written)

## Teardown

```sh
k3d cluster delete argocd-hub crossplane-1 crossplane-2
colima stop --profile mcucp-poc
colima delete mcucp-poc --force   # only if not needed again
```
