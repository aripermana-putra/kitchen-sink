// 01-manual: Manual constructor injection — no DI framework.
//
// ── SCENARIO A — Initial wiring (2 feature slices: compute + database) ────
// Wiring lines in buildApp(): 8 lines  [marked with WIRE]
// Files involved in wiring: 1 (this file only)
// Error detection: compile time
//
// ── SCENARIO B — Add 1 new feature slice (storage) ────────────────────────
// New files: internal/storage/service.go, internal/storage/handler.go
// Changes in this file:
//   + storageSvc := storage.NewService(k8s, temporal)   [+1 line]
//   + storageH   := storage.NewHandler(storageSvc)      [+1 line]
//   + storageH field in handlers struct                  [+1 line]
//   + storageH arg in newHandlers()                      [+1 line]
//   + delegation methods for storage endpoints           [+N lines matching spec]
// Total main.go delta: ~4 lines + delegation methods
//
// ── ERROR DETECTION ────────────────────────────────────────────────────────
// Remove `temporal` from compute.NewService(k8s) → immediate build failure:
//   "not enough arguments in call to compute.NewService"
// No app.Run() needed — compiler catches it before a single line executes.
//
// ── AI ASSISTANCE ──────────────────────────────────────────────────────────
// Pattern is plain Go function calls. No framework knowledge required.
// AI sees the graph in one file and replicates the pattern mechanically.
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
	"github.com/kitchen-sink/di-shared"
)

// ── Wiring ────────────────────────────────────────────────────────────────
// All dependency construction in one function.
// Reading top-to-bottom shows the full graph: what depends on what.

func buildApp() *handlers {
	cfg := shared.LoadConfig()                         // [WIRE 1] read config

	k8s := platform.NewK8sClient(cfg)                 // [WIRE 2] platform: k8s
	temporal := platform.NewTemporalClient(cfg)        // [WIRE 3] platform: temporal

	computeSvc := compute.NewService(k8s, temporal)    // [WIRE 4] compute service
	computeH := compute.NewHandler(computeSvc)         // [WIRE 5] compute handler

	databaseSvc := database.NewService(k8s, temporal)  // [WIRE 6] database service
	databaseH := database.NewHandler(databaseSvc)      // [WIRE 7] database handler

	return newHandlers(computeH, databaseH)            // [WIRE 8] assemble
}

// ── Handlers ──────────────────────────────────────────────────────────────
// Aggregates all feature handlers and implements StrictServerInterface.
// Each method delegates to the appropriate feature handler.

type handlers struct {
	compute  *compute.Handler
	database *database.Handler
}

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

// ── Server setup ──────────────────────────────────────────────────────────

func main() {
	app := buildApp()

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
