// 05-otel-sdk-pull: uses OpenTelemetry SDK with the Prometheus exporter.
// Exposes /metrics directly on the app — no OTel Collector needed.
// This is the pull model equivalent of 02-otel-sdk.
//
// Difference vs 02:
//   - 02 pushes via OTLP gRPC → Collector → Prometheus scrapes Collector
//   - 05 registers with a Prometheus registry → Prometheus scrapes the app directly
//
// When to use this over 02:
//   - You want OTel's vendor-neutral API in code
//   - But don't want to run an OTel Collector
//   - MonaaS/Prometheus scrapes your app directly (simpler infra)
package main

import (
	"context"
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var activeWorkflows int64

func main() {
	ctx := context.Background()

	// Prometheus exporter bridges OTel SDK → Prometheus registry.
	// No Collector, no OTLP — metrics are served directly at /metrics.
	reg := prometheus.NewRegistry()
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(reg))
	if err != nil {
		log.Fatalf("create prometheus exporter: %v", err)
	}

	// Same custom bucket boundaries as 02 — required for accurate p99.
	latencyView := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "workflow.submission.duration"},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
			},
		},
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(latencyView),
	)
	defer func() {
		if err := provider.Shutdown(ctx); err != nil {
			log.Printf("shutdown meter provider: %v", err)
		}
	}()
	otel.SetMeterProvider(provider)

	meter := otel.Meter("kitchen-sink/05-otel-sdk-pull")

	submissionsTotal, err := meter.Int64Counter(
		"workflow.submissions.total",
		metric.WithDescription("Total workflow submit requests."),
	)
	if err != nil {
		log.Fatalf("create counter: %v", err)
	}

	submissionDuration, err := meter.Float64Histogram(
		"workflow.submission.duration",
		metric.WithDescription("Duration of workflow submit handler."),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Fatalf("create histogram: %v", err)
	}

	_, err = meter.Int64ObservableGauge(
		"workflow.active.total",
		metric.WithDescription("Workflows currently being processed."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(atomic.LoadInt64(&activeWorkflows))
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("create gauge: %v", err)
	}

	mux := http.NewServeMux()

	// /metrics served directly — Prometheus scrapes this, no Collector needed.
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/workflow/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		start := time.Now()
		atomic.AddInt64(&activeWorkflows, 1)
		defer atomic.AddInt64(&activeWorkflows, -1)

		time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)
		dur := time.Since(start).Seconds()

		if rand.Float64() < 0.1 {
			submissionsTotal.Add(r.Context(), 1, metric.WithAttributes(attribute.String("status", "error")))
			submissionDuration.Record(r.Context(), dur, metric.WithAttributes(attribute.String("status", "error")))
			http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
			return
		}

		submissionsTotal.Add(r.Context(), 1, metric.WithAttributes(attribute.String("status", "ok")))
		submissionDuration.Record(r.Context(), dur, metric.WithAttributes(attribute.String("status", "ok")))
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted"}`))
	})

	log.Println("05-otel-sdk-pull listening on :8085")
	log.Println("Metrics exposed at /metrics (pull model, no Collector needed)")
	log.Fatal(http.ListenAndServe(":8085", mux))
}
