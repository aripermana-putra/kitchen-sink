// lifecycle.go — Scenario F + F2: graceful shutdown implementation.
//
// F:  Single-component shutdown (HTTP only)
// F2: Multi-component ordered shutdown (HTTP → Temporal → K8s)
//
// For UCP, F2 is the realistic scenario. The shutdown order matters:
//   1. HTTP first  — stop accepting new requests
//   2. Temporal    — drain in-flight workflow submissions
//   3. K8s         — close cluster connections last
//
// MANUAL vs FX:
//   Manual (this file): order is explicit — statement order = shutdown order.
//   fx (02-uber-fx):    order is implicit — REVERSE of hook registration order.
//                       Must register K8s → Temporal → HTTP to get HTTP stopped first.
//
// For UCP's 3 components, manual is simpler and more readable.
// fx lifecycle ordering adds value at 5+ components where manual sequencing
// becomes error-prone — not at UCP's current scale.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
)

// ── Stub interfaces ───────────────────────────────────────────────────────
// In real UCP these would be the actual Temporal and K8s client types.
// Stubbed here so the shutdown pattern is demonstrable without real infrastructure.

type temporalWorkerStopper interface {
	Stop()
}

type k8sClientCloser interface {
	Close()
}

type noopTemporalWorker struct{}

func (n *noopTemporalWorker) Stop() {
	slog.Info("temporal worker stopped")
}

type noopK8sClient struct{}

func (n *noopK8sClient) Close() {
	slog.Info("k8s client closed")
}

// ── Scenario F2: ordered graceful shutdown ────────────────────────────────

// runWithOrderedShutdown starts the Echo server and all components,
// then waits for SIGTERM/SIGINT and shuts down in the correct order.
// Called from main() — this replaces e.Logger.Fatal(e.Start(...)).
func runWithOrderedShutdown(e *echo.Echo, temporal temporalWorkerStopper, k8s k8sClientCloser, port string) {
	// Start HTTP server in background
	go func() {
		slog.Info("http server starting", "port", port)
		if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	// Block until shutdown signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutdown signal received — draining in order")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: stop HTTP — no new requests accepted after this point
	if err := e.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
	slog.Info("http stopped")

	// Step 2: drain Temporal — finish in-progress workflow submissions
	temporal.Stop()

	// Step 3: close K8s connections
	k8s.Close()

	slog.Info("shutdown complete")
}
