package xrdclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/catalog"
)

var xrdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.crossplane.io",
	Version:  "v1",
	Resource: "compositeresourcedefinitions",
}

type Client struct {
	dyn dynamic.Interface
}

func New(kubeconfigPath string) (*Client, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return &Client{dyn: dyn}, nil
}

// ListCatalogItems lists every catalog-eligible XRD (catalog.ucp.io/enabled=true)
// and derives both its Item metadata and its EntrySchema in the same LIST call.
func (c *Client) ListCatalogItems(ctx context.Context, timeout time.Duration) ([]catalog.Item, map[string]catalog.EntrySchema, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	list, err := c.dyn.Resource(xrdGVR).List(ctx, metav1.ListOptions{
		LabelSelector: "catalog.ucp.io/enabled=true",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list compositeresourcedefinitions: %w", err)
	}

	items := make([]catalog.Item, 0, len(list.Items))
	schemas := make(map[string]catalog.EntrySchema, len(list.Items))

	for _, obj := range list.Items {
		item, ok := deriveItem(&obj)
		if !ok {
			continue
		}
		items = append(items, item)

		es, ok := deriveEntrySchema(&obj, item.ServiceID)
		if !ok {
			continue
		}
		schemas[item.ServiceID] = es
	}

	return items, schemas, nil
}

func deriveItem(obj *unstructured.Unstructured) (catalog.Item, bool) {
	ann := obj.GetAnnotations()
	serviceID := ann["catalog.ucp.io/service-id"]
	if serviceID == "" {
		return catalog.Item{}, false
	}
	return catalog.Item{
		ServiceID:   serviceID,
		Name:        ann["catalog.ucp.io/name"],
		Provider:    ann["catalog.ucp.io/provider"],
		Category:    ann["catalog.ucp.io/category"],
		Description: ann["catalog.ucp.io/description"],
	}, true
}

func deriveEntrySchema(obj *unstructured.Unstructured, serviceID string) (catalog.EntrySchema, bool) {
	ann := obj.GetAnnotations()
	graphRaw, ok := ann["catalog.ucp.io/resource-graph"]
	if !ok {
		return catalog.EntrySchema{}, false
	}

	var graph []catalog.ResourceGraphNode
	if err := json.Unmarshal([]byte(graphRaw), &graph); err != nil {
		return catalog.EntrySchema{}, false
	}

	versions, found, err := unstructured.NestedSlice(obj.Object, "spec", "versions")
	if err != nil || !found {
		return catalog.EntrySchema{}, false
	}

	es := catalog.EntrySchema{
		ServiceID:     serviceID,
		ResourceGraph: graph,
		Versions:      map[string]map[string]any{},
	}

	for _, v := range versions {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(vm, "name")
		served, _, _ := unstructured.NestedBool(vm, "served")
		if name == "" || !served {
			continue
		}
		params, found, err := unstructured.NestedMap(vm, "schema", "openAPIV3Schema", "properties", "spec", "properties", "parameters")
		if err != nil || !found {
			continue
		}
		es.Versions[name] = params
	}

	// Crossplane v2 XRDs mark the storage version via referenceable: true on
	// that spec.versions[] entry (exactly one is required), not a literal
	// "storage" field on the XRD's own version entry.
	for _, v := range versions {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(vm, "name")
		referenceable, _, _ := unstructured.NestedBool(vm, "referenceable")
		if referenceable {
			es.StorageVersion = name
			break
		}
	}

	if es.StorageVersion == "" || len(es.Versions) == 0 {
		return catalog.EntrySchema{}, false
	}

	return es, true
}
