// Package xrdclient lists CompositeResourceDefinitions on a remote
// Crossplane cluster and parses their catalog.ucp.io/* annotations into
// catalog.Item values. The client is built from an explicit kubeconfig
// file — never rest.InClusterConfig() — since the Crossplane cluster is not
// the cluster this process runs on.
package xrdclient

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-catalog-source-poc/internal/catalog"
)

var xrdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.crossplane.io",
	Version:  "v1",
	Resource: "compositeresourcedefinitions",
}

const (
	labelEnabledSelector  = "catalog.ucp.io/enabled=true"
	annotationServiceID   = "catalog.ucp.io/service-id"
	annotationName        = "catalog.ucp.io/name"
	annotationProvider    = "catalog.ucp.io/provider"
	annotationCategory    = "catalog.ucp.io/category"
	annotationDescription = "catalog.ucp.io/description"
)

// Client lists catalog-annotated XRDs on a remote cluster.
type Client struct {
	dyn dynamic.Interface
}

// New builds a Client from a standalone kubeconfig file — API server URL,
// CA cert, and a ServiceAccount token — the same pattern Argo CD and
// Cluster API use to manage remote clusters.
func New(kubeconfigPath string) (*Client, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("build rest.Config from kubeconfig %q: %w", kubeconfigPath, err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return &Client{dyn: dyn}, nil
}

// ListCatalogItems LISTs CompositeResourceDefinitions labeled
// catalog.ucp.io/enabled=true and derives a catalog.Item from each one's
// annotations. This is the only place the raw XRD shape is touched — the
// derived Item is all that ever leaves this function.
func (c *Client) ListCatalogItems(ctx context.Context) ([]catalog.Item, error) {
	list, err := c.dyn.Resource(xrdGVR).List(ctx, metav1.ListOptions{
		LabelSelector: labelEnabledSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list compositeresourcedefinitions: %w", err)
	}

	items := make([]catalog.Item, 0, len(list.Items))
	for _, obj := range list.Items {
		item, ok := deriveItem(&obj)
		if !ok {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func deriveItem(obj *unstructured.Unstructured) (catalog.Item, bool) {
	ann := obj.GetAnnotations()
	id, ok := ann[annotationServiceID]
	if !ok || id == "" {
		return catalog.Item{}, false
	}
	return catalog.Item{
		ID:          id,
		Name:        ann[annotationName],
		Provider:    ann[annotationProvider],
		Category:    ann[annotationCategory],
		Description: ann[annotationDescription],
	}, true
}
