// 02-uber-fx: Dependency injection via uber/fx — reflection-based container.
//
// ── SCENARIO A — Initial wiring (2 feature slices: compute + database) ────
// Wiring lines (fx.Provide + fx.Invoke): 11 lines  [marked with WIRE]
// Files involved in wiring: 1 (this file only)
// Error detection: runtime — app.Run() panics on missing/wrong dependency
//
// ── SCENARIO B — Add 1 new feature slice (storage) ────────────────────────
// New files: internal/storage/service.go, internal/storage/handler.go
// Changes in this file:
//   + fx.Provide(storage.NewService)   [+1 line]
//   + fx.Provide(storage.NewHandler)   [+1 line]
//   + Add *storage.Handler to handlers struct   [+1 field]
//   + delegation methods for storage endpoints  [+N lines]
// Total main.go delta: ~3 lines + delegation methods
//
// ── ERROR DETECTION ────────────────────────────────────────────────────────
// Remove fx.Provide(platform.NewTemporalClient) → app panics at app.Run():
//   "missing type: *platform.temporalClient (did you mean to use fx.Provide?)"
// The error is clear but only surfaces at runtime startup, not at compile time.
// All imports still compile — the missing dependency is invisible to the compiler.
//
// ── AI ASSISTANCE ──────────────────────────────────────────────────────────
// AI must know fx.Provide/fx.Invoke conventions and the fx.App lifecycle.
// The dependency graph is implicit — fx resolves it by reflecting on constructor
// signatures at runtime. Reading main.go alone does not show the full graph.
// AI needs to know: "add fx.Provide for every new constructor, types must match".
// A wrong type (e.g. providing *Service where *Handler is expected) compiles fine
// but panics at app.Run() with a type mismatch error.
//
// ── DEPENDENCY FOOTPRINT ───────────────────────────────────────────────────
// Direct dependency added: go.uber.org/fx v1.23.0
// Transitive dependencies pulled in: ~12 packages
// Packages actually used in application code: go.uber.org/fx only
// The rest (go.uber.org/dig, go.uber.org/zap, etc.) are fx internals —
// they ship in your binary but you never import them directly.
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
	"github.com/kitchen-sink/di-shared"
)

// ── Handlers ──────────────────────────────────────────────────────────────

type handlers struct {
	compute  *compute.Handler
	database *database.Handler
}

// newHandlers is a constructor fx will call — it resolves the arguments
// by matching their types against what was provided via fx.Provide.
func newHandlers(c *compute.Handler, d *database.Handler) *handlers {
	return &handlers{compute: c, database: d}
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

// ── Server ────────────────────────────────────────────────────────────────

type serverParams struct {
	fx.In
	Handlers *handlers
	Config   *shared.Config
}

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

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go e.Start(":" + p.Config.Port)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return e.Shutdown(ctx)
		},
	})
}

// ── Wiring ────────────────────────────────────────────────────────────────
// fx resolves the graph by reflecting on constructor signatures at runtime.
// Every type that a constructor needs must be provided somewhere in the app.

func main() {
	app := fx.New(
		fx.Provide(shared.LoadConfig),                  // [WIRE 1]
		fx.Provide(platform.NewK8sClient),              // [WIRE 2]
		fx.Provide(platform.NewTemporalClient),         // [WIRE 3]
		fx.Provide(compute.NewService),                 // [WIRE 4]
		fx.Provide(compute.NewHandler),                 // [WIRE 5]
		fx.Provide(database.NewService),                // [WIRE 6]
		fx.Provide(database.NewHandler),                // [WIRE 7]
		fx.Provide(newHandlers),                        // [WIRE 8]
		fx.Invoke(startServer),                         // [WIRE 9] — runs at app.Run()
	)
	app.Run()
}
