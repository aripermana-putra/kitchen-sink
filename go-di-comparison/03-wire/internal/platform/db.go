package platform

import (
	"context"
	"fmt"

	"github.com/kitchen-sink/di-shared"
)

type postgresDB struct {
	dsn  string
	role string // "read" or "write" — for logging only
}

// NewWriteDB constructs the write replica connection.
// Returns shared.DB interface — callers never import this package.
func NewWriteDB(cfg *shared.Config) (shared.DB, error) {
	fmt.Printf("[db] connected to write replica: %s\n", cfg.WriteDBURL)
	return &postgresDB{dsn: cfg.WriteDBURL, role: "write"}, nil
}

// NewReadDB constructs the read replica connection.
func NewReadDB(cfg *shared.Config) (shared.DB, error) {
	fmt.Printf("[db] connected to read replica: %s\n", cfg.ReadDBURL)
	return &postgresDB{dsn: cfg.ReadDBURL, role: "read"}, nil
}

func (db *postgresDB) Exec(_ context.Context, query string, args ...any) error {
	fmt.Printf("[db:%s] exec: %s\n", db.role, query)
	return nil
}

func (db *postgresDB) Query(_ context.Context, query string, args ...any) ([]map[string]any, error) {
	fmt.Printf("[db:%s] query: %s\n", db.role, query)
	return []map[string]any{}, nil
}
