package compute

import (
	"context"

	"github.com/kitchen-sink/di-shared"

	api "github.com/kitchen-sink/02-uber-fx/gen"
)

// Handler implements the compute subset of api.StrictServerInterface.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateCompute(ctx context.Context, req api.CreateComputeRequestObject) (api.CreateComputeResponseObject, error) {
	body := req.Body
	if body.Name == "" {
		return nil, shared.ErrInvalidRequest("name is required")
	}
	size := "medium"
	if body.Size != nil {
		size = string(*body.Size)
	}
	inst, err := h.svc.Provision(ctx, body.Name, body.TenantId, string(body.Provider), size)
	if err != nil {
		return nil, err
	}
	wfID, status, msg := inst.WorkflowID, "provisioning", "compute instance provisioning started"
	return api.CreateCompute202JSONResponse{WorkflowId: &wfID, Status: &status, Message: &msg}, nil
}

func (h *Handler) GetCompute(ctx context.Context, req api.GetComputeRequestObject) (api.GetComputeResponseObject, error) {
	inst, err := h.svc.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	name, status, wfID := inst.Name, inst.Status, inst.WorkflowID
	return api.GetCompute200JSONResponse{Name: &name, Status: &status, WorkflowId: &wfID}, nil
}
