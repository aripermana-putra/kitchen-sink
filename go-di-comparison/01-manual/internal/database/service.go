package database

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

func (s *Service) Provision(ctx context.Context, name, tenantID, engine, tier string) (*shared.DatabaseInstance, error) {
	workflowID := fmt.Sprintf("database-%s-%s", tenantID, name)
	if _, err := s.temporal.StartWorkflow(ctx, "ProvisionDatabase", workflowID, map[string]string{
		"name": name, "tenantId": tenantID, "engine": engine, "tier": tier,
	}); err != nil {
		return nil, shared.ErrInternal(err)
	}
	return &shared.DatabaseInstance{
		Name: name, TenantID: tenantID, Engine: engine,
		Tier: tier, Status: "provisioning", WorkflowID: workflowID,
	}, nil
}

func (s *Service) Get(ctx context.Context, name string) (*shared.DatabaseInstance, error) {
	data, err := s.k8s.Get(ctx, "xdatabases", name)
	if err != nil {
		return nil, shared.ErrNotFound(name)
	}
	_ = data
	return &shared.DatabaseInstance{Name: name, Status: "running"}, nil
}
