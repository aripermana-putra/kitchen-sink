// 01-echo-standard: Echo with manual handlers — no code generation.
//
// Key things to observe:
// - Global error handler via e.HTTPErrorHandler — ONE place handles all errors
// - Every handler just returns an error, never writes error responses directly
// - Request binding via c.Bind(), validation is manual per-handler
// - Compare boilerplate to 02-echo-strict where codegen eliminates most of this
package main

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/kitchen-sink/shared"
)

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

func main() {
	store := shared.NewStore()

	e := echo.New()
	e.HideBanner = true

	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Generator: func() string { return uuid.New().String() },
	}))

	// Global error handler — ONE function for all error responses.
	// Every handler returns errors; this decides what the client sees.
	// Internal cause is never exposed — logged server-side only.
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		reqID := c.Response().Header().Get(echo.HeaderXRequestID)

		var de *shared.DomainError
		if errors.As(err, &de) {
			if de.Cause != nil {
				slog.Error("domain error", "code", de.Code, "cause", de.Cause, "request_id", reqID)
			}
			c.JSON(de.Status, ErrorResponse{Code: de.Code, Message: de.Message, RequestID: reqID})
			return
		}

		// Echo's own errors (e.g. 404 from unmatched route)
		var he *echo.HTTPError
		if errors.As(err, &he) {
			c.JSON(he.Code, ErrorResponse{Code: "HTTP_ERROR", Message: http.StatusText(he.Code), RequestID: reqID})
			return
		}

		slog.Error("unhandled error", "error", err, "request_id", reqID)
		c.JSON(500, ErrorResponse{Code: "INTERNAL_ERROR", Message: "an internal error occurred", RequestID: reqID})
	}

	e.POST("/compute", func(c echo.Context) error {
		var req struct {
			Name     string `json:"name"`
			TenantID string `json:"tenantId"`
			Provider string `json:"provider"`
			Size     string `json:"size"`
		}
		if err := c.Bind(&req); err != nil {
			return &shared.DomainError{Code: "INVALID_REQUEST", Message: "invalid request body", Status: 400}
		}
		// Manual validation — contrast with oapi-codegen strict which generates this
		if req.Name == "" {
			return &shared.DomainError{Code: "INVALID_REQUEST", Message: "name is required", Status: 400}
		}
		if req.TenantID == "" {
			return &shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId is required", Status: 400}
		}
		if req.Provider != "gcp" && req.Provider != "aws" {
			return &shared.DomainError{Code: "INVALID_REQUEST", Message: "provider must be gcp or aws", Status: 400}
		}
		if req.Size == "" {
			req.Size = "medium"
		}

		inst, err := store.Create(req.Name, req.TenantID, req.Provider, req.Size)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusAccepted, map[string]string{
			"workflowId": inst.WorkflowID,
			"status":     "provisioning",
			"message":    "compute instance provisioning started",
		})
	})

	e.GET("/compute", func(c echo.Context) error {
		tenantID := c.QueryParam("tenantId")
		if tenantID == "" {
			return &shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId query param is required", Status: 400}
		}
		limit := 20
		instances, err := store.List(tenantID, limit)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]interface{}{"items": instances, "total": len(instances)})
	})

	e.GET("/compute/:name", func(c echo.Context) error {
		inst, err := store.Get(c.Param("name"))
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, inst)
	})

	e.DELETE("/compute/:name", func(c echo.Context) error {
		if err := store.Delete(c.Param("name")); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})

	slog.Info("01-echo-standard listening", "port", 9001)
	e.Logger.Fatal(e.Start(":9001"))
}
