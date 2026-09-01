package queryhandlers

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
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

type statsCacheEntry struct {
	storedAt time.Time
	resp     subflux.Stats
}

// Invalidate marks the cache stale (exported for use by polling subsystem).
func (c *statsCache) Invalidate() { c.invalid.Store(true) }

func (c *statsCache) invalidate() { c.invalid.Store(true) }

func (c *statsCache) get(ctx context.Context, fn func(context.Context) subflux.Stats) subflux.Stats {
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
		return subflux.Stats{}
	}
	if resp, ok := v.(subflux.Stats); ok {
		return resp
	}
	return subflux.Stats{}
}
