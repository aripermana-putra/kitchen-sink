// Package shared contains interfaces and types used across all DI variants.
// Feature slices depend on these interfaces — never on concrete implementations.
// This enforces the dependency inversion principle: high-level policy (feature slices)
// depends on abstractions, not on infrastructure (platform layer).
package shared

import (
	"context"
	"fmt"
)

// ── Config ────────────────────────────────────────────────────────────────

type Config struct {
	Port           string
	K8sKubeconfig  string
	TemporalHost   string
}

func LoadConfig() *Config {
	return &Config{
		Port:          "8080",
		K8sKubeconfig: "/etc/kubeconfig",
		TemporalHost:  "temporal:7233",
	}
}

// ── Platform interfaces ───────────────────────────────────────────────────
// Defined here so feature slices can depend on abstractions.
// Implementations live in internal/platform/ — never imported by feature slices.

type K8sClient interface {
	Apply(ctx context.Context, resource string, spec []byte) error
	Get(ctx context.Context, resource, name string) ([]byte, error)
}

type TemporalClient interface {
	StartWorkflow(ctx context.Context, workflowType, workflowID string, input any) (string, error)
}

// ── Domain types ──────────────────────────────────────────────────────────

type ComputeInstance struct {
	Name       string
	TenantID   string
	Provider   string
	Size       string
	Status     string
	WorkflowID string
}

type DatabaseInstance struct {
	Name       string
	TenantID   string
	Engine     string
	Tier       string
	Status     string
	WorkflowID string
}

// ── Error types ───────────────────────────────────────────────────────────

type DomainError struct {
	Code    string
	Message string
	Status  int
	Cause   error
}

func (e *DomainError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	return e.Code
}

func ErrNotFound(name string) *DomainError {
	return &DomainError{Code: "NOT_FOUND", Message: fmt.Sprintf("%q not found", name), Status: 404}
}

func ErrInvalidRequest(msg string) *DomainError {
	return &DomainError{Code: "INVALID_REQUEST", Message: msg, Status: 400}
}

func ErrInternal(cause error) *DomainError {
	return &DomainError{Code: "INTERNAL_ERROR", Message: "an internal error occurred", Status: 500, Cause: cause}
}
