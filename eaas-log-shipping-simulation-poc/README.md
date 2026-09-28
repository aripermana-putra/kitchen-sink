# EaaS Log Shipping Simulation — Sandbox PoC

Validates that a shared Filebeat shipper (DaemonSet, not sidecar-per-pod), configured per EaaS's
documented conventions, can correctly ship UCP's real log shapes — a generic Go service's
structured JSON, Temporal Server's, Temporal Worker's, and Crossplane's own log output — into a
pipeline shaped like EaaS's own (Logstash → Kafka → Elasticsearch → Kibana), using
locally-simulated stand-ins for that pipeline rather than the real EaaS service.

Docs: `docs/projects/universal-control-plane-ucp/pocs/eaas-log-shipping-simulation/` in the
documentation repo — start with `design.md`, then `implementation.md` for what was actually run,
then `poc-report.md` for the verdict. This directory holds only the manifests/configs used to
run the PoC.

## Two tracks, two shippers, four log groups

| Track | Where | Shipper | Log groups covered |
|---|---|---|---|
| GKE | `ucp-agent-cluster` sandbox cluster, namespace `eaas-poc` | 1 Filebeat DaemonSet | `sample-go-service` |
| Colima | `default` profile (pre-existing k3s cluster, kubectl context `colima`) | 1 Filebeat DaemonSet | `temporal-server`, `temporal-worker`, `crossplane-core` |

Both tracks ship to a Logstash `beats` input; the GKE track's stand-in pipeline runs in-cluster
(no Kafka), the Colima track reaches the `eaas-standin` docker-compose stack below (full
Logstash → Kafka → Elasticsearch → Kibana). See `implementation.md`'s "Deviations from
design.md" for why the shapes differ slightly from the original design.

## Sandbox cluster and local environment

| | |
|---|---|
| **GKE project** | `sub-gcp-ucp-clsd-sandbox` |
| **GKE cluster** | `ucp-agent-cluster` (region `asia-northeast1`), namespace `eaas-poc` |
| **Colima profile hosting the stand-in pipeline** | `ucp-crossplane` (4GB/2CPU) |
| **Colima profile hosting Temporal + Crossplane** | `default` (8GB/4CPU) — a pre-existing, already-running local dev cluster, not something this PoC created |

## Prerequisites

```sh
export PATH="/opt/homebrew/share/google-cloud-sdk/bin:$PATH"   # gke-gcloud-auth-plugin
gcloud container clusters get-credentials ucp-agent-cluster --region=asia-northeast1 --project=sub-gcp-ucp-clsd-sandbox

colima start ucp-crossplane   # hosts the eaas-standin docker-compose stack
colima start default          # the pre-existing Temporal/Crossplane cluster (context: colima)
```

## Files

| File | Purpose |
|------|---------|
| `eaas-standin/docker-compose.yml` | Local stand-in pipeline: Logstash + Kafka (`apache/kafka:3.7.0`, KRaft mode) + Elasticsearch + Kibana (7.17.18) |
| `eaas-standin/logstash/logstash.yml`, `eaas-standin/logstash/pipeline/eaas-standin.conf` | Logstash config — `beats` input on `:5044`, per-log_group GROK/JSON filters |
| `gke/00-namespace.yaml` | `eaas-poc` namespace on the GKE sandbox cluster |
| `gke/01-sample-app.yaml` | `sample-go-service` — a `busybox` shell loop emitting ADR-007 `slog`-shaped JSON (not a compiled binary; this PoC tests log *shape* ingestion, not app logic) |
| `gke/03-filebeat-daemonset.yaml` | Filebeat DaemonSet for the GKE track (`add_kubernetes_metadata` + RBAC) |
| `gke/04-eaas-standin-configmap.yaml`, `gke/05-eaas-standin-workloads.yaml` | The GKE track's **in-cluster** stand-in pipeline (Elasticsearch + Logstash + Kibana, no Kafka — see `implementation.md` Deviation 2) |
| `colima-k8s/filebeat-daemonset.yaml` | The shared Filebeat DaemonSet for the Colima `default`-profile cluster, covering `temporal-server`/`temporal-worker`/`crossplane-core` via metadata-based routing |

## How to run

```sh
# 1. Local stand-in pipeline (Colima track's target)
cd eaas-standin && docker compose up -d

# 2. GKE track
kubectl apply -f gke/00-namespace.yaml
kubectl apply -f gke/01-sample-app.yaml
kubectl apply -f gke/04-eaas-standin-configmap.yaml -f gke/05-eaas-standin-workloads.yaml
kubectl apply -f gke/03-filebeat-daemonset.yaml

# 3. Colima track (against the already-running `default`-profile cluster)
kubectl --context colima apply -f colima-k8s/filebeat-daemonset.yaml
```

## How to monitor progress

**GKE track**
```sh
kubectl -n eaas-poc get pods -o wide
kubectl -n eaas-poc logs daemonset/filebeat -f
kubectl -n eaas-poc port-forward svc/eaas-standin-kibana 5601:5601   # then open localhost:5601
```

**Colima track**
```sh
kubectl --context colima -n eaas-poc get pods -o wide
kubectl --context colima -n eaas-poc logs daemonset/filebeat -f

# the pipeline itself:
docker compose -f eaas-standin/docker-compose.yml logs -f logstash
curl localhost:9200/_cat/indices?v                 # see eaas_stg-poc_ucp-poc_<log_group>-* indices land
curl "localhost:9200/eaas_stg-poc_ucp-poc_temporal-worker-*/_search?pretty&size=1"
open http://localhost:5601                          # Kibana Discover
```

**Kafka durability check** (Colima track only — the GKE track's in-cluster pipeline omits Kafka)
```sh
docker compose -f eaas-standin/docker-compose.yml exec kafka \
  kafka-run-class.sh kafka.tools.GetOffsetShell --broker-list localhost:9092 --topic eaas-standin-durability
```

## Current state

Both Colima profiles and the GKE resources were left running after this PoC (not torn down) in
case further inspection is wanted — see `implementation.md`'s "Current running state" table for
exactly what's up. Nothing in this directory or the docs repo has been committed; see
`implementation.md`'s "Commit note" for what would need to land.
