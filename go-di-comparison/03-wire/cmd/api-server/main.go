// 03-wire: Dependency injection via google/wire — code generation.
//
// ── SCENARIO D: Multiple same-type dependencies (dbRead + dbWrite) ─────────
// wire uses WRAPPER TYPES to distinguish same-type deps — more type-safe than
// fx's string name tags but requires extra boilerplate (WriteDB, ReadDB types
// in platform/db_wire.go + adapter function here).
//
// Compare:
//   Manual:  report.NewService(dbWrite, dbRead)  — 0 extra code
//   fx:      fx.Annotate + name tags             — extra params struct
//   wire:    WriteDB/ReadDB wrapper types         — extra types + adapter
//
// ── SCENARIO E: Runtime strategy selection (quota per provider) ────────────
// Same as manual — buildQuotaCheckers constructs the map, wire provides it.
//
// ── NOTE: MAINTENANCE STATUS ──────────────────────────────────────────────
// google/wire has been in maintenance mode since ~2022.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	api "github.com/kitchen-sink/03-wire/gen"
	"github.com/kitchen-sink/03-wire/internal/compute"
	"github.com/kitchen-sink/03-wire/internal/database"
	"github.com/kitchen-sink/03-wire/internal/platform"
	"github.com/kitchen-sink/03-wire/internal/quota"
	"github.com/kitchen-sink/03-wire/internal/report"
	"github.com/kitchen-sink/di-shared"
)

type handlers struct {
	compute  *compute.Handler
	database *database.Handler
	report   *report.Handler
	quota    *quota.Handler
}

func newHandlers(c *compute.Handler, d *database.Handler, r *report.Handler, q *quota.Handler) *handlers {
	return &handlers{compute: c, database: d, report: r, quota: q}
}

var _ api.StrictServerInterface = (*handlers)(nil)

func (h *handlers) CreateCompute(ctx context.Context, req api.CreateComputeRequestObject) (api.CreateComputeResponseObject, error) {
	return h.compute.CreateCompute(ctx, req)
}
func (h *handlers) GetCompute(ctx context.Context, req api.GetComputeRequestObject) (api.GetComputeResponseObject, error) {
	return h.compute.GetCompute(ctx, req)
}
func (h *handlers) CreateDatabase(ctx context.Context, req api.CreateDatabaseRequestObject) (api.CreateDatabaseResponseObject, error) {
	return h.database.CreateDatabase(ctx, req)
}
func (h *handlers) GetDatabase(ctx context.Context, req api.GetDatabaseRequestObject) (api.GetDatabaseResponseObject, error) {
	return h.database.GetDatabase(ctx, req)
}
func (h *handlers) GetReport(ctx context.Context, req api.GetReportRequestObject) (api.GetReportResponseObject, error) {
	return h.report.GetReport(ctx, req)
}
func (h *handlers) CheckQuota(ctx context.Context, req api.CheckQuotaRequestObject) (api.CheckQuotaResponseObject, error) {
	return h.quota.CheckQuota(ctx, req)
}

// SCENARIO D: adapter that converts wrapper types back to shared.DB
// This is the boilerplate cost of wire's wrapper type approach.
func newReportServiceFromWrapped(w platform.WriteDB, r platform.ReadDB) *report.Service {
	return report.NewService(shared.DB(w), shared.DB(r))
}

// SCENARIO E: quota map — identical to manual
func buildQuotaCheckers(cfg *shared.Config) quota.QuotaCheckers {
	return quota.QuotaCheckers{
		"gcp": platform.NewGCPQuotaChecker(cfg),
		"aws": platform.NewAWSQuotaChecker(cfg),
		"roc": platform.NewROCQuotaChecker(cfg),
	}
}

func main() {
	app, err := initializeHandlers()
	if err != nil {
		slog.Error("failed to initialize", "error", err)
		return
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Generator: func() string { return uuid.New().String() },
	}))
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		reqID := c.Response().Header().Get(echo.HeaderXRequestID)
		var de *shared.DomainError
		if errors.As(err, &de) {
			if de.Cause != nil {
				slog.Error("domain error", "code", de.Code, "cause", de.Cause, "request_id", reqID)
			}
			c.JSON(de.Status, map[string]string{"code": de.Code, "message": de.Message, "requestId": reqID})
			return
		}
		var he *echo.HTTPError
		if errors.As(err, &he) {
			c.JSON(he.Code, map[string]string{"code": "HTTP_ERROR", "message": http.StatusText(he.Code), "requestId": reqID})
			return
		}
		slog.Error("unhandled error", "error", err, "request_id", reqID)
		c.JSON(500, map[string]string{"code": "INTERNAL_ERROR", "message": "an internal error occurred", "requestId": reqID})
	}

	api.RegisterHandlers(e, api.NewStrictHandler(app, nil))

	// Scenario F2: ordered graceful shutdown — identical to 01-manual.
	// wire has no lifecycle management — this is always manual.
	runWithOrderedShutdown(e, &noopTemporalWorker{}, &noopK8sClient{}, "8083")
}
