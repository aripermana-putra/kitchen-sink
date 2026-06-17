// 04-victoriametrics: uses VictoriaMetrics/metrics — a minimal, zero-alloc
// metrics library built on top of net/http (no fasthttp dependency needed).
//
// Pros: extremely low overhead, simple API, Histogram (not Summary).
// Cons: smaller ecosystem, less feature-rich than prometheus/client_golang.
//       Labels must be embedded in the metric name string (no label maps).
//
// /metrics is served natively by the library via WritePrometheus.
package main

import (
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	metrics "github.com/VictoriaMetrics/metrics"
)

var activeWorkflows int64

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		// WritePrometheus writes all registered metrics in Prometheus text format.
		metrics.WritePrometheus(w, true)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/workflow/submit", submitHandler)

	// VictoriaMetrics/metrics registers a gauge via a callback — called each scrape.
	metrics.NewGauge("workflow_active_total", func() float64 {
		return float64(atomic.LoadInt64(&activeWorkflows))
	})

	log.Println("04-victoriametrics listening on :8084")
	log.Fatal(http.ListenAndServe(":8084", mux))
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	atomic.AddInt64(&activeWorkflows, 1)
	defer atomic.AddInt64(&activeWorkflows, -1)

	time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)
	dur := time.Since(start).Seconds()

	// Labels are embedded in the metric name string — different style from other libs.
	// GetOrCreateHistogram returns a Histogram (not Summary) — safe to aggregate.
	metrics.GetOrCreateHistogram("workflow_submission_duration_seconds").Update(dur)

	if rand.Float64() < 0.1 {
		metrics.GetOrCreateCounter(`workflow_submissions_total{status="error"}`).Inc()
		http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
		return
	}

	metrics.GetOrCreateCounter(`workflow_submissions_total{status="ok"}`).Inc()
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}
