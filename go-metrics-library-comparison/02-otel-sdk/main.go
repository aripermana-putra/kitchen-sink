// 02-otel-sdk: uses OpenTelemetry SDK with OTLP gRPC exporter.
// Pushes metrics to OTel Collector → Prometheus scrapes the Collector.
// No /metrics endpoint on the app itself — Collector owns that.
// This is the recommended path for UCP (vendor-neutral, works with MonaaS).
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
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var activeWorkflows int64

func main() {
	ctx := context.Background()

	// OTEL_EXPORTER_OTLP_ENDPOINT can override the default grpc://localhost:4317.
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		log.Fatalf("create otlp exporter: %v", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(15*time.Second)),
		),
	)
	defer func() {
		if err := provider.Shutdown(ctx); err != nil {
			log.Printf("shutdown meter provider: %v", err)
		}
	}()
	otel.SetMeterProvider(provider)

	meter := otel.Meter("kitchen-sink/02-otel-sdk")

	submissionsTotal, err := meter.Int64Counter(
		"workflow.submissions.total",
		metric.WithDescription("Total workflow submit requests."),
	)
	if err != nil {
		log.Fatalf("create counter: %v", err)
	}

	// OTel Histogram — equivalent to Prometheus Histogram, safe to aggregate.
	submissionDuration, err := meter.Float64Histogram(
		"workflow.submission.duration",
		metric.WithDescription("Duration of workflow submit handler."),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Fatalf("create histogram: %v", err)
	}

	// Observable gauge reads atomic on each collection cycle.
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

	log.Println("02-otel-sdk listening on :8082 (metrics pushed to OTel Collector)")
	log.Fatal(http.ListenAndServe(":8082", mux))
}
