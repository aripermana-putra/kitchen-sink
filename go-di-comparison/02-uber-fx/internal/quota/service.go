// Package quota demonstrates the "runtime strategy selection" pattern.
//
// SCENARIO: QuotaChecker has multiple implementations (GCP, AWS, ROC).
// The right implementation is selected per request based on req.Provider.
// All implementations are constructed at startup and stored in a map.
//
// This is Go's equivalent of Spring Boot's @Qualifier — but instead of
// annotations, the map key serves as the qualifier.
//
// ALL THREE DI APPROACHES wire the map identically in main.go:
//   quotaSvc := quota.NewService(map[string]shared.QuotaChecker{
//       "gcp": gcp.NewGCPQuotaChecker(cfg),
//       "aws": aws.NewAWSQuotaChecker(cfg),
//       "roc": roc.NewROCQuotaChecker(cfg),
//   })
//
// The service itself is identical across all three variants — the map dispatch
// is business logic, not a DI concern. DI just constructs the map at startup.
package quota

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

// QuotaCheckers is a named type for the map to make the constructor signature clear.
// Without this, main.go would pass map[string]shared.QuotaChecker{} directly
// which is fine but less readable.
type QuotaCheckers map[string]shared.QuotaChecker

type Service struct {
	checkers QuotaCheckers
}

func NewService(checkers QuotaCheckers) *Service {
	return &Service{checkers: checkers}
}

func (s *Service) Check(ctx context.Context, tenantID, provider, resourceType string) (*shared.QuotaResult, error) {
	checker, ok := s.checkers[provider]
	if !ok {
		return nil, shared.ErrInvalidRequest(fmt.Sprintf("unsupported provider: %s", provider))
	}
	if err := checker.Check(ctx, tenantID, resourceType); err != nil {
		return &shared.QuotaResult{
			TenantID:     tenantID,
			Provider:     provider,
			ResourceType: resourceType,
			Allowed:      false,
			Message:      err.Error(),
		}, nil
	}
	return &shared.QuotaResult{
		TenantID:     tenantID,
		Provider:     provider,
		ResourceType: resourceType,
		Allowed:      true,
		Message:      "quota available",
	}, nil
}
