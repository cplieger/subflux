// Package polling provides the history-polling subsystem for Sonarr/Radarr
// import events and the write-through poll timestamp cache.
package polling

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// PollCacher is the interface for poll timestamp caching. Consumers depend
// on this interface rather than the concrete *PollCache, enabling test
// doubles and alternative implementations.
type PollCacher interface {
	Get(ctx context.Context, key api.PollKey) time.Time
	Set(ctx context.Context, key api.PollKey, t time.Time)
}

// Compile-time assertion: *PollCache implements PollCacher.
var _ PollCacher = (*PollCache)(nil)

// PollCache is a write-through cache for poll timestamps. It absorbs
// transient DB write failures: if the DB write fails, subsequent reads
// still return the in-memory value instead of re-reading a stale DB entry.
// On first read after startup, the cache is seeded from the DB via readFn.
// Uses sync.Map for lock-free reads on the hot path (2 keys, read-heavy).
type PollCache struct {
	readFn func(ctx context.Context, key api.PollKey) (time.Time, error)
	setFn  func(ctx context.Context, key api.PollKey, t time.Time) error
	shadow sync.Map
}

// NewPollCache creates a PollCache backed by the given read/set functions.
func NewPollCache(
	readFn func(ctx context.Context, key api.PollKey) (time.Time, error),
	setFn func(ctx context.Context, key api.PollKey, t time.Time) error,
) *PollCache {
	return &PollCache{
		readFn: readFn,
		setFn:  setFn,
	}
}

// Get returns the cached timestamp for key, falling back to the DB on miss.
// Uses LoadOrStore to handle the race between concurrent first-reads atomically.
func (c *PollCache) Get(ctx context.Context, key api.PollKey) time.Time {
	if v, ok := c.shadow.Load(key); ok {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}

	t, err := c.readFn(ctx, key)
	if err != nil {
		slog.Warn("PollCache: read failed, returning zero", "key", key, "error", err)
		return time.Time{}
	}

	if !t.IsZero() {
		actual, _ := c.shadow.LoadOrStore(key, t)
		if stored, ok := actual.(time.Time); ok {
			return stored
		}
	}
	return t
}

// Set updates both the in-memory cache and the persistent store.
// The cache advances unconditionally; the DB write is best-effort and
// WARN-logged on failure.
func (c *PollCache) Set(ctx context.Context, key api.PollKey, t time.Time) {
	c.shadow.Store(key, t)
	if err := c.setFn(ctx, key, t); err != nil {
		slog.Warn("PollCache: write failed", "key", key, "error", err)
	}
}
