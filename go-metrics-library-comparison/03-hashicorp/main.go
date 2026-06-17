// 03-hashicorp: uses hashicorp/go-metrics with the Prometheus sink.
//
// KEY LIMITATION: hashicorp/go-metrics emits Summary (not Histogram) for
// timing metrics. Summary quantiles are computed per-pod and CANNOT be
// aggregated across multiple replicas in Prometheus. If you run 3 pods you get
// 3 separate p99 values with no meaningful way to combine them.
// Compare the /metrics output to 01-prometheus-direct to see the difference:
//   - 01: workflow_submission_duration_seconds_bucket{le="0.1"} 42  ← aggregatable
//   - 03: workflow_submission_duration_seconds{quantile="0.99"} 0.18 ← per-pod only
//
// Use this library only if you already depend on it (e.g. via Consul/Vault).
// For new code, prefer 01 or 02.
package main

import (
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	gometrics "github.com/hashicorp/go-metrics"
	prometheussink "github.com/hashicorp/go-metrics/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var activeWorkflows int64

func main() {
	reg := prometheus.NewRegistry()

	sink, err := prometheussink.NewPrometheusSinkFrom(prometheussink.PrometheusOpts{
		Registerer: reg,
	})
	if err != nil {
		log.Fatalf("create prometheus sink: %v", err)
	}

	cfg := gometrics.DefaultConfig("workflow")
	cfg.EnableHostname = false
	if _, err := gometrics.NewGlobal(cfg, sink); err != nil {
		log.Fatalf("init go-metrics: %v", err)
	}

	// Gauge is polled every 5s and sent to the sink.
	go func() {
		for range time.Tick(5 * time.Second) {
			gometrics.SetGauge([]string{"active_total"}, float32(atomic.LoadInt64(&activeWorkflows)))
		}
	}()

	mux := http.NewServeMux()
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

		// MeasureSince emits a Summary in the Prometheus sink.
		// Notice: no Histogram buckets — you lose the aggregation guarantee.
		gometrics.MeasureSince([]string{"submission_duration_seconds"}, start)

		if rand.Float64() < 0.1 {
			gometrics.IncrCounter([]string{"submissions_total", "error"}, 1)
			http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
			return
		}

		gometrics.IncrCounter([]string{"submissions_total", "ok"}, 1)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted"}`))
	})

	log.Println("03-hashicorp listening on :8083")
	log.Println("NOTE: timing metrics are Summary, not Histogram — cannot aggregate across pods")
	log.Fatal(http.ListenAndServe(":8083", mux))
}
