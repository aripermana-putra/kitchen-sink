// lifecycle.go — Scenario F + F2: graceful shutdown via fx.Hook.
//
// F:  Single-component (already in startServer via fx.Hook)
// F2: Multi-component ordered shutdown registered via fx.Invoke
//
// FX APPROACH — shutdown order is REVERSE of OnStart registration order.
// To get HTTP stopped FIRST, register HTTP hook LAST:
//
//   lc.Append(k8sHook)       // registered 1st → stopped LAST
//   lc.Append(temporalHook)  // registered 2nd → stopped 2nd
//   lc.Append(httpHook)      // registered 3rd → stopped FIRST ✓
//
// This reverse-registration convention is non-obvious.
// Compare to 01-manual where shutdown order = statement order.
//
// WHEN FX LIFECYCLE WINS:
//   At 5+ components with non-trivial dependencies, manual sequencing
//   becomes error-prone. fx's automatic reverse-order guarantee prevents
//   ordering bugs. For UCP's 3 components, both approaches are equivalent.
package main

import (
	"context"
	"log/slog"
	"net/http"

	"go.uber.org/fx"
)

// Stubs — in real UCP these would be actual client types from the DI graph.
type fxTemporalStopper interface{ Stop() }
type fxK8sCloser interface{ Close() }

type noopFxTemporal struct{}

func (n *noopFxTemporal) Stop() { slog.Info("temporal worker stopped") }

type noopFxK8s struct{}

func (n *noopFxK8s) Close() { slog.Info("k8s client closed") }

// orderLifecycleParams groups the components whose lifecycle we manage.
type orderLifecycleParams struct {
	fx.In
	Server  *http.Server
	Worker  fxTemporalStopper
	K8s     fxK8sCloser
}

// RegisterOrderedLifecycle registers OnStart/OnStop hooks for all components.
// Registered via fx.Invoke in main() — fx calls this during app.Run().
//
// IMPORTANT: hooks registered in reverse shutdown order so fx's reverse
// execution gives us the correct sequence: HTTP stopped first.
func RegisterOrderedLifecycle(lc fx.Lifecycle, p orderLifecycleParams) {
	// Register K8s first → stopped last
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return nil },
		OnStop: func(ctx context.Context) error {
			p.K8s.Close()
			return nil
		},
	})

	// Register Temporal second → stopped second
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return nil },
		OnStop: func(ctx context.Context) error {
			p.Worker.Stop()
			return nil
		},
	})

	// Register HTTP last → stopped first (what we want)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				slog.Info("http server starting")
				if err := p.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("http error", "error", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := p.Server.Shutdown(ctx); err != nil {
				slog.Error("http shutdown error", "error", err)
			}
			slog.Info("http stopped")
			return nil
		},
	})
}
