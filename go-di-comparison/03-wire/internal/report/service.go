// Package report demonstrates the "multiple same-type dependencies" pattern.
//
// SCENARIO: dbWrite and dbRead are both shared.DB but serve different purposes.
// The service declares two separate fields of the same interface type.
// At the constructor level, order/naming makes the intent clear.
//
// MANUAL vs FX CONTRAST:
//   Manual — straightforward positional args, compiler enforces types:
//     report.NewService(dbWrite, dbRead)
//
//   uber/fx — same interface type causes ambiguity, requires annotation tags:
//     fx.Provide(fx.Annotate(NewWriteDB, fx.ResultTags(`name:"write"`)))
//     fx.Provide(fx.Annotate(NewReadDB,  fx.ResultTags(`name:"read"`)))
//     // And in the struct:
//     type Params struct {
//         fx.In
//         Write shared.DB `name:"write"`
//         Read  shared.DB `name:"read"`
//     }
//
//   wire — requires separate provider functions that return distinct named types
//   or wrapper types to disambiguate (adds boilerplate).
package report

import (
	"context"

	"github.com/kitchen-sink/di-shared"
)

type Service struct {
	dbWrite shared.DB // write replica — for audit log writes
	dbRead  shared.DB // read replica  — for report queries
}

// NewService takes both DB instances as separate parameters.
// The caller (main.go) is responsible for passing them in the right order.
// Manual injection: the intent is clear from the parameter names.
func NewService(dbWrite shared.DB, dbRead shared.DB) *Service {
	return &Service{dbWrite: dbWrite, dbRead: dbRead}
}

func (s *Service) ListReports(ctx context.Context, tenantID string) ([]*shared.ReportEntry, error) {
	// reads go to the read replica
	rows, err := s.dbRead.Query(ctx,
		"SELECT resource_name, tenant_id, provider, status FROM resources WHERE tenant_id = $1",
		tenantID,
	)
	if err != nil {
		return nil, shared.ErrInternal(err)
	}
	entries := make([]*shared.ReportEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, &shared.ReportEntry{
			ResourceName: row["resource_name"].(string),
			TenantID:     tenantID,
		})
	}
	return entries, nil
}

func (s *Service) WriteAuditLog(ctx context.Context, entry *shared.ReportEntry) error {
	// writes go to the write replica
	return s.dbWrite.Exec(ctx,
		"INSERT INTO audit_log (resource_name, tenant_id, provider, status) VALUES ($1, $2, $3, $4)",
		entry.ResourceName, entry.TenantID, entry.Provider, entry.Status,
	)
}
