package database

import (
	"context"

	"github.com/kitchen-sink/di-shared"

	api "github.com/kitchen-sink/01-manual/gen"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateDatabase(ctx context.Context, req api.CreateDatabaseRequestObject) (api.CreateDatabaseResponseObject, error) {
	body := req.Body
	if body.Name == "" {
		return nil, shared.ErrInvalidRequest("name is required")
	}
	tier := "medium"
	if body.Tier != nil {
		tier = string(*body.Tier)
	}
	inst, err := h.svc.Provision(ctx, body.Name, body.TenantId, string(body.Engine), tier)
	if err != nil {
		return nil, err
	}
	wfID, status, msg := inst.WorkflowID, "provisioning", "database provisioning started"
	return api.CreateDatabase202JSONResponse{WorkflowId: &wfID, Status: &status, Message: &msg}, nil
}

func (h *Handler) GetDatabase(ctx context.Context, req api.GetDatabaseRequestObject) (api.GetDatabaseResponseObject, error) {
	inst, err := h.svc.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	name, status, wfID := inst.Name, inst.Status, inst.WorkflowID
	return api.GetDatabase200JSONResponse{Name: &name, Status: &status, WorkflowId: &wfID}, nil
}
