// 01-prometheus-direct: uses prometheus/client_golang directly.
// Exposes /metrics in Prometheus text format.
// This is the baseline approach — full control, standard pull model.
package main

import (
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	submissionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "workflow_submissions_total",
			Help: "Total number of workflow submit requests.",
		},
		[]string{"status"},
	)

	// Histogram gives per-quantile AND sum/count — safe to aggregate across pods.
	submissionDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "workflow_submission_duration_seconds",
			Help:    "Duration of workflow submit handler.",
			Buckets: prometheus.DefBuckets,
		},
	)

	activeWorkflows int64 // atomic — exposed as gauge via GaugeFunc
)

func main() {
	activeGauge := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "workflow_active_total",
			Help: "Number of workflows currently being processed.",
		},
		func() float64 { return float64(atomic.LoadInt64(&activeWorkflows)) },
	)

	reg := prometheus.NewRegistry()
	reg.MustRegister(submissionsTotal, submissionDuration, activeGauge)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/workflow/submit", submitHandler)

	log.Println("01-prometheus-direct listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", mux))
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	atomic.AddInt64(&activeWorkflows, 1)
	defer atomic.AddInt64(&activeWorkflows, -1)

	// Simulate work: 50–200 ms, 10% error rate.
	time.Sleep(time.Duration(50+rand.Intn(150)) * time.Millisecond)
	dur := time.Since(start).Seconds()
	submissionDuration.Observe(dur)

	if rand.Float64() < 0.1 {
		submissionsTotal.WithLabelValues("error").Inc()
		http.Error(w, "workflow engine unavailable", http.StatusServiceUnavailable)
		return
	}

	submissionsTotal.WithLabelValues("ok").Inc()
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}
