// Command apiserver stands in for the real api-server in this PoC: it seeds
// its catalog cache from the Platform DB at startup, polls the Crossplane
// cluster on a fixed interval, and serves the cache over HTTP.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-catalog-source-poc/internal/catalog"
	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-catalog-source-poc/internal/db"
	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-catalog-source-poc/internal/xrdclient"
)

func main() {
	kubeconfigPath := envOr("KUBECONFIG_PATH", "kubeconfig-crossplane.yaml")
	dsn := envOr("PLATFORM_DB_DSN", "postgres://ucp:ucp@localhost:5432/platform")
	addr := envOr("LISTEN_ADDR", ":8080")
	pollInterval := envDurationOr("POLL_INTERVAL", 10*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect to platform db: %v", err)
	}

	xrd, err := xrdclient.New(kubeconfigPath)
	if err != nil {
		log.Fatalf("build xrd client: %v", err)
	}

	cache := &catalog.Cache{}

	seed, err := store.LatestSnapshot(ctx)
	if err != nil {
		log.Fatalf("read latest snapshot for warm start: %v", err)
	}
	cache.Seed(seed)
	log.Printf("warm start: seeded cache with %d item(s) from platform db", len(seed))

	go catalog.Poller(ctx, cache, xrd, store, pollInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /catalog", catalogHandler(cache))
	mux.HandleFunc("GET /health/ready", readyHandler(cache))

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// catalogHandler only ever reads the in-memory cache — never the Kubernetes
// API or the Platform DB on the request path.
func catalogHandler(cache *catalog.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cache.List())
	}
}

// readyHandler reports ready as soon as the cache has been seeded at least
// once (DB warm start or a live poll) — it never waits on a live poll.
func readyHandler(cache *catalog.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cache.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("invalid %s=%q, using default %s: %v", key, v, fallback, err)
		return fallback
	}
	return d
}
