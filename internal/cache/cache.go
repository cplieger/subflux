// Package cache provides a generic TTL cache with singleflight coalescing.
package cache

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultTTL is the standard TTL for provider lookup caches.
// Used by providers that cache title/show ID lookups across scan cycles.
const DefaultTTL = 6 * time.Hour

// typedGroup wraps singleflight.Group to provide type-safe Do/DoChan
// without requiring callers to perform unsafe type assertions.
type typedGroup[T any] struct {
	g singleflight.Group
}

func (tg *typedGroup[T]) Do(key string, fn func() (T, error)) (val T, shared bool, err error) {
	v, err, shared := tg.g.Do(key, func() (any, error) { return fn() })
	if err != nil {
		var zero T
		return zero, shared, err
	}
	val, _ = v.(T) //nolint:errcheck // v is always T when err==nil (singleflight stores fn() result)
	return val, shared, nil
}

func (tg *typedGroup[T]) DoChan(key string, fn func() (T, error)) <-chan singleflight.Result {
	return tg.g.DoChan(key, func() (any, error) { return fn() })
}

// Cache is a generic TTL cache for provider lookups. Thread-safe.
// Used to avoid redundant API calls when scanning multiple episodes
// of the same series (e.g. title ID lookups, torrent ID lookups).
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
// absent or expired.
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

// GetOrFetchCtx is like GetOrFetch but accepts a context-aware fetch function.
// Each caller's context is respected independently: if one caller's context
// is cancelled, other coalesced waiters are not affected. The fetch itself
// runs with a detached context so it completes for all waiters.
func (c *Cache[T]) GetOrFetchCtx(ctx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	ch := c.group.DoChan(key, func() (T, error) {
		result, err := fn(context.WithoutCancel(ctx))
		if err == nil {
			c.Set(key, result)
		}
		return result, err
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			var zero T
			return zero, res.Err
		}
		val, _ := res.Val.(T) //nolint:errcheck // Val is always T when Err==nil
		return val, nil
	}
}

// Reap removes expired entries.
func (c *Cache[T]) Reap() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
}

// Clear removes all entries.
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}
