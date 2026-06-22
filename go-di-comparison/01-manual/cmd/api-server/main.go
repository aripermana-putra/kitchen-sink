// 01-manual: Manual constructor injection — no DI framework.
//
// ── SCENARIO A — Initial wiring (2 feature slices: compute + database) ────
// Wiring lines in buildApp(): 8 lines  [marked with WIRE-A]
//
// ── SCENARIO B — Add 1 new feature slice (storage) ────────────────────────
// Add internal/storage/service.go + handler.go
// Add 2 lines to buildApp() + delegation methods in handlers struct
//
// ── SCENARIO D — Multiple same-type dependencies (dbRead + dbWrite) ────────
// Both are shared.DB — same interface, different instances.
// Manual: just two positional args — clear, compiler-enforced.
// uber/fx: requires fx.Annotate + name tags to disambiguate same-type deps.
// See: 02-uber-fx/cmd/api-server/main.go for the fx contrast.
//
// ── SCENARIO E — Runtime strategy selection (quota per provider) ───────────
// QuotaChecker has 3 implementations (GCP, AWS, ROC).
// Strategy is selected per request based on req.Provider.
// All implementations are wired at startup into a map.
// This pattern is identical across all 3 DI approaches —
// the dispatch logic is in the service, not the DI framework.
//
// ── ERROR DETECTION ────────────────────────────────────────────────────────
// Remove `dbWrite` from report.NewService(dbWrite, dbRead) → immediate build:
//   "not enough arguments in call to report.NewService"
//
// ── AI ASSISTANCE ──────────────────────────────────────────────────────────
// Pattern is plain Go function calls. No framework knowledge required.
// The full dependency graph is readable top-to-bottom in buildApp().
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	api "github.com/kitchen-sink/01-manual/gen"
	"github.com/kitchen-sink/01-manual/internal/compute"
	"github.com/kitchen-sink/01-manual/internal/database"
	"github.com/kitchen-sink/01-manual/internal/platform"
	"github.com/kitchen-sink/01-manual/internal/quota"
	"github.com/kitchen-sink/01-manual/internal/report"
	"github.com/kitchen-sink/di-shared"
)

// ── Wiring ────────────────────────────────────────────────────────────────

func buildApp() (*handlers, error) {
	cfg := shared.LoadConfig()                          // [WIRE-A 1]

	k8s := platform.NewK8sClient(cfg)                  // [WIRE-A 2]
	temporal := platform.NewTemporalClient(cfg)         // [WIRE-A 3]

	computeSvc := compute.NewService(k8s, temporal)     // [WIRE-A 4]
	computeH := compute.NewHandler(computeSvc)          // [WIRE-A 5]

	databaseSvc := database.NewService(k8s, temporal)   // [WIRE-A 6]
	databaseH := database.NewHandler(databaseSvc)       // [WIRE-A 7]

	// ── SCENARIO D: two instances of the same shared.DB interface ─────────
	// Plain positional args — no annotation, no name tag, no ambiguity.
	// Contrast with 02-uber-fx where fx.Annotate + name tags are required.
	dbWrite, err := platform.NewWriteDB(cfg)            // [WIRE-D 1]
	if err != nil {
		return nil, err
	}
	dbRead, err := platform.NewReadDB(cfg)              // [WIRE-D 2]
	if err != nil {
		return nil, err
	}
	reportSvc := report.NewService(dbWrite, dbRead)     // [WIRE-D 3] — two same-type args, clear intent
	reportH := report.NewHandler(reportSvc)             // [WIRE-D 4]

	// ── SCENARIO E: runtime strategy selection via map ────────────────────
	// All provider implementations constructed at startup.
	// Service selects the right one per request based on req.Provider.
	// Adding a new provider = one line here, service is untouched.
	quotaSvc := quota.NewService(quota.QuotaCheckers{   // [WIRE-E 1]
		"gcp": platform.NewGCPQuotaChecker(cfg),
		"aws": platform.NewAWSQuotaChecker(cfg),
		"roc": platform.NewROCQuotaChecker(cfg),
	})
	quotaH := quota.NewHandler(quotaSvc)                // [WIRE-E 2]

	return newHandlers(computeH, databaseH, reportH, quotaH), nil
}

// ── Handlers ──────────────────────────────────────────────────────────────

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

// ── Server setup ──────────────────────────────────────────────────────────

func main() {
	app, err := buildApp()
	if err != nil {
		slog.Error("failed to build app", "error", err)
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

	slog.Info("01-manual listening", "port", 8081)
	e.Logger.Fatal(e.Start(":8081"))
}
