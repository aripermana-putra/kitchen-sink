//go:build wireinject
// +build wireinject

// wire.go — wire injection spec. This file is used ONLY by the wire tool.
// It is excluded from normal builds via the wireinject build tag.
// Run `wire ./cmd/api-server/` to regenerate wire_gen.go.
//
// ── SCENARIO B — Add 1 new feature slice (storage) ────────────────────────
// Changes in this file:
//   + storage.NewService in wire.Build   [+1 entry]
//   + storage.NewHandler in wire.Build   [+1 entry]
// Then run: wire ./cmd/api-server/
// wire regenerates wire_gen.go automatically — no manual editing of wire_gen.go
//
// ── ERROR DETECTION ────────────────────────────────────────────────────────
// Remove platform.NewTemporalClient from wire.Build → wire generate fails:
//   "cannot find provider for shared.TemporalClient"
// The error surfaces at wire generate time — before any compilation.
// Like manual, errors are caught before app.Run() but unlike manual,
// a separate generate step is required.
package main

import (
	"github.com/google/wire"

	"github.com/kitchen-sink/03-wire/internal/compute"
	"github.com/kitchen-sink/03-wire/internal/database"
	"github.com/kitchen-sink/03-wire/internal/platform"
	"github.com/kitchen-sink/di-shared"
)

// initializeHandlers is the wire injector function.
// wire reads this, resolves the full graph, and generates initializeHandlers
// in wire_gen.go as plain Go constructor calls — zero runtime overhead.
func initializeHandlers() (*handlers, error) {
	wire.Build(
		shared.LoadConfig,              // [WIRE 1]
		platform.NewK8sClient,         // [WIRE 2]
		platform.NewTemporalClient,     // [WIRE 3]
		compute.NewService,             // [WIRE 4]
		compute.NewHandler,             // [WIRE 5]
		database.NewService,            // [WIRE 6]
		database.NewHandler,            // [WIRE 7]
		newHandlers,                    // [WIRE 8]
	)
	return nil, nil // wire replaces this body
}
