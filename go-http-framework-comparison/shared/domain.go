// Package shared contains domain types and logic used by all 6 framework variants.
// Business logic is identical across variants — only the HTTP wiring differs.
package shared

import (
	"fmt"
	"sync"
)

// ComputeInstance is the domain model.
type ComputeInstance struct {
	Name       string
	TenantID   string
	Provider   string
	Size       string
	Status     string
	WorkflowID string
}

// DomainError carries a machine-readable code, HTTP status, and client-safe message.
// Internal cause is never serialized — only logged server-side.
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
	return &DomainError{Code: "NOT_FOUND", Message: fmt.Sprintf("compute instance %q not found", name), Status: 404}
}

func ErrAlreadyExists(name string) *DomainError {
	return &DomainError{Code: "ALREADY_EXISTS", Message: fmt.Sprintf("compute instance %q already exists", name), Status: 409}
}

func ErrAlreadyDeleting(name string) *DomainError {
	return &DomainError{Code: "ALREADY_DELETING", Message: fmt.Sprintf("compute instance %q is already being deleted", name), Status: 409}
}

func ErrInternal(cause error) *DomainError {
	return &DomainError{Code: "INTERNAL_ERROR", Message: "an internal error occurred", Status: 500, Cause: cause}
}

// Store is an in-memory compute instance store shared across all variants.
type Store struct {
	mu    sync.RWMutex
	items map[string]*ComputeInstance
}

func NewStore() *Store {
	return &Store{items: make(map[string]*ComputeInstance)}
}

func (s *Store) Create(name, tenantID, provider, size string) (*ComputeInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[name]; ok {
		return nil, ErrAlreadyExists(name)
	}
	inst := &ComputeInstance{
		Name:       name,
		TenantID:   tenantID,
		Provider:   provider,
		Size:       size,
		Status:     "provisioning",
		WorkflowID: fmt.Sprintf("wf-%s-001", name),
	}
	s.items[name] = inst
	return inst, nil
}

func (s *Store) List(tenantID string, limit int) ([]*ComputeInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ComputeInstance
	for _, inst := range s.items {
		if inst.TenantID == tenantID {
			result = append(result, inst)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *Store) Get(name string) (*ComputeInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.items[name]
	if !ok {
		return nil, ErrNotFound(name)
	}
	return inst, nil
}

func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.items[name]
	if !ok {
		return ErrNotFound(name)
	}
	if inst.Status == "deleting" {
		return ErrAlreadyDeleting(name)
	}
	inst.Status = "deleting"
	return nil
}
