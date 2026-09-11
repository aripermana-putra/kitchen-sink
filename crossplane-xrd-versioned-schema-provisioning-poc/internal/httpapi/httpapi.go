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

type Server struct {
	cache *catalog.Cache
	dyn   dynamic.Interface
}

func NewServer(cache *catalog.Cache, dyn dynamic.Interface) *Server {
	return &Server{cache: cache, dyn: dyn}
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
	fmt.Fprintf(&b, "# schemaVersion (fixed) — XRD version this template was generated against; leave as-is\n")
	fmt.Fprintf(&b, "schemaVersion: %s\n", es.StorageVersion)
	fmt.Fprintf(&b, "# name (required) — Resource name\n")
	fmt.Fprintf(&b, "name: \"\"\n")

	groups, _ := params["properties"].(map[string]any)

	// Group order matches MCUCP-145's worked --generate-template example:
	// "shared" first (if present), then one per resource-graph item in graph
	// order, then any remaining group name (not expected in practice) sorted
	// alphabetically as a fallback.
	ordered := make([]string, 0, len(groups))
	seen := map[string]bool{}
	if _, ok := groups["shared"]; ok {
		ordered = append(ordered, "shared")
		seen["shared"] = true
	}
	for _, node := range es.ResourceGraph {
		if _, ok := groups[node.Name]; ok && !seen[node.Name] {
			ordered = append(ordered, node.Name)
			seen[node.Name] = true
		}
	}
	var remaining []string
	for name := range groups {
		if !seen[name] {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)

	for _, groupName := range ordered {
		group, _ := groups[groupName].(map[string]any)
		props, _ := group["properties"].(map[string]any)
		if len(props) == 0 {
			continue
		}

		required := map[string]bool{}
		if reqList, ok := group["required"].([]any); ok {
			for _, req := range reqList {
				if name, ok := req.(string); ok {
					required[name] = true
				}
			}
		}

		fmt.Fprintf(&b, "\n%s:\n", groupName)

		propNames := make([]string, 0, len(props))
		for name := range props {
			propNames = append(propNames, name)
		}
		sort.Strings(propNames)

		for _, propName := range propNames {
			prop, _ := props[propName].(map[string]any)
			desc, _ := prop["description"].(string)
			def := prop["default"]
			example := prop["example"]

			reqWord := "optional"
			if required[propName] {
				reqWord = "required"
			}

			line := fmt.Sprintf("  # %s (%s)", propName, reqWord)
			if desc != "" {
				line += fmt.Sprintf(" — %s", desc)
			}
			if def != nil {
				line += fmt.Sprintf(" (default: %v)", def)
			}
			fmt.Fprintf(&b, "%s\n", line)

			if example != nil {
				if s, ok := example.(string); ok {
					fmt.Fprintf(&b, "  #   example: %q\n", s)
				} else {
					fmt.Fprintf(&b, "  #   example: %v\n", example)
				}
			}

			fmt.Fprintf(&b, "  %s: \"\"\n", propName)
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

	xr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": fmt.Sprintf("%s/%s", es.Group, req.SchemaVersion),
			"kind":       es.Kind,
			"metadata": map[string]any{
				"name":      req.Name,
				"namespace": "default",
			},
			"spec": map[string]any{
				"parameters": req.Parameters,
			},
		},
	}

	gvr := schema.GroupVersionResource{Group: es.Group, Version: req.SchemaVersion, Resource: es.Resource}
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
