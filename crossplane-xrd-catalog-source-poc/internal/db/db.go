// Package db persists the derived catalog snapshot to the Platform DB and
// reads it back at startup. It only ever stores/reads catalog.Item — never
// the raw XRD.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aripermana-putra/kitchen-sink/crossplane-xrd-catalog-source-poc/internal/catalog"
)

const singletonSnapshotID = 1

// Store wraps a Postgres connection pool. The derived snapshot is stored as
// a single JSON blob under a fixed ID — this PoC's Open Question about
// one-row-per-item vs. one-JSON-blob is resolved here as the latter, since
// the whole list is always replaced atomically together (see design.md).
type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping platform db: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS catalog_snapshot (
			id INT PRIMARY KEY,
			items JSONB NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

// Upsert writes the full derived item list as a single JSON blob, replacing
// whatever was there before. Called after every successful poll — safe to
// call concurrently from multiple pods, since it's an idempotent write of
// the same source data.
func (s *Store) Upsert(ctx context.Context, items []catalog.Item) error {
	payload, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("marshal items: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO catalog_snapshot (id, items, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (id) DO UPDATE SET items = $2, updated_at = now()
	`, singletonSnapshotID, payload)
	return err
}

// LatestSnapshot reads the most recent snapshot for the startup warm start.
// A missing row is not an error — it returns an empty, non-nil slice, since
// "no snapshot yet" is a valid cold-start state (see design.md).
func (s *Store) LatestSnapshot(ctx context.Context) ([]catalog.Item, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT items FROM catalog_snapshot WHERE id = $1
	`, singletonSnapshotID).Scan(&payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []catalog.Item{}, nil
		}
		return nil, fmt.Errorf("query latest snapshot: %w", err)
	}
	var items []catalog.Item
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return items, nil
}
