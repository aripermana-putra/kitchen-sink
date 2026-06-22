package compute

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

type Service struct {
	k8s      shared.K8sClient
	temporal shared.TemporalClient
}

func NewService(k8s shared.K8sClient, temporal shared.TemporalClient) *Service {
	return &Service{k8s: k8s, temporal: temporal}
}

func (s *Service) Provision(ctx context.Context, name, tenantID, provider, size string) (*shared.ComputeInstance, error) {
	workflowID := fmt.Sprintf("compute-%s-%s", tenantID, name)
	if _, err := s.temporal.StartWorkflow(ctx, "ProvisionCompute", workflowID, map[string]string{
		"name": name, "tenantId": tenantID, "provider": provider, "size": size,
	}); err != nil {
		return nil, shared.ErrInternal(err)
	}
	return &shared.ComputeInstance{
		Name: name, TenantID: tenantID, Provider: provider,
		Size: size, Status: "provisioning", WorkflowID: workflowID,
	}, nil
}

func (s *Service) Get(ctx context.Context, name string) (*shared.ComputeInstance, error) {
	data, err := s.k8s.Get(ctx, "xcomputeinstances", name)
	if err != nil {
		return nil, shared.ErrNotFound(name)
	}
	_ = data
	return &shared.ComputeInstance{Name: name, Status: "running"}, nil
}
