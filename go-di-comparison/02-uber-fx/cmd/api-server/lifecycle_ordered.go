// lifecycle_ordered.go — Scenario F2: ordered multi-component graceful shutdown.
//
// Same UCP shutdown sequence as 01-manual, using fx.Hook instead.
//
// FX APPROACH:
//   - Each component registers OnStart + OnStop in its own hook
//   - fx guarantees OnStop runs in REVERSE OnStart registration order
//   - Order is enforced by registration order, not explicit sequencing
//
// CONTRAST with 01-manual:
//   Manual: order is explicit (statement order) — immediately readable
//   fx:     order is implicit (reverse of registration) — requires knowing fx convention
//
// LINE COUNT: roughly equivalent (~20 lines each)
//
// THE REAL QUESTION: is fx's automatic ordering worth +5.5 MB binary and
// runtime error detection? For UCP's 3 components with obvious ordering,
// the manual approach is not meaningfully harder.
//
// fx lifecycle becomes more compelling at 5+ components where manual
// sequencing starts to have ordering bugs.
package main

import (
	"context"
	"log/slog"
	"net/http"

	"go.uber.org/fx"
)

type fxTemporalWorker interface{ Stop() }
type fxK8sCloser interface{ Close() }

// UCPLifecycleParams groups all components for the lifecycle hook.
type UCPLifecycleParams struct {
	fx.In
	HTTP    *http.Server
	Worker  fxTemporalWorker
	K8s     fxK8sCloser
}

// RegisterUCPLifecycle registers OnStart/OnStop hooks for all UCP components.
// fx calls OnStop in REVERSE OnStart order automatically:
//   Start order: HTTP → Temporal → K8s
//   Stop order:  K8s → Temporal → HTTP  ← fx reverses this
//
// Wait — that's the WRONG order for UCP. We want HTTP stopped FIRST,
// then Temporal, then K8s. So we register in the order we want to STOP:
//   Register: K8s → Temporal → HTTP
//   Start order: K8s → Temporal → HTTP
//   Stop order (reversed): HTTP → Temporal → K8s  ✓ correct
//
// This non-obvious registration order is a hidden complexity of fx lifecycle.
// With manual code (01-manual) the order is simply the order of statements.
func RegisterUCPLifecycle(lc fx.Lifecycle, p UCPLifecycleParams) {
	// Register in reverse shutdown order (fx reverses for OnStop)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			slog.Info("k8s client connected")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			p.K8s.Close()
			slog.Info("k8s client closed")
			return nil
		},
	})

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			slog.Info("temporal worker started")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			p.Worker.Stop()
			slog.Info("temporal worker stopped")
			return nil
		},
	})

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go p.HTTP.ListenAndServe()
			slog.Info("http server started", "addr", p.HTTP.Addr)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := p.HTTP.Shutdown(ctx); err != nil {
				slog.Error("http shutdown error", "error", err)
			}
			slog.Info("http stopped")
			return nil
		},
	})
}
