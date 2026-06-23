// lifecycle.go — Scenario F: graceful shutdown (OnStart/OnStop equivalent).
//
// Manual approach uses stdlib signal handling.
// This is the SAME pattern whether you use manual injection or wire —
// wire only generates constructor calls, it has no lifecycle management.
//
// MANUAL / WIRE equivalent of fx.Hook{OnStart, OnStop}:
//
//   OnStart equivalent: go srv.Start(port)
//   OnStop equivalent:  signal.NotifyContext → srv.Shutdown()
//
// The manual approach is ~10 lines of stdlib code.
// Compare to 02-uber-fx/cmd/api-server/lifecycle.go which uses fx.Hook.
//
// When does fx lifecycle add real value?
//   - Multiple components need ordered shutdown (e.g. stop HTTP first,
//     then flush metrics, then close DB — in that exact order)
//   - fx guarantees OnStop runs in reverse OnStart order automatically
//   - Manual: you manage the order yourself
//
// For UCP (1 HTTP server + optional graceful Temporal drain):
//   Manual is sufficient — the ordering is obvious and trivial.
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

// startWithGracefulShutdown starts the Echo server and blocks until
// SIGTERM or SIGINT, then performs graceful shutdown.
// This is the manual equivalent of fx.Hook{OnStart, OnStop}.
func startWithGracefulShutdown(srv *http.Server) {
	// OnStart equivalent — start in background
	go func() {
		slog.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal (SIGTERM from Kubernetes, SIGINT from Ctrl+C)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutdown signal received, draining...")

	// OnStop equivalent — graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	slog.Info("server stopped cleanly")
}
