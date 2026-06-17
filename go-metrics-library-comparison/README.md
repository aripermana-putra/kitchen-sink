# Go Metrics Library Comparison

Four identical apps — same endpoint, same business logic, different metrics libraries.

| # | Library | `/metrics` | Timing type | Multi-pod safe? |
|---|---------|-----------|-------------|-----------------|
| 01 | `prometheus/client_golang` | on app | **Histogram** | Yes |
| 02 | `go.opentelemetry.io/otel` | on Collector | **Histogram** | Yes |
| 03 | `hashicorp/go-metrics` | on app | **Summary** | **No** |
| 04 | `VictoriaMetrics/metrics` | on app | **Histogram** | Yes |

Each app exposes:
- `POST /workflow/submit` — random 50–200 ms latency, 10% error rate
- `GET /metrics` — except 02 which pushes to OTel Collector
- `GET /health`

## Quickstart (docker-compose)

```sh
make up          # build + start everything
make load        # send 200 test requests across all 4 apps
make metrics-01  # check Histogram output (bucket lines)
make metrics-03  # check Summary output (quantile lines) — spot the difference
```

- Prometheus: http://localhost:9090
- Grafana:    http://localhost:3000

## Key observation to make

After sending load, compare the `/metrics` output between 01 and 03:

```
# 01 — Histogram (aggregatable across pods):
workflow_submission_duration_seconds_bucket{le="0.05"} 12
workflow_submission_duration_seconds_bucket{le="0.1"}  47
workflow_submission_duration_seconds_bucket{le="0.25"} 89

# 03 — Summary (per-pod only, cannot be summed):
workflow_submission_duration_seconds{quantile="0.5"}  0.112
workflow_submission_duration_seconds{quantile="0.9"}  0.174
workflow_submission_duration_seconds{quantile="0.99"} 0.198
```

With 2+ replicas of app-03, Prometheus scrapes two separate Summary streams.
There is no PromQL expression that gives you "the p99 across all pods" from Summaries.
With Histograms (01, 02, 04) you can: `histogram_quantile(0.99, sum by (le) (rate(…_bucket[5m])))`.

## K8s / Colima

```sh
make k8s-up      # build images, load into Colima, apply manifests
make k8s-status  # watch pods
make k8s-down    # teardown
```

Each app is deployed with 2 replicas so the Summary limitation of 03 is immediately visible.

## Structure

```
.
├── 01-prometheus-direct/   # prometheus/client_golang, pull model
├── 02-otel-sdk/            # OTel SDK, push to Collector
├── 03-hashicorp/           # hashicorp/go-metrics, pull model, Summary timing
├── 04-victoriametrics/     # VictoriaMetrics/metrics, pull model, simple API
├── deploy/
│   ├── docker-compose.yml
│   ├── prometheus.yml
│   ├── otel-collector-config.yaml
│   └── k8s/
└── Makefile
```
