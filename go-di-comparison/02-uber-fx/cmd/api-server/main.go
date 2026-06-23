// 02-uber-fx: Dependency injection via uber/fx — reflection-based container.
//
// ── SCENARIO D: Multiple same-type dependencies (dbRead + dbWrite) ─────────
// Both are shared.DB — same interface type. fx resolves by type, so two
// providers returning the same type causes a panic: "two providers for shared.DB".
// SOLUTION: fx.Annotate + name tags — extra boilerplate not needed with manual.
//
// ── SCENARIO E: Runtime strategy selection (quota per provider) ────────────
// fx cannot construct map[string]QuotaChecker automatically — build map manually.
//
// ── SCENARIO F2: Ordered graceful shutdown ─────────────────────────────────
// All lifecycle hooks registered in startServer — one function owns all lifecycle.
// Hooks registered in REVERSE shutdown order so fx's OnStop runs correctly:
//   Registration: K8s → Temporal → HTTP
//   OnStop order: HTTP → Temporal → K8s  ✓ (fx reverses automatically)
// Compare to 01-manual where shutdown order = statement order — more readable.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/fx"

	api "github.com/kitchen-sink/02-uber-fx/gen"
	"github.com/kitchen-sink/02-uber-fx/internal/compute"
	"github.com/kitchen-sink/02-uber-fx/internal/database"
	"github.com/kitchen-sink/02-uber-fx/internal/platform"
	"github.com/kitchen-sink/02-uber-fx/internal/quota"
	"github.com/kitchen-sink/02-uber-fx/internal/report"
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

// ── SCENARIO D: fx.In params struct for named same-type dependencies ───────

type reportParams struct {
	fx.In
	Write shared.DB `name:"write"`
	Read  shared.DB `name:"read"`
}

func newReportService(p reportParams) *report.Service {
	return report.NewService(p.Write, p.Read)
}

// ── SCENARIO E: quota map ─────────────────────────────────────────────────

func buildQuotaCheckers(cfg *shared.Config) quota.QuotaCheckers {
	return quota.QuotaCheckers{
		"gcp": platform.NewGCPQuotaChecker(cfg),
		"aws": platform.NewAWSQuotaChecker(cfg),
		"roc": platform.NewROCQuotaChecker(cfg),
	}
}

// ── SCENARIO F2: server + ordered lifecycle ───────────────────────────────

type serverParams struct {
	fx.In
	Handlers *handlers
	Config   *shared.Config
	Worker   fxTemporalStopper
	K8s      fxK8sCloser
}

// startServer sets up Echo and registers ALL lifecycle hooks in one place.
// Hooks are registered in reverse shutdown order — fx reverses for OnStop:
//
//	Registration: K8s → Temporal → HTTP
//	OnStop order: HTTP → Temporal → K8s  ✓
func startServer(lc fx.Lifecycle, p serverParams) {
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
	api.RegisterHandlers(e, api.NewStrictHandler(p.Handlers, nil))

	// Register K8s first → stopped last
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { p.K8s.Close(); return nil },
	})
	// Register Temporal second → stopped second
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { p.Worker.Stop(); return nil },
	})
	// Register HTTP last → stopped first
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { go e.Start(":" + p.Config.Port); return nil },
		OnStop:  func(ctx context.Context) error { return e.Shutdown(ctx) },
	})
}

func main() {
	fx.New(
		fx.Provide(shared.LoadConfig),
		fx.Provide(platform.NewK8sClient),
		fx.Provide(platform.NewTemporalClient),
		fx.Provide(compute.NewService),
		fx.Provide(compute.NewHandler),
		fx.Provide(database.NewService),
		fx.Provide(database.NewHandler),

		// SCENARIO D: without fx.Annotate, fx panics "two providers for shared.DB"
		fx.Provide(fx.Annotate(platform.NewWriteDB, fx.ResultTags(`name:"write"`))),
		fx.Provide(fx.Annotate(platform.NewReadDB, fx.ResultTags(`name:"read"`))),
		fx.Provide(newReportService),
		fx.Provide(report.NewHandler),

		// SCENARIO E: map built manually
		fx.Provide(buildQuotaCheckers),
		fx.Provide(quota.NewService),
		fx.Provide(quota.NewHandler),

		fx.Provide(newHandlers),

		// SCENARIO F2: stubs for temporal + k8s — real UCP passes actual clients
		fx.Provide(func() fxTemporalStopper { return &noopFxTemporal{} }),
		fx.Provide(func() fxK8sCloser { return &noopFxK8s{} }),

		fx.Invoke(startServer),
	).Run()
}
