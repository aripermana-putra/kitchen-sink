package platform

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

type k8sClient struct {
	kubeconfig string
}

// NewK8sClient constructs the K8s client from config.
// Returns the shared.K8sClient interface — feature slices never import this package.
func NewK8sClient(cfg *shared.Config) shared.K8sClient {
	return &k8sClient{kubeconfig: cfg.K8sKubeconfig}
}

func (c *k8sClient) Apply(_ context.Context, resource string, spec []byte) error {
	fmt.Printf("[k8s] apply %s (%d bytes)\n", resource, len(spec))
	return nil
}

func (c *k8sClient) Get(_ context.Context, resource, name string) ([]byte, error) {
	fmt.Printf("[k8s] get %s/%s\n", resource, name)
	return []byte(`{"status":"running"}`), nil
}
