package catalog

// ResourceGraphNode is one entry in an XRD's catalog.ucp.io/resource-graph annotation.
type ResourceGraphNode struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	DependsOn  []string `json:"dependsOn,omitempty"`
}

// Item is the catalog metadata derived from an XRD's catalog.ucp.io/* annotations,
// matching MCUCP-144's model.
type Item struct {
	ServiceID   string
	Name        string
	Provider    string
	Category    string
	Description string
}

// EntrySchema is the per-served-version JSON Schema derived from an XRD's
// spec.versions[], matching MCUCP-145's TRD model.
type EntrySchema struct {
	ServiceID     string
	ResourceGraph []ResourceGraphNode
	// Versions is keyed by spec.versions[].name; each value is that version's
	// parameters schema copied verbatim (a full JSON Schema object).
	Versions map[string]map[string]any
	// StorageVersion is the spec.versions[] entry name whose CRD-generated
	// storage flag is true (the version Crossplane keeps in etcd). In a real
	// Crossplane v2 XRD this is driven by referenceable: true, not a literal
	// "storage" field on the XRD version entry itself.
	StorageVersion string
}
