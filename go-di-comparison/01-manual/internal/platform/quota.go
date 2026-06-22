package platform

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

// ── GCP quota checker ─────────────────────────────────────────────────────

type gcpQuotaChecker struct{ cfg *shared.Config }

func NewGCPQuotaChecker(cfg *shared.Config) shared.QuotaChecker {
	return &gcpQuotaChecker{cfg: cfg}
}

func (c *gcpQuotaChecker) Check(_ context.Context, tenantID, resourceType string) error {
	fmt.Printf("[quota:gcp] checking %s for tenant %s\n", resourceType, tenantID)
	// real impl: call GCP quotas API
	return nil
}

// ── AWS quota checker ─────────────────────────────────────────────────────

type awsQuotaChecker struct{ cfg *shared.Config }

func NewAWSQuotaChecker(cfg *shared.Config) shared.QuotaChecker {
	return &awsQuotaChecker{cfg: cfg}
}

func (c *awsQuotaChecker) Check(_ context.Context, tenantID, resourceType string) error {
	fmt.Printf("[quota:aws] checking %s for tenant %s\n", resourceType, tenantID)
	// real impl: call AWS service quotas API
	return nil
}

// ── ROC quota checker ─────────────────────────────────────────────────────

type rocQuotaChecker struct{ cfg *shared.Config }

func NewROCQuotaChecker(cfg *shared.Config) shared.QuotaChecker {
	return &rocQuotaChecker{cfg: cfg}
}

func (c *rocQuotaChecker) Check(_ context.Context, tenantID, resourceType string) error {
	fmt.Printf("[quota:roc] checking %s for tenant %s\n", resourceType, tenantID)
	// real impl: call ROC limits API
	return nil
}
