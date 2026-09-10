package validate

import (
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Against compiles the given JSON Schema (already a map[string]any, verbatim
// from an XRD's spec.versions[].schema.openAPIV3Schema...parameters) and
// validates obj against it. Returns the validator's error unchanged on
// failure so callers can surface it directly.
func Against(jsonSchema map[string]any, obj map[string]any) error {
	raw, err := json.Marshal(jsonSchema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	var schemaDoc any
	if err := json.Unmarshal(raw, &schemaDoc); err != nil {
		return fmt.Errorf("unmarshal schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	const resourceName = "params.json"
	if err := c.AddResource(resourceName, schemaDoc); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := c.Compile(resourceName)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}

	objRaw, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal object: %w", err)
	}
	var instance any
	if err := json.Unmarshal(objRaw, &instance); err != nil {
		return fmt.Errorf("unmarshal object: %w", err)
	}

	return compiled.Validate(instance)
}
