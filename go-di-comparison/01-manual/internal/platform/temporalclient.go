package platform

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

type temporalClient struct {
	host string
}

// NewTemporalClient constructs the Temporal client from config.
func NewTemporalClient(cfg *shared.Config) shared.TemporalClient {
	return &temporalClient{host: cfg.TemporalHost}
}

func (c *temporalClient) StartWorkflow(_ context.Context, workflowType, workflowID string, input any) (string, error) {
	fmt.Printf("[temporal] start %s id=%s\n", workflowType, workflowID)
	return workflowID, nil
}
