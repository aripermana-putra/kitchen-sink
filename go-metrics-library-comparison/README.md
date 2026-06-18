# Go Metrics Library Comparison

Four identical apps — same endpoint, same business logic, different metrics libraries.

| # | Library | `/metrics` | Timing type | p99 aggregatable? |
|---|---------|-----------|-------------|-------------------|
| 01 | `prometheus/client_golang` | on app `:8081` | **Histogram** | Yes |
| 02 | `go.opentelemetry.io/otel` | OTel Collector `:8889` | **Histogram** | Yes (needs custom buckets) |
| 03 | `hashicorp/go-metrics` | on app `:8083` | **Summary** | No |
| 04 | `VictoriaMetrics/metrics` | on app `:8084` | **Histogram** (`vmrange`) | No (Prometheus incompatible) |

Each app exposes:
- `POST /workflow/submit` — random 50–200ms latency, 10% error rate
- `GET /metrics` — except 02 which pushes to OTel Collector
- `GET /health`

## Prerequisites

- Docker + docker compose
- Colima running with kube-prometheus-stack already deployed in the `monitoring` namespace
- `kubectl` configured to the Colima cluster

## Setup

**1. Start the apps and OTel Collector:**
```sh
make up
```

This starts 4 app containers + 1 OTel Collector container via docker-compose. No Prometheus or Grafana — those use the existing ones in your Colima cluster.

**2. Register scrape targets with the existing Prometheus:**
```sh
make scrape-apply
```

This applies a Kubernetes Secret (`additionalScrapeConfigs`) that tells kube-prometheus-stack to scrape the 4 apps via `host.docker.internal`.

**3. Port-forward Prometheus and Grafana:**
```sh
kubectl port-forward -n monitoring svc/monitoring-kube-prometheus-prometheus 9090:9090
kubectl port-forward -n monitoring svc/monitoring-grafana 3001:80
```

- Prometheus: http://localhost:9090
- Grafana: http://admin:admin@localhost:3001

**4. Send load:**
```sh
make load   # 500 requests to each app in parallel
```

## Key observation — Histogram vs Summary

After sending load, compare the raw `/metrics` output:

```sh
make metrics-01   # shows _bucket{le=...} lines — aggregatable
make metrics-03   # shows {quantile=...} lines — per-pod only
```

```
# 01 — Histogram (safe to aggregate across pods):
workflow_submission_duration_seconds_bucket{le="0.1"}  47
workflow_submission_duration_seconds_bucket{le="0.25"} 89

# 03 — Summary (cannot aggregate across pods):
workflow_submission_duration_seconds{quantile="0.5"}  0.112
workflow_submission_duration_seconds{quantile="0.99"} 0.198
```

With multiple replicas of app-03, Prometheus scrapes separate Summary streams per pod — there is no PromQL expression that gives you p99 across all pods. With Histograms (01, 02, 04):
```
histogram_quantile(0.99, sum by (le) (rate(workflow_submission_duration_seconds_bucket[1m])))
```

## Four Golden Signals queries

**Latency p99**
```promql
# 01
histogram_quantile(0.99, sum by (le) (rate(workflow_submission_duration_seconds_bucket{job="kitchen-sink/01-prometheus-direct"}[1m])))
# 02
histogram_quantile(0.99, sum by (le) (rate(otel_workflow_submission_duration_seconds_bucket{exported_job="02-otel-sdk"}[1m])))
# 03 — average only (no p99 possible)
sum(rate(workflow_submission_duration_seconds_sum{job="kitchen-sink/03-hashicorp"}[1m])) / sum(rate(workflow_submission_duration_seconds_count{job="kitchen-sink/03-hashicorp"}[1m]))
# 04 — average only (vmrange incompatible with Prometheus histogram_quantile)
sum(rate(workflow_submission_duration_seconds_sum{job="kitchen-sink/04-victoriametrics"}[1m])) / sum(rate(workflow_submission_duration_seconds_count{job="kitchen-sink/04-victoriametrics"}[1m]))
```

**Traffic (req/s)**
```promql
sum by (job) (rate(workflow_submissions_total[1m]))
# 02 (separate query)
sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk"}[1m]))
```

**Errors (%)**
```promql
# 01
sum(rate(workflow_submissions_total{job="kitchen-sink/01-prometheus-direct", status="error"}[1m])) / sum(rate(workflow_submissions_total{job="kitchen-sink/01-prometheus-direct"}[1m])) * 100
# 02
sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk", status="error"}[1m])) / sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk"}[1m])) * 100
# 03 (status baked into metric name)
sum(rate(workflow_submissions_total_error{job="kitchen-sink/03-hashicorp"}[1m])) / (sum(rate(workflow_submissions_total_error{job="kitchen-sink/03-hashicorp"}[1m])) + sum(rate(workflow_submissions_total_ok{job="kitchen-sink/03-hashicorp"}[1m]))) * 100
# 04
sum(rate(workflow_submissions_total{job="kitchen-sink/04-victoriametrics", status="error"}[1m])) / sum(rate(workflow_submissions_total{job="kitchen-sink/04-victoriametrics"}[1m])) * 100
```

**Saturation (active workflows)**
```promql
workflow_active_total
# 02 (separate query)
otel_workflow_active_total{exported_job="02-otel-sdk"}
```

## Teardown

```sh
make down           # stop docker-compose containers
make scrape-delete  # remove scrape config from K8s Prometheus
```

## Structure

```
.
├── 01-prometheus-direct/   # prometheus/client_golang, pull model
├── 02-otel-sdk/            # OTel SDK, push to Collector, needs custom histogram buckets
├── 03-hashicorp/           # hashicorp/go-metrics, Summary timing, no p99 across pods
├── 04-victoriametrics/     # VictoriaMetrics/metrics, vmrange histogram, Prometheus incompatible
├── deploy/
│   ├── docker-compose.yml         # runs the 4 apps + OTel Collector
│   ├── otel-collector-config.yaml # Collector config: OTLP in, Prometheus out
│   └── k8s/
│       └── additional-scrape-configs.yaml  # patches existing kube-prometheus-stack
└── Makefile
```
