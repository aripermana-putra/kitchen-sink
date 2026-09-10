package catalog

import (
	"context"
	"log"
	"time"
)

// Lister LISTs the remote Crossplane cluster and derives Items. Satisfied
// by xrdclient.Client — kept as an interface here so this package doesn't
// depend on Kubernetes client-go directly.
type Lister interface {
	ListCatalogItems(ctx context.Context) ([]Item, error)
}

// Persister writes the derived snapshot to the Platform DB. Satisfied by
// db.Store.
type Persister interface {
	Upsert(ctx context.Context, items []Item) error
}

// Poller runs the fixed-interval LIST-and-derive loop against the remote
// Crossplane cluster, atomically swapping the result into cache and writing
// it back to the Platform DB on every success. A failed poll logs and
// leaves the cache untouched — it never clears it.
func Poller(ctx context.Context, cache *Cache, lister Lister, persister Persister, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	poll := func() {
		items, err := lister.ListCatalogItems(ctx)
		if err != nil {
			log.Printf("poll failed, serving last-good snapshot: %v", err)
			return
		}
		cache.Swap(items)
		if err := persister.Upsert(ctx, items); err != nil {
			log.Printf("failed to persist snapshot to platform db: %v", err)
		}
	}

	poll() // first poll runs immediately, not after the first tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
