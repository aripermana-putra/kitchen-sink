package platform

import "github.com/kitchen-sink/di-shared"

// WriteDB and ReadDB are wrapper types so wire can distinguish two shared.DB
// instances. Without these, wire would see two providers for the same type
// and fail at generate time.
//
// This is wire's approach to the @Qualifier problem:
// - fx uses string name tags (runtime metadata)
// - wire uses distinct types (compile-time)
//
// The wrapper type approach is more type-safe but adds boilerplate.
// Compare to manual injection which simply uses positional args:
//   report.NewService(dbWrite, dbRead) — no wrapper types needed.

type WriteDB shared.DB
type ReadDB shared.DB

func NewWriteDBWrapped(cfg *shared.Config) (WriteDB, error) {
	db, err := NewWriteDB(cfg)
	return WriteDB(db), err
}

func NewReadDBWrapped(cfg *shared.Config) (ReadDB, error) {
	db, err := NewReadDB(cfg)
	return ReadDB(db), err
}
