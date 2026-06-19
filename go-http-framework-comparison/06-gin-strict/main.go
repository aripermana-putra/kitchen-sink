// 06-gin-strict: Gin + oapi-codegen strict mode.
//
// KEY OBSERVATION — gin strict mode error handling:
// Similar to chi-strict: errors don't flow through gin middleware automatically.
// The generated strict wrapper uses StrictGinServerOptions with THREE callbacks:
//
//   RequestErrorHandlerFunc  — request body parse/bind failures
//   HandlerErrorFunc         — errors returned from handler functions (our DomainErrors)
//   ResponseErrorHandlerFunc — response serialization failures
//
// All three must be wired to get consistent error responses.
// If any is missed, gin's default returns {"msg": err.Error()} — leaking internal strings.
//
// Compare to echo-strict: ONE HTTPErrorHandler covers all cases automatically.
// Compare to chi-strict: ONE ResponseErrorHandlerFunc (chi uses http.ResponseWriter not *gin.Context).
//
// Gin-specific: options struct is StrictGinServerOptions; callbacks receive *gin.Context.
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	api "github.com/kitchen-sink/06-gin-strict/gen"
	"github.com/kitchen-sink/shared"
)

var _ api.StrictServerInterface = (*Handlers)(nil)

type Handlers struct{ store *shared.Store }

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

func toStatus(s string) api.ComputeInstanceStatus {
	return api.ComputeInstanceStatus(s)
}

func errorHandlerFunc(c *gin.Context, err error) {
	reqID, _ := c.Get("requestId")
	reqIDStr, _ := reqID.(string)
	var de *shared.DomainError
	if errors.As(err, &de) {
		if de.Cause != nil {
			slog.Error("domain error", "code", de.Code, "cause", de.Cause, "request_id", reqIDStr)
		}
		c.JSON(de.Status, ErrorResponse{Code: de.Code, Message: de.Message, RequestID: reqIDStr})
		return
	}
	slog.Error("unhandled error", "error", err, "request_id", reqIDStr)
	c.JSON(500, ErrorResponse{Code: "INTERNAL_ERROR", Message: "an internal error occurred", RequestID: reqIDStr})
}

func (h *Handlers) CreateCompute(ctx context.Context, req api.CreateComputeRequestObject) (api.CreateComputeResponseObject, error) {
	body := req.Body
	if body.Name == "" {
		return nil, &shared.DomainError{Code: "INVALID_REQUEST", Message: "name is required", Status: 400}
	}
	size := "medium"
	if body.Size != nil {
		size = string(*body.Size)
	}
	inst, err := h.store.Create(body.Name, body.TenantId, string(body.Provider), size)
	if err != nil {
		return nil, err
	}
	wfID, status, msg := inst.WorkflowID, "provisioning", "compute instance provisioning started"
	return api.CreateCompute202JSONResponse{WorkflowId: &wfID, Status: &status, Message: &msg}, nil
}

func (h *Handlers) ListCompute(ctx context.Context, req api.ListComputeRequestObject) (api.ListComputeResponseObject, error) {
	limit := 20
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	instances, err := h.store.List(req.Params.TenantId, limit)
	if err != nil {
		return nil, err
	}
	items := make([]api.ComputeInstance, 0, len(instances))
	for _, inst := range instances {
		name, tenantID, provider, size, status, wfID :=
			inst.Name, inst.TenantID, inst.Provider, inst.Size, toStatus(inst.Status), inst.WorkflowID
		items = append(items, api.ComputeInstance{Name: &name, TenantId: &tenantID, Provider: &provider, Size: &size, Status: &status, WorkflowId: &wfID})
	}
	total := len(items)
	return api.ListCompute200JSONResponse{Items: &items, Total: &total}, nil
}

func (h *Handlers) GetCompute(ctx context.Context, req api.GetComputeRequestObject) (api.GetComputeResponseObject, error) {
	inst, err := h.store.Get(req.Name)
	if err != nil {
		return nil, err
	}
	name, tenantID, provider, size, status, wfID :=
		inst.Name, inst.TenantID, inst.Provider, inst.Size, toStatus(inst.Status), inst.WorkflowID
	return api.GetCompute200JSONResponse{Name: &name, TenantId: &tenantID, Provider: &provider, Size: &size, Status: &status, WorkflowId: &wfID}, nil
}

func (h *Handlers) DeleteCompute(ctx context.Context, req api.DeleteComputeRequestObject) (api.DeleteComputeResponseObject, error) {
	if err := h.store.Delete(req.Name); err != nil {
		return nil, err
	}
	return api.DeleteCompute204Response{}, nil
}

func main() {
	store := shared.NewStore()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("requestId", uuid.New().String())
		c.Next()
	})

	opts := api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  errorHandlerFunc,
		HandlerErrorFunc:         errorHandlerFunc,
		ResponseErrorHandlerFunc: errorHandlerFunc,
	}
	api.RegisterHandlers(r, api.NewStrictHandlerWithOptions(&Handlers{store: store}, nil, opts))

	slog.Info("06-gin-strict listening", "port", 9006)
	r.Run(":9006")
}
