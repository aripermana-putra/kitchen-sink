package report

import (
	"context"

	api "github.com/kitchen-sink/03-wire/gen"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetReport(ctx context.Context, req api.GetReportRequestObject) (api.GetReportResponseObject, error) {
	tenantID := req.Params.TenantId
	entries, err := h.svc.ListReports(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	items := make([]api.ReportEntry, 0, len(entries))
	for _, e := range entries {
		name, provider, status := e.ResourceName, e.Provider, e.Status
		items = append(items, api.ReportEntry{
			ResourceName: &name,
			Provider:     &provider,
			Status:       &status,
		})
	}
	return api.GetReport200JSONResponse{Items: &items}, nil
}
