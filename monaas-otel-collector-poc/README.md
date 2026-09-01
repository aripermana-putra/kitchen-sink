# MonaaS OTel Collector Variant — Sandbox PoC

Measures Option B's operational overhead (component count, manifest count, wall-clock setup
time) for GKE resource metrics + a custom application metric, remote-writing to an in-cluster
Grafana Mimir standing in for MonaaS's Cortex backend.

Docs: `docs/projects/universal-control-plane-ucp/pocs/monaas-otel-collector/` in the
documentation repo. This directory holds only the manifests/scripts used to run the PoC.

## Sandbox cluster

| | |
|---|---|
| Project | `sub-gcp-ucp-clsd-sandbox` |
| Cluster | `ucp-agent-cluster` (region `asia-northeast1`) |
| Node pools | `system-pool` (e2-small) — real workloads + sample app + DaemonSet Collector; `spot-pool` (n2-standard-2, taint `workload=spot:NoSchedule`) — Deployment Collector + Mimir |

## Prerequisites

```sh
export PATH="/opt/homebrew/share/google-cloud-sdk/bin:$PATH"   # gke-gcloud-auth-plugin
gcloud container clusters get-credentials ucp-agent-cluster --region=asia-northeast1 --project=sub-gcp-ucp-clsd-sandbox
```

**Corporate proxy SSL fix.** `gcloud container ...` commands fail with
`SSLCertVerificationError: Basic Constraints of CA cert not marked critical` on this network —
gcloud's bundled Python/OpenSSL rejects the proxy's CA cert extension where macOS's own TLS
stack accepts it.

Temporary (current shell only):
```sh
export CLOUDSDK_PYTHON=/usr/bin/python3
```

Permanent (add to `~/.zshrc`):
```sh
echo 'export CLOUDSDK_PYTHON=/usr/bin/python3' >> ~/.zshrc
source ~/.zshrc
```

## Manifests

| File | Purpose |
|------|---------|
| `deploy/k8s/01-mimir.yaml` | `monitoring` namespace, Mimir monolithic-mode Deployment + Service, scheduled on `spot-pool` |

## How to monitor progress

**Nodes / pools**
```sh
kubectl get nodes -o wide                  # all nodes
kubectl get nodes -l workload=spot          # just spot-pool
```

**Everything in the `monitoring` namespace (Mimir, later the Collectors)**
```sh
kubectl -n monitoring get all
kubectl -n monitoring get pods -o wide       # which node each pod landed on
kubectl -n monitoring logs deploy/mimir -f   # live logs
```

**Watch things come up in real time**
```sh
kubectl get pods -A -w
```

**Once the sample app / OTel Collectors are deployed** (later phases), same pattern:
```sh
kubectl -n <namespace> get pods -o wide
kubectl -n <namespace> logs <pod-name> -f
kubectl -n <namespace> describe pod <pod-name>   # for scheduling/RBAC errors
```

**Port-forward to poke at Mimir from your laptop**
```sh
kubectl -n monitoring port-forward svc/mimir 8080:8080
curl "localhost:8080/prometheus/api/v1/query?query=up"
```
