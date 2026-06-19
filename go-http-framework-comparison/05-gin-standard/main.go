// 05-gin-standard: Gin with manual handlers — no code generation.
//
// Key things to observe:
// - Gin has a global error handler via c.Errors and custom middleware,
//   but it's NOT as clean as echo — handlers must call c.Error(err) explicitly
//   then a middleware reads c.Errors at the end. Easy to forget c.Error().
// - Alternative: use c.AbortWithStatusJSON() directly — but then error formatting
//   is per-handler again, same problem as chi.
// - This example uses the middleware approach to show the best-case gin pattern.
// - Request binding via c.ShouldBindJSON() — similar to echo's c.Bind()
package main

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/kitchen-sink/shared"
)

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

func main() {
	store := shared.NewStore()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Request ID middleware
	r.Use(func(c *gin.Context) {
		c.Set("requestId", uuid.New().String())
		c.Next()
	})

	// Global error handler middleware — reads c.Errors set by handlers.
	// Handlers call c.Error(err) + c.Abort() instead of writing responses directly.
	// Problem: if a handler forgets c.Error() and writes directly, this is bypassed.
	r.Use(func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		reqID, _ := c.Get("requestId")
		err := c.Errors.Last().Err

		var de *shared.DomainError
		if errors.As(err, &de) {
			if de.Cause != nil {
				slog.Error("domain error", "code", de.Code, "cause", de.Cause, "request_id", reqID)
			}
			c.JSON(de.Status, ErrorResponse{Code: de.Code, Message: de.Message, RequestID: reqID.(string)})
			return
		}

		slog.Error("unhandled error", "error", err, "request_id", reqID)
		c.JSON(500, ErrorResponse{Code: "INTERNAL_ERROR", Message: "an internal error occurred", RequestID: reqID.(string)})
	})

	r.POST("/compute", func(c *gin.Context) {
		var req struct {
			Name     string `json:"name"`
			TenantID string `json:"tenantId"`
			Provider string `json:"provider"`
			Size     string `json:"size"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.Error(&shared.DomainError{Code: "INVALID_REQUEST", Message: "invalid request body", Status: 400})
			c.Abort()
			return
		}
		if req.Name == "" {
			c.Error(&shared.DomainError{Code: "INVALID_REQUEST", Message: "name is required", Status: 400})
			c.Abort()
			return
		}
		if req.TenantID == "" {
			c.Error(&shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId is required", Status: 400})
			c.Abort()
			return
		}
		if req.Provider != "gcp" && req.Provider != "aws" {
			c.Error(&shared.DomainError{Code: "INVALID_REQUEST", Message: "provider must be gcp or aws", Status: 400})
			c.Abort()
			return
		}
		if req.Size == "" {
			req.Size = "medium"
		}
		inst, err := store.Create(req.Name, req.TenantID, req.Provider, req.Size)
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusAccepted, gin.H{
			"workflowId": inst.WorkflowID,
			"status":     "provisioning",
			"message":    "compute instance provisioning started",
		})
	})

	r.GET("/compute", func(c *gin.Context) {
		tenantID := c.Query("tenantId")
		if tenantID == "" {
			c.Error(&shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId query param is required", Status: 400})
			c.Abort()
			return
		}
		instances, err := store.List(tenantID, 20)
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": instances, "total": len(instances)})
	})

	r.GET("/compute/:name", func(c *gin.Context) {
		inst, err := store.Get(c.Param("name"))
		if err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.JSON(http.StatusOK, inst)
	})

	r.DELETE("/compute/:name", func(c *gin.Context) {
		if err := store.Delete(c.Param("name")); err != nil {
			c.Error(err)
			c.Abort()
			return
		}
		c.Status(http.StatusNoContent)
	})

	slog.Info("05-gin-standard listening", "port", 9005)
	r.Run(":9005")
}
