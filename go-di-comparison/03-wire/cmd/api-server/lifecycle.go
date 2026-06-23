// lifecycle.go — Scenario F: graceful shutdown for wire variant.
//
// wire only generates constructor wiring — it has NO lifecycle management.
// Graceful shutdown is identical to 01-manual: stdlib signal handling.
//
// This is an important distinction from fx:
//   wire = DI wiring only (like manual, just generated)
//   fx   = DI wiring + lifecycle management (OnStart/OnStop hooks)
//
// If you need structured lifecycle management with wire, you add it manually —
// same signal.NotifyContext pattern as 01-manual.
//
// See 01-manual/cmd/api-server/lifecycle.go for the implementation.
// 03-wire uses the exact same startWithGracefulShutdown function.
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

// startWithGracefulShutdown — identical to 01-manual.
// wire adds nothing here; lifecycle is always manual.
func startWithGracefulShutdown(srv *http.Server) {
	go func() {
		slog.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	slog.Info("server stopped cleanly")
}
