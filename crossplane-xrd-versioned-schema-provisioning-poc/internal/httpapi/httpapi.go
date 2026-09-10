package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/catalog"
	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-versioned-schema-provisioning-poc/internal/validate"
)

// XRTarget names the exact XR kind/group/resource a catalog entry's
// provision endpoint applies, since these aren't derivable from the catalog
// annotations alone (Kind capitalization isn't a mechanical transform of the
// plural resource name).
type XRTarget struct {
	Group    string
	Kind     string
	Resource string // plural, lowercase
}

type Server struct {
	cache     *catalog.Cache
	dyn       dynamic.Interface
	xrTargets map[string]XRTarget
}

func NewServer(cache *catalog.Cache, dyn dynamic.Interface, xrTargets map[string]XRTarget) *Server {
	return &Server{cache: cache, dyn: dyn, xrTargets: xrTargets}
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /catalog/{serviceId}", s.handleDescribe)
	mux.HandleFunc("GET /catalog/{serviceId}/template", s.handleTemplate)
	mux.HandleFunc("POST /catalog/{serviceId}/provision", s.handleProvision)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.cache.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type describeResponse struct {
	ServiceID        string                      `json:"serviceId"`
	ResourceGraph    []catalog.ResourceGraphNode `json:"resourceGraph"`
	StorageVersion   string                      `json:"storageVersion"`
	ParametersSchema map[string]any              `json:"parametersSchema"`
}

func (s *Server) handleDescribe(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("serviceId")
	es, ok := s.cache.GetEntrySchema(serviceID)
	if !ok {
		http.Error(w, "catalog entry not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, describeResponse{
		ServiceID:        es.ServiceID,
		ResourceGraph:    es.ResourceGraph,
		StorageVersion:   es.StorageVersion,
		ParametersSchema: es.Versions[es.StorageVersion],
	})
}

func (s *Server) handleTemplate(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("serviceId")
	es, ok := s.cache.GetEntrySchema(serviceID)
	if !ok {
		http.Error(w, "catalog entry not found", http.StatusNotFound)
		return
	}
	params := es.Versions[es.StorageVersion]

	var b strings.Builder
	fmt.Fprintf(&b, "# schemaVersion (fixed) — schema version this template was generated against; leave as-is\n")
	fmt.Fprintf(&b, "schemaVersion: %s\n", es.StorageVersion)
	fmt.Fprintf(&b, "# name (required) — resource name\n")
	fmt.Fprintf(&b, "name: \"\"\n")

	groups, _ := params["properties"].(map[string]any)
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		group, _ := groups[groupName].(map[string]any)
		props, _ := group["properties"].(map[string]any)
		if len(props) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", groupName)

		propNames := make([]string, 0, len(props))
		for name := range props {
			propNames = append(propNames, name)
		}
		sort.Strings(propNames)

		for _, propName := range propNames {
			prop, _ := props[propName].(map[string]any)
			desc, _ := prop["description"].(string)
			def := prop["default"]
			if desc != "" {
				fmt.Fprintf(&b, "  # %s\n", desc)
			}
			if def != nil {
				fmt.Fprintf(&b, "  %s: %v\n", propName, def)
			} else {
				fmt.Fprintf(&b, "  %s: \"\"\n", propName)
			}
		}
	}

	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(b.String()))
}

type provisionRequest struct {
	Name          string         `json:"name"`
	SchemaVersion string         `json:"schemaVersion"`
	Parameters    map[string]any `json:"parameters"`
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("serviceId")
	es, ok := s.cache.GetEntrySchema(serviceID)
	if !ok {
		http.Error(w, "catalog entry not found", http.StatusNotFound)
		return
	}

	var req provisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	versionSchema, ok := es.Versions[req.SchemaVersion]
	if !ok {
		http.Error(w, fmt.Sprintf("schemaVersion %q is not a served version of %q", req.SchemaVersion, serviceID), http.StatusNotFound)
		return
	}

	if err := validate.Against(versionSchema, req.Parameters); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "validation failed",
			"detail": err.Error(),
		})
		return
	}

	target, ok := s.xrTargets[serviceID]
	if !ok {
		http.Error(w, fmt.Sprintf("no XR target registered for %q", serviceID), http.StatusInternalServerError)
		return
	}

	xr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": fmt.Sprintf("%s/%s", target.Group, req.SchemaVersion),
			"kind":       target.Kind,
			"metadata": map[string]any{
				"name":      req.Name,
				"namespace": "default",
			},
			"spec": map[string]any{
				"parameters": req.Parameters,
			},
		},
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: req.SchemaVersion, Resource: target.Resource}
	created, err := s.dyn.Resource(gvr).Namespace("default").Create(r.Context(), xr, metav1.CreateOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf("apply failed: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"name":          created.GetName(),
		"apiVersion":    created.GetAPIVersion(),
		"kind":          created.GetKind(),
		"schemaVersion": req.SchemaVersion,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
