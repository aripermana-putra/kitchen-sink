// lifecycle_ordered.go — Scenario F2: ordered multi-component graceful shutdown.
//
// wire only generates constructor wiring — no lifecycle management.
// Ordered shutdown is IDENTICAL to 01-manual.
//
// This confirms: wire = DI wiring only.
// If you need lifecycle management with wire, you write it manually.
// wire provides zero help here — unlike fx which at least gives you
// the reverse-order guarantee automatically.
package main

// See 01-manual/cmd/api-server/lifecycle_ordered.go — identical implementation.
// UCPComponents, temporalWorker, k8sCloser, and startOrderedShutdown
// are defined there and copied here verbatim.

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type UCPComponents struct {
	HTTPServer     *http.Server
	TemporalWorker temporalWorker
	K8sClient      k8sCloser
}

type temporalWorker interface{ Stop() }
type k8sCloser interface{ Close() }

func startOrderedShutdown(c UCPComponents) {
	go func() {
		slog.Info("http server starting", "addr", c.HTTPServer.Addr)
		if err := c.HTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutdown signal received — draining in order")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.HTTPServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}
	slog.Info("http stopped")

	c.TemporalWorker.Stop()
	slog.Info("temporal worker stopped")

	c.K8sClient.Close()
	slog.Info("k8s client closed")

	slog.Info("shutdown complete")
}
