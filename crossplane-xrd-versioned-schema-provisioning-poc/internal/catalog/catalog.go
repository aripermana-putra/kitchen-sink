package catalog

import "sync"

// Cache holds the latest derived catalog snapshot. Reads copy out from behind
// a RWMutex; writes always replace the whole slice/map, never mutate in place,
// matching the prior crossplane-xrd-catalog-source PoC's convention.
type Cache struct {
	mu           sync.RWMutex
	items        []Item
	entrySchemas map[string]EntrySchema
	ready        bool
}

func NewCache() *Cache {
	return &Cache{entrySchemas: map[string]EntrySchema{}}
}

func (c *Cache) Seed(items []Item, schemas map[string]EntrySchema) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = items
	c.entrySchemas = schemas
	c.ready = true
}

func (c *Cache) Swap(items []Item, schemas map[string]EntrySchema) {
	c.Seed(items, schemas)
}

func (c *Cache) List() []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Item, len(c.items))
	copy(out, c.items)
	return out
}

func (c *Cache) GetEntrySchema(serviceID string) (EntrySchema, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	es, ok := c.entrySchemas[serviceID]
	return es, ok
}

func (c *Cache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}
