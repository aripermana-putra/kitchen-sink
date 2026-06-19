# Go Metrics Library Comparison

Five identical apps — same endpoint, same business logic, different metrics libraries.
Test setup: 500 requests per app, 50–200ms random latency, 10% error rate.

| # | Library | `/metrics` | Timing type | Observed p99 | Observed avg | p99 PromQL | Multi-pod p99 | Extra infra | Data loss risk |
|---|---------|-----------|-------------|-------------|-------------|-----------|--------------|-------------|----------------|
| 01 | `prometheus/client_golang` | on app `:8081` | Histogram (`le=`) | ~0.247s ✅ | ~0.125s | ✅ Works | ✅ `sum by (le)` | None | None (pull) |
| 02 | OTel SDK push | OTel Collector `:8889` | Histogram (`le=`) | ~0.247s ✅ | ~0.125s | ✅ Works | ✅ `sum by (le)` | OTel Collector | Up to 15s |
| 03 | `hashicorp/go-metrics` | on app `:8083` | Summary (quantile=) | N/A ❌ | ~0.124s | ❌ No buckets | ❌ Impossible | None | None (pull) |
| 04 | `VictoriaMetrics/metrics` | on app `:8084` | Histogram (`vmrange=`) | N/A ❌ | ~0.125s | ❌ `le` missing | ❌ Impossible | None | None (pull) |
| 05 | OTel SDK pull | on app `:8085` | Histogram (`le=`) | ~0.247s ✅ | ~0.125s | ✅ Works | ✅ `sum by (le)` | None | None (pull) |

**Notes:**
- 02 p99 requires custom histogram boundaries — OTel default boundaries (`0, 5, 10...` seconds) put all HTTP handler latency into `le="5"`, returning a flat 5.0s with no resolution
- 03 Summary quantiles return `NaN` on sparse traffic; under load they populate but remain per-pod only
- 04 `vmrange=` labels are incompatible with Prometheus `histogram_quantile` — works only if VictoriaMetrics is the TSDB
- 05 = same OTel SDK as 02 but with Prometheus exporter bridge instead of OTLP push — no Collector needed

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

Starts 5 app containers + 1 OTel Collector via docker-compose. No Prometheus or Grafana — uses the existing ones in your Colima cluster.

**2. Register scrape targets with the existing Prometheus:**
```sh
make scrape-apply
```

Applies a Kubernetes Secret (`additionalScrapeConfigs`) that tells kube-prometheus-stack to scrape all 5 apps via `host.docker.internal`.

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
workflow_submission_duration_seconds{quantile="0.5"}  NaN
workflow_submission_duration_seconds{quantile="0.99"} NaN
```

With multiple replicas of app-03, Prometheus scrapes separate Summary streams per pod — there is no PromQL expression that gives you p99 across all pods. With Histograms (01, 02, 04, 05):
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
# 05
histogram_quantile(0.99, sum by (le) (rate(workflow_submission_duration_seconds_bucket{job="kitchen-sink/05-otel-sdk-pull"}[1m])))
```

**Traffic (req/s)**
```promql
sum by (job) (rate(workflow_submissions_total[1m]))
# 02 (separate query — otel_ prefix + exported_job label)
sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk"}[1m]))
```

**Errors (%)**
```promql
# 01
sum(rate(workflow_submissions_total{job="kitchen-sink/01-prometheus-direct", status="error"}[1m])) / sum(rate(workflow_submissions_total{job="kitchen-sink/01-prometheus-direct"}[1m])) * 100
# 02
sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk", status="error"}[1m])) / sum(rate(otel_workflow_submissions_total{exported_job="02-otel-sdk"}[1m])) * 100
# 03 (status baked into metric name suffix, not label)
sum(rate(workflow_submissions_total_error{job="kitchen-sink/03-hashicorp"}[1m])) / (sum(rate(workflow_submissions_total_error{job="kitchen-sink/03-hashicorp"}[1m])) + sum(rate(workflow_submissions_total_ok{job="kitchen-sink/03-hashicorp"}[1m]))) * 100
# 04
sum(rate(workflow_submissions_total{job="kitchen-sink/04-victoriametrics", status="error"}[1m])) / sum(rate(workflow_submissions_total{job="kitchen-sink/04-victoriametrics"}[1m])) * 100
# 05
sum(rate(workflow_submissions_total{job="kitchen-sink/05-otel-sdk-pull", status="error"}[1m])) / sum(rate(workflow_submissions_total{job="kitchen-sink/05-otel-sdk-pull"}[1m])) * 100
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
├── 02-otel-sdk/            # OTel SDK push, requires Collector + custom histogram buckets
├── 03-hashicorp/           # hashicorp/go-metrics, Summary timing, no p99 across pods
├── 04-victoriametrics/     # VictoriaMetrics/metrics, vmrange incompatible with Prometheus
├── 05-otel-sdk-pull/       # OTel SDK pull, Prometheus exporter bridge, no Collector needed
├── deploy/
│   ├── docker-compose.yml              # runs all 5 apps + OTel Collector
│   ├── otel-collector-config.yaml      # Collector: OTLP in, Prometheus out
│   └── k8s/
│       └── additional-scrape-configs.yaml  # patches existing kube-prometheus-stack
└── Makefile
```
