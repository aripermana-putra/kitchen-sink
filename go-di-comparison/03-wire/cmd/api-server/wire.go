//go:build wireinject
// +build wireinject

// wire.go — wire injection spec.
//
// ── SCENARIO D: Multiple same-type dependencies (dbRead + dbWrite) ─────────
// wire also needs disambiguation for same-type deps — but the approach is
// different from fx. Instead of name tags, wire requires separate provider
// functions with distinct return types (wrapper types) OR explicit binding.
//
// Here we use a wrapper type approach:
//   type WriteDB shared.DB
//   type ReadDB  shared.DB
// Then provide functions returning these wrapper types, and a constructor
// that takes WriteDB + ReadDB (now distinct types to wire).
//
// This is more type-safe than fx's string name tags (compile-time checked)
// but requires wrapper type boilerplate.
//
// ── SCENARIO E: Runtime strategy selection (quota per provider) ────────────
// Same as manual — wire cannot construct map[string]QuotaChecker automatically.
// buildQuotaCheckers is provided as a regular constructor returning QuotaCheckers.
package main

import (
	"github.com/google/wire"

	"github.com/kitchen-sink/03-wire/internal/compute"
	"github.com/kitchen-sink/03-wire/internal/database"
	"github.com/kitchen-sink/03-wire/internal/platform"
	"github.com/kitchen-sink/03-wire/internal/quota"
	"github.com/kitchen-sink/03-wire/internal/report"
	"github.com/kitchen-sink/di-shared"
)

func initializeHandlers() (*handlers, error) {
	wire.Build(
		shared.LoadConfig,
		platform.NewK8sClient,
		platform.NewTemporalClient,
		compute.NewService,
		compute.NewHandler,
		database.NewService,
		database.NewHandler,

		// SCENARIO D: wire uses wrapper types to disambiguate same-type deps
		platform.NewWriteDBWrapped, // returns WriteDB (wrapper type)
		platform.NewReadDBWrapped,  // returns ReadDB (wrapper type)
		newReportServiceFromWrapped, // takes WriteDB + ReadDB
		report.NewHandler,

		// SCENARIO E: buildQuotaCheckers returns QuotaCheckers (named type)
		buildQuotaCheckers,
		quota.NewService,
		quota.NewHandler,

		newHandlers,
	)
	return nil, nil
}
