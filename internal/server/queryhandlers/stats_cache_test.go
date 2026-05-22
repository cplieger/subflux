package queryhandlers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestStatsCache(t *testing.T) {
	t.Parallel()

	t.Run("returns_computed_value", func(t *testing.T) {
		t.Parallel()
		var c statsCache
		resp := c.get(context.Background(), func(_ context.Context) api.StateStatsResponse {
			return api.StateStatsResponse{Downloads: 42}
		})
		if resp.Downloads != 42 {
			t.Errorf("Downloads = %d, want 42", resp.Downloads)
		}
	})

	t.Run("caches_within_TTL", func(t *testing.T) {
		t.Parallel()
		var c statsCache
		var calls atomic.Int32
		fn := func(_ context.Context) api.StateStatsResponse {
			calls.Add(1)
			return api.StateStatsResponse{Downloads: 10}
		}
		c.get(context.Background(), fn)
		c.get(context.Background(), fn)
		c.get(context.Background(), fn)
		if got := calls.Load(); got != 1 {
			t.Errorf("compute called %d times, want 1 (cached)", got)
		}
	})

	t.Run("invalidate_forces_recompute", func(t *testing.T) {
		t.Parallel()
		var c statsCache
		var calls atomic.Int32
		fn := func(_ context.Context) api.StateStatsResponse {
			n := calls.Add(1)
			return api.StateStatsResponse{Downloads: int(n) * 10}
		}
		resp1 := c.get(context.Background(), fn)
		c.invalidate()
		resp2 := c.get(context.Background(), fn)
		if resp1.Downloads == resp2.Downloads {
			t.Error("invalidate did not force recompute")
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("compute called %d times, want 2", got)
		}
	})

	t.Run("expires_after_TTL", func(t *testing.T) {
		t.Parallel()
		var c statsCache
		var calls atomic.Int32
		fn := func(_ context.Context) api.StateStatsResponse {
			calls.Add(1)
			return api.StateStatsResponse{Downloads: 5}
		}
		c.get(context.Background(), fn)
		// Manually expire the entry by backdating storedAt.
		if e := c.mu.Load(); e != nil {
			c.mu.Store(&statsCacheEntry{resp: e.resp, storedAt: time.Now().Add(-2 * statsCacheTTL)})
		}
		c.get(context.Background(), fn)
		if got := calls.Load(); got != 2 {
			t.Errorf("compute called %d times, want 2 (expired)", got)
		}
	})
}
