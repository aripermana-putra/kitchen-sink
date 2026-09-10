package catalog

import (
	"context"
	"log"
	"time"
)

// Lister lists the current set of catalog items and their derived schemas
// from Crossplane.
type Lister interface {
	ListCatalogItems(ctx context.Context, timeout time.Duration) ([]Item, map[string]EntrySchema, error)
}

// Poller runs an immediate first poll, then polls on a fixed interval.
// A successful poll atomically swaps the cache. A failed poll logs the error
// and keeps serving the last-good snapshot.
func Poller(ctx context.Context, cache *Cache, lister Lister, pollTimeout, interval time.Duration) {
	poll := func() {
		items, schemas, err := lister.ListCatalogItems(ctx, pollTimeout)
		if err != nil {
			log.Printf("poll failed, keeping last-good snapshot: %v", err)
			return
		}
		cache.Swap(items, schemas)
		log.Printf("poll succeeded: %d items, %d entry schemas", len(items), len(schemas))
	}

	poll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
