// Package showskip provides a TTL-based cache for show-level subtitle
// pre-check results. It avoids repeating expensive API calls for the same
// series on every scan cycle.
package showskip

import (
	"sync"
	"time"
)

// result holds a cached pre-check result with expiry.
type result struct {
	expires time.Time
	skip    bool
}

// Cache caches show-level subtitle count pre-check results across scan
// batches with a TTL.
type Cache struct {
	entries map[string]result
	ttl     time.Duration
	mu      sync.RWMutex
}

// New creates a cache with the given TTL.
func New(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]result),
		ttl:     ttl,
	}
}

// Get returns the cached skip decision if present and not expired.
func (c *Cache) Get(key string) (skip, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.entries[key]
	if !ok || time.Now().After(r.expires) {
		return false, false
	}
	return r.skip, true
}

// Set stores a skip decision with TTL-based expiry.
func (c *Cache) Set(key string, skip bool) {
	c.mu.Lock()
	c.entries[key] = result{skip: skip, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// Clear removes all cached entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]result)
	c.mu.Unlock()
}

// Prune removes expired entries from the cache, reclaiming memory.
func (c *Cache) Prune() {
	c.mu.Lock()
	now := time.Now()
	for k, r := range c.entries {
		if now.After(r.expires) {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}
