package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/catalog"
	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/httpapi"
	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/xrdclient"
)

func main() {
	kubeconfigPath := envOr("KUBECONFIG_PATH", "kubeconfig-crossplane.yaml")
	listenAddr := envOr("LISTEN_ADDR", ":8081")
	pollInterval := durationEnvOr("POLL_INTERVAL", 10*time.Second)
	pollTimeout := durationEnvOr("POLL_TIMEOUT", 5*time.Second)

	client, err := xrdclient.New(kubeconfigPath)
	if err != nil {
		log.Fatalf("build xrdclient: %v", err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatalf("build kubeconfig: %v", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("build dynamic client: %v", err)
	}

	cache := catalog.NewCache()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go catalog.Poller(ctx, cache, client, pollTimeout, pollInterval)

	server := httpapi.NewServer(cache, dyn)
	mux := http.NewServeMux()
	server.Routes(mux)

	httpServer := &http.Server{Addr: listenAddr, Handler: mux}
	go func() {
		log.Printf("listening on %s", listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func durationEnvOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
