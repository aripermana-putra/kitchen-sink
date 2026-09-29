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

## How to run — step by step, with verification after each step

Each step below has an explicit **Verify** command and expected result. Don't move to the next
step until the current one's verification passes — this mirrors how the original PoC run caught
its two real bugs (see [Known gotchas](#known-gotchas-already-fixed-in-these-manifests) below),
by checking at each step rather than applying everything and inspecting at the end.

### Step 1 — Local stand-in pipeline (Colima track's target)

```sh
cd eaas-standin && docker compose up -d
```

**Verify:**
```sh
curl -s localhost:9200/_cluster/health?pretty | grep '"status"'   # expect "status" : "green"
nc -zv localhost 5044                                              # expect: succeeded (Logstash beats input up)
docker compose ps                                                  # expect: all 4 containers "Up"
```
If Elasticsearch/Kafka aren't healthy yet, wait a few seconds and retry — first-boot JVM startup
takes longer than the container "Up" state implies.

### Step 2 — GKE track: namespace + sample workload

```sh
kubectl apply -f gke/00-namespace.yaml
kubectl apply -f gke/01-sample-app.yaml
```

**Verify:**
```sh
kubectl -n eaas-poc get pods -l app=sample-go-service        # expect: Running, 1/1
kubectl -n eaas-poc logs -l app=sample-go-service --tail=3   # expect: JSON lines like
#   {"level":"info","ts":"...","msg":"handled request","request_id":"req-N","user_id":"user-42",...}
```

### Step 3 — GKE track: in-cluster EaaS stand-in (no tunnel — see Known gotchas)

```sh
kubectl apply -f gke/04-eaas-standin-configmap.yaml -f gke/05-eaas-standin-workloads.yaml
```

**Verify:**
```sh
kubectl -n eaas-poc get pods -l 'app in (eaas-standin-es,eaas-standin-logstash,eaas-standin-kibana)'
# expect: all three Running, 1/1

kubectl -n eaas-poc port-forward svc/eaas-standin-es 9200:9200 &
curl -s localhost:9200/_cluster/health?pretty | grep '"status"'   # expect "status" : "green"
kill %1   # stop the port-forward
```

### Step 4 — GKE track: Filebeat DaemonSet

```sh
kubectl apply -f gke/03-filebeat-daemonset.yaml
```

**Verify:**
```sh
kubectl -n eaas-poc get pods -l app=filebeat -o wide
# expect: one pod per system-pool node, all Running, 0 restarts

kubectl -n eaas-poc logs daemonset/filebeat --tail=20 | grep -iE "error|established"
# expect: "Connection to backoff(...) established" (or similar) — no repeated "connect: connection refused"
```
If a pod shows `CrashLoopBackOff` or RBAC errors in its logs, check
`kubectl -n eaas-poc describe pod <name>` — the most likely cause is the `ClusterRole` not
having applied (verify with `kubectl get clusterrole filebeat-poc`).

### Step 5 — Colima track: confirm the pre-existing cluster and reachability

This PoC does **not** deploy Temporal/Crossplane — it uses whatever is already running in the
`colima` (`default`-profile) cluster. Confirm that first:

```sh
colima start default   # no-op if already running
kubectl --context colima get pods -A | grep -E "temporal|crossplane"
# expect: temporal-system pods (frontend/history/matching/worker/web) and
#         crossplane-system pods (crossplane core + providers) all Running
```

**Verify network reachability from that cluster to the stand-in pipeline** (Colima's `vz` driver
guest gateway routes to the Mac host, which forwards `ucp-crossplane`'s Logstash port):
```sh
kubectl --context colima run nc-test --rm -i --image=busybox --restart=Never -- \
  sh -c "nc -z -w3 192.168.5.2 5044 && echo REACHABLE || echo BLOCKED"
# expect: REACHABLE
```
If `BLOCKED`, confirm `ucp-crossplane`'s Colima profile is running (`colima start ucp-crossplane`)
and that Step 1's `docker compose up -d` succeeded — the gateway IP itself
(`192.168.5.2`) is Colima's own guest-network default, not something to change per-machine
without checking `colima status ucp-crossplane` first.

### Step 6 — Colima track: Filebeat DaemonSet

```sh
kubectl --context colima apply -f colima-k8s/filebeat-daemonset.yaml
```

**Verify:**
```sh
kubectl --context colima -n eaas-poc get pods -l app=filebeat   # expect: Running, 0 restarts
kubectl --context colima -n eaas-poc logs daemonset/filebeat --tail=20 | grep -iE "error|established"
# expect: connection established, no persistent errors
```

### Step 7 — Confirm shipping and parsing, per log group

Give it ~30s for events to flow, then check each of the four log groups lands with the right
fields — **not** just that data exists, but that it's field-parsed and distinguishable:

```sh
# sample-go-service (GKE, via the in-cluster stand-in — port-forward first)
kubectl -n eaas-poc port-forward svc/eaas-standin-es 9200:9200 &
curl -s "localhost:9200/eaas_stg-poc_ucp-poc_sample-go-service-*/_search?pretty&size=1" | grep -E "level|request_id|user_id"
kill %1

# temporal-server / temporal-worker / crossplane-core (Colima track, via eaas-standin ES on :9200)
curl -s "localhost:9200/eaas_stg-poc_ucp-poc_temporal-server-*/_search?pretty&size=1" | grep -E "level|app_msg"
curl -s "localhost:9200/eaas_stg-poc_ucp-poc_temporal-worker-*/_search?pretty&size=1" | grep -E "level|worker_context"
curl -s "localhost:9200/eaas_stg-poc_ucp-poc_crossplane-core-*/_search?pretty&size=1" | grep -E "level|app_msg"
```

**Expected fields per log group** (real examples — see `poc-report.md`'s "Evidence, per log
group" for the full picture):

| Log group | Expect to see |
|---|---|
| `sample-go-service` | `level`, `request_id`, `user_id`, `app_msg` as separate fields |
| `temporal-server` | `level`, `app_msg`; `temporal_error`/`temporal_service` present only on lines that have them (never a literal `%{...}` placeholder — if you see one, the Logstash existence-guard broke) |
| `temporal-worker` | `level`, `app_msg`, and a `worker_context` field holding the free-form SDK key/value tail (not split further — this is intentional; see the docs repo's `pocs/eaas-log-shipping-simulation/implementation.md`) |
| `crossplane-core` | `level`, `app_msg`; occasional docs tagged `crossplane_nonjson_line` for the mixed-in plain-text deprecation warnings — expected, not a bug |

**Also confirm log-group isolation** — a routing bug would misfile one component's logs under
another's group:
```sh
curl -s "localhost:9200/_cat/indices/eaas_stg-poc_*?v"
# expect exactly 4 indices, one per log group, all with doc counts > 0 and growing
```

**Kafka durability check** (Colima track only — the GKE track's in-cluster pipeline omits Kafka):
```sh
docker compose -f eaas-standin/docker-compose.yml exec kafka \
  kafka-run-class.sh kafka.tools.GetOffsetShell --broker-list localhost:9092 --topic eaas-standin-durability
# expect: a non-zero, growing offset
```

### Step 8 — Browse in Kibana (optional, visual confirmation)

```sh
open http://localhost:5601   # eaas-standin Kibana — Colima-track log groups
# or, for the GKE track's in-cluster Kibana:
kubectl -n eaas-poc port-forward svc/eaas-standin-kibana 5601:5601 &
```
Create/select an index pattern matching `eaas_stg-poc_*` and confirm all four log groups appear
under Discover, filterable by `geap_log_group`.

## Known gotchas (already fixed in these manifests)

Two real bugs were found and fixed while building this PoC — the manifests here already have the
fixes, but they're worth knowing about if you're adapting this for a real EaaS onboarding:

1. **Unscoped `container` input harvests every pod's logs on the node.** An early version used
   `paths: ["/var/log/containers/*.log"]` with no scoping — Filebeat picked up every unrelated
   pod's historical backlog too, and unmatched events got a literal unresolved `%{...}` token in
   the index name instead of failing loudly. Fixed by scoping `paths` to the target
   namespace/pod glob (see both DaemonSet ConfigMaps) plus an explicit `else: drop_event: ~`.
2. **Filebeat's `add_fields` with a dotted-string `target` doesn't nest.** `target: "fields.geap"`
   produces a literal top-level key named `"fields.geap"`, not a nested `fields.geap` object —
   silently breaking any Logstash reference to `[fields][geap][...]`. Fixed by using
   `target: fields` with a properly nested `fields: {geap: {...}}` mapping in one call (see any
   `add_fields` block in the DaemonSet ConfigMaps for the correct shape).

Also: `cloudflared`/`ngrok` tunnels for the GKE→local bridge (design.md's original Phase 2 plan)
both failed in this environment (no ngrok account; `cloudflared access tcp` gave `websocket: bad
handshake`) — that's why the GKE track runs its stand-in pipeline in-cluster instead. If you have
a working tunnel setup, you could restore the original design and skip Step 3's in-cluster
pipeline, pointing the GKE Filebeat DaemonSet at the `eaas-standin` stack directly instead.

## Teardown

```sh
# GKE track
kubectl delete namespace eaas-poc

# Colima track's Filebeat only (the pre-existing Temporal/Crossplane cluster is not this PoC's to tear down)
kubectl --context colima delete namespace eaas-poc

# Local stand-in pipeline
cd eaas-standin && docker compose down -v

# Colima profiles, only if nothing else needs them running
colima stop ucp-crossplane
# colima stop default   # leave running — this is a shared local dev cluster, not this PoC's own
```

## Current state

Both Colima profiles and the GKE resources were left running after this PoC (not torn down) in
case further inspection is wanted — see `implementation.md`'s "Current running state" table for
exactly what's up. Nothing in this directory or the docs repo has been committed; see
`implementation.md`'s "Commit note" for what would need to land.
