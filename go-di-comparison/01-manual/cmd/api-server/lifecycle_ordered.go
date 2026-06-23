// lifecycle_ordered.go — Scenario F2: ordered multi-component graceful shutdown.
//
// Realistic UCP shutdown sequence:
//   1. Stop HTTP server — stop accepting new /compute /database requests
//   2. Stop Temporal worker — stop picking up new workflow tasks
//   3. Close K8s client — close connections to the cluster
//
// Manual approach: explicit sequencing, fully readable, ~20 lines of stdlib.
// The order is enforced by the order of statements — obvious to any reader.
//
// Compare to 02-uber-fx/cmd/api-server/lifecycle_ordered.go which uses
// fx.Hook to achieve the same — same number of lines, order is implicit
// (reverse of OnStart registration order).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// UCPComponents holds all components that need ordered shutdown.
// In real UCP these would be the actual clients — faked here for the comparison.
type UCPComponents struct {
	HTTPServer     *http.Server
	TemporalWorker temporalWorker
	K8sClient      k8sCloser
}

type temporalWorker interface{ Stop() }
type k8sCloser interface{ Close() }

// startOrderedShutdown demonstrates multi-component ordered graceful shutdown.
//
// SHUTDOWN ORDER (matters — must stop in reverse startup order):
//   1. HTTP first  — stop new requests entering the system
//   2. Temporal    — drain in-flight workflow tasks
//   3. K8s         — close cluster connections last
//
// MANUAL COST: ~20 lines, plain stdlib, order is explicit and readable.
// No framework knowledge required. Any Go developer can read and modify this.
func startOrderedShutdown(c UCPComponents) {
	// Start all components
	go func() {
		slog.Info("http server starting", "addr", c.HTTPServer.Addr)
		if err := c.HTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for SIGTERM (Kubernetes) or SIGINT (Ctrl+C)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutdown signal received — draining in order")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: stop HTTP — no new requests after this point
	if err := c.HTTPServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
	slog.Info("http stopped")

	// Step 2: drain Temporal worker — finish in-progress tasks
	c.TemporalWorker.Stop()
	slog.Info("temporal worker stopped")

	// Step 3: close K8s connections
	c.K8sClient.Close()
	slog.Info("k8s client closed")

	slog.Info("shutdown complete")
}
