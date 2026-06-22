package quota

import (
	"context"

	api "github.com/kitchen-sink/02-uber-fx/gen"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CheckQuota(ctx context.Context, req api.CheckQuotaRequestObject) (api.CheckQuotaResponseObject, error) {
	body := req.Body
	result, err := h.svc.Check(ctx, body.TenantId, string(body.Provider), body.ResourceType)
	if err != nil {
		return nil, err
	}
	allowed, msg := result.Allowed, result.Message
	return api.CheckQuota200JSONResponse{Allowed: &allowed, Message: &msg}, nil
}
