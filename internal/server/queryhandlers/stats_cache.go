package queryhandlers

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/sync/singleflight"
)

// statsCacheTTL is how long a cached /api/state/stats response is reused
// before recomputing.
const statsCacheTTL = 5 * time.Second

// statsCache holds the last computed /api/state/stats response.
type statsCache struct {
	sf      singleflight.Group
	mu      atomic.Pointer[statsCacheEntry]
	invalid atomic.Bool
}

// statsCacheEntry is one cached compute result.
type statsCacheEntry struct {
	storedAt time.Time
	resp     api.StateStatsResponse
}

// Invalidate marks the cache stale (exported for use by polling subsystem).
func (c *statsCache) Invalidate() { c.invalid.Store(true) }

// invalidate marks the cache stale; the next call to get() will recompute.
func (c *statsCache) invalidate() { c.invalid.Store(true) }

// get returns the cached response if fresh, otherwise computes via singleflight.
func (c *statsCache) get(ctx context.Context, fn func(context.Context) api.StateStatsResponse) api.StateStatsResponse {
	if e := c.mu.Load(); e != nil && !c.invalid.Load() && time.Since(e.storedAt) < statsCacheTTL {
		return e.resp
	}
	v, err, _ := c.sf.Do("stats", func() (any, error) {
		if e := c.mu.Load(); e != nil && !c.invalid.Load() && time.Since(e.storedAt) < statsCacheTTL {
			return e.resp, nil
		}
		resp := fn(ctx)
		c.mu.Store(&statsCacheEntry{resp: resp, storedAt: time.Now()})
		c.invalid.Store(false)
		return resp, nil
	})
	if err != nil {
		return api.StateStatsResponse{}
	}
	if resp, ok := v.(api.StateStatsResponse); ok {
		return resp
	}
	return api.StateStatsResponse{}
}
