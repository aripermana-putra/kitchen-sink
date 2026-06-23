// lifecycle.go — Scenario F2: stubs and types for ordered graceful shutdown.
//
// The actual lifecycle hooks (OnStart/OnStop) are registered inside startServer()
// in main.go — one function owns all lifecycle. No separate RegisterOrderedLifecycle
// needed — that would conflict with startServer's HTTP hook.
//
// FX SHUTDOWN ORDER CONVENTION:
//   fx runs OnStop in REVERSE OnStart registration order.
//   To get HTTP stopped FIRST, HTTP hook must be registered LAST in startServer().
//   See main.go startServer() for the full hook registration with comments.
//
// Contrast with 01-manual/lifecycle.go where shutdown order = statement order.
package main

import "log/slog"

// Stubs — in real UCP these would be actual Temporal worker + K8s client
// passed through the fx dependency graph.
type fxTemporalStopper interface{ Stop() }
type fxK8sCloser interface{ Close() }

type noopFxTemporal struct{}

func (n *noopFxTemporal) Stop() { slog.Info("temporal worker stopped") }

type noopFxK8s struct{}

func (n *noopFxK8s) Close() { slog.Info("k8s client closed") }
