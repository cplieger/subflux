// Package cache provides a generic TTL cache with singleflight coalescing.
package cache

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultTTL is the standard TTL for provider lookup caches.
// Used by providers that cache title/show ID lookups across scan cycles.
const DefaultTTL = 6 * time.Hour

// typedGroup wraps singleflight.Group to provide a type-safe Do without
// requiring callers to perform unsafe type assertions.
type typedGroup[T any] struct {
	g singleflight.Group
}

func (tg *typedGroup[T]) Do(key string, fn func() (T, error)) (val T, shared bool, err error) {
	v, err, shared := tg.g.Do(key, func() (any, error) { return fn() })
	if err != nil {
		var zero T
		return zero, shared, err
	}
	val, _ = v.(T)
	return val, shared, nil
}

// Cache is a generic TTL cache for provider lookups. Thread-safe.
// Used to avoid redundant API calls when scanning multiple episodes
// of the same series (e.g. title ID lookups, torrent ID lookups).
//
// The zero value is NOT usable: always construct with New. Two things are
// missing from it, and only the first is a crash. The entries map is nil, so
// Set (and every path through it, GetOrFetch included) panics on the
// write; Get and Clear happen to tolerate a nil map, which is what makes the
// zero value look serviceable until the first store. The ttl is also 0, so
// even with a map every entry would expire the instant it was written. A zero
// ttl is meaningful on its own — New(0) is a legal call that yields exactly
// that expire-immediately cache — so it is not reinterpreted here as a default
// or as "never expires", and the zero value cannot be rescued by giving it a
// second meaning.
type Cache[T any] struct {
	group   typedGroup[T]
	entries map[string]entry[T]
	mu      sync.RWMutex
	ttl     time.Duration
}

type entry[T any] struct {
	value   T
	expires time.Time
}

// New creates a cache with the given TTL.
func New[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		entries: make(map[string]entry[T]),
		ttl:     ttl,
	}
}

// Get returns the cached value for key, or the zero value if the key is
// absent or expired. Get is read-only: an expired entry is reported as a miss
// but stays in the map until it is overwritten or Clear reclaims it.
func (c *Cache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		var zero T
		return zero, false
	}
	return e.value, true
}

// Set stores a value with the cache's TTL.
func (c *Cache[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry[T]{
		value:   value,
		expires: time.Now().Add(c.ttl),
	}
}

// GetOrFetch returns the cached value for key, or calls fn to fetch it.
// Concurrent calls for the same key are coalesced via singleflight.
func (c *Cache[T]) GetOrFetch(key string, fn func() (T, error)) (T, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	v, _, err := c.group.Do(key, func() (T, error) {
		result, err := fn()
		if err == nil {
			c.Set(key, result)
		}
		return result, err
	})
	return v, err
}

// Clear removes all entries.
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}
