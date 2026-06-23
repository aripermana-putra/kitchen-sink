// lifecycle.go — Scenario F: graceful shutdown via fx.Hook.
//
// fx.Hook{OnStart, OnStop} is the fx equivalent of manual signal handling.
//
// CONTRAST with 01-manual and 03-wire:
//
//   Manual/wire (~10 lines):
//     go srv.ListenAndServe()
//     ctx, stop := signal.NotifyContext(..., syscall.SIGTERM)
//     <-ctx.Done()
//     srv.Shutdown(shutdownCtx)
//
//   fx (~same lines, different structure):
//     lc.Append(fx.Hook{
//         OnStart: func(ctx context.Context) error { go srv.Start(); return nil },
//         OnStop:  func(ctx context.Context) error { return srv.Shutdown(ctx) },
//     })
//
// WHEN fx LIFECYCLE ADDS REAL VALUE:
//   Multiple components with ordered shutdown requirements:
//
//     lc.Append(httpHook)     // OnStop: stop accepting requests first
//     lc.Append(metricsHook)  // OnStop: flush metrics after HTTP drains
//     lc.Append(dbHook)       // OnStop: close DB last
//
//   fx guarantees OnStop runs in REVERSE OnStart order automatically:
//     Start order:  HTTP → Metrics → DB
//     Stop order:   DB → Metrics → HTTP  (automatic)
//
//   With manual code, you manage this ordering yourself via multiple
//   signal handlers or explicit sequencing — more error-prone as
//   the number of components grows.
//
// FOR UCP (1 HTTP server, optional Temporal drain):
//   Manual is sufficient. The ordering is obvious.
//   fx lifecycle becomes genuinely useful at 3+ components with
//   non-trivial shutdown dependencies.
package main

// fx.Hook is defined inline in main.go startServer() function.
// See the OnStart/OnStop hooks in startServer() — that IS this file's content.
// No additional code needed here; the hook is wired inside startServer()
// which is already registered via fx.Invoke(startServer).
