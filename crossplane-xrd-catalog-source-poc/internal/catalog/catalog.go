// Package catalog holds the derived CatalogItem model and the in-memory
// cache that GET /catalog reads from. The cache is never written to on the
// request path — only the poller (see poller.go) replaces it.
package catalog

import "sync"

// Item is the derived catalog model this PoC caches and persists. It is
// parsed once, at poll time, from a CompositeResourceDefinition's
// catalog.ucp.io/* annotations — never the raw XRD shape itself.
type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

// Cache holds the current snapshot of derived Items behind a RWMutex. Every
// successful poll (or DB-seeded warm start) replaces the whole slice — it
// never mutates in place — so no reader ever observes a partial update.
type Cache struct {
	mu    sync.RWMutex
	items []Item
	ready bool
}

// Seed installs an initial snapshot (from the Platform DB, possibly empty)
// and marks the cache ready. Called once at startup, before the poller
// starts.
func (c *Cache) Seed(items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = items
	c.ready = true
}

// Swap atomically replaces the cached snapshot. Called by the poller after
// every successful LIST+parse cycle.
func (c *Cache) Swap(items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = items
	c.ready = true
}

// List returns the current snapshot. Safe for concurrent use.
func (c *Cache) List() []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Item, len(c.items))
	copy(out, c.items)
	return out
}

// Ready reports whether the cache has been seeded at least once (via DB
// warm start or a live poll).
func (c *Cache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}
