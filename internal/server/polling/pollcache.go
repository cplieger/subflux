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
//
// A failed durable write leaves the cursor DIRTY: memory and disk disagree,
// and a restart would replay from the older persisted position. That state
// is explicit — WARN-logged with its onset, retried via RetryDirty on the
// poll heartbeat, gauged through the optional dirty gauge, and announced
// when it heals — so restart replay is an expected, explained event instead
// of a silent drift.
type PollCache struct {
	readFn     func(ctx context.Context, key api.PollKey) (time.Time, error)
	setFn      func(ctx context.Context, key api.PollKey, t time.Time) error
	dirty      map[api.PollKey]time.Time
	dirtyGauge func(n int)
	shadow     sync.Map
	dirtyMu    sync.Mutex
}

// NewPollCache creates a PollCache backed by the given read/set functions.
func NewPollCache(
	readFn func(ctx context.Context, key api.PollKey) (time.Time, error),
	setFn func(ctx context.Context, key api.PollKey, t time.Time) error,
) *PollCache {
	return &PollCache{
		readFn: readFn,
		setFn:  setFn,
		dirty:  make(map[api.PollKey]time.Time),
	}
}

// SetDirtyGauge installs an observer for the dirty-cursor count (e.g. a
// Prometheus gauge). Called with the current count on every transition.
func (c *PollCache) SetDirtyGauge(fn func(n int)) {
	c.dirtyMu.Lock()
	defer c.dirtyMu.Unlock()
	c.dirtyGauge = fn
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

// Set updates both the in-memory cache and the persistent store. The cache
// advances unconditionally so polling keeps working through disk trouble;
// a failed durable write marks the cursor dirty (see PollCache doc).
func (c *PollCache) Set(ctx context.Context, key api.PollKey, t time.Time) {
	c.shadow.Store(key, t)
	if err := c.setFn(ctx, key, t); err != nil {
		c.markDirty(key, err)
		return
	}
	c.markClean(key)
}

// RetryDirty re-attempts the durable persist of every dirty cursor using its
// CURRENT in-memory position. Called on the poll heartbeat so a transient
// write failure heals within one cycle; a no-op when everything is clean.
func (c *PollCache) RetryDirty(ctx context.Context) {
	c.dirtyMu.Lock()
	keys := make([]api.PollKey, 0, len(c.dirty))
	for k := range c.dirty {
		keys = append(keys, k)
	}
	c.dirtyMu.Unlock()

	for _, key := range keys {
		v, ok := c.shadow.Load(key)
		if !ok {
			continue
		}
		t, ok := v.(time.Time)
		if !ok {
			continue
		}
		if err := c.setFn(ctx, key, t); err != nil {
			slog.Warn("PollCache: dirty cursor persist retry failed",
				"key", key, "error", err, "dirty_since", c.dirtySince(key))
			continue
		}
		c.markClean(key)
	}
}

// DirtyCount returns how many cursors currently have a failing persist.
func (c *PollCache) DirtyCount() int {
	c.dirtyMu.Lock()
	defer c.dirtyMu.Unlock()
	return len(c.dirty)
}

func (c *PollCache) dirtySince(key api.PollKey) time.Time {
	c.dirtyMu.Lock()
	defer c.dirtyMu.Unlock()
	return c.dirty[key]
}

func (c *PollCache) markDirty(key api.PollKey, err error) {
	c.dirtyMu.Lock()
	since, already := c.dirty[key]
	if !already {
		since = time.Now()
		c.dirty[key] = since
	}
	n := len(c.dirty)
	gauge := c.dirtyGauge
	c.dirtyMu.Unlock()

	slog.Warn("PollCache: durable cursor write failed; in-memory position is ahead of disk (restart would replay)",
		"key", key, "error", err, "dirty_since", since)
	if gauge != nil {
		gauge(n)
	}
}

func (c *PollCache) markClean(key api.PollKey) {
	c.dirtyMu.Lock()
	since, was := c.dirty[key]
	if was {
		delete(c.dirty, key)
	}
	n := len(c.dirty)
	gauge := c.dirtyGauge
	c.dirtyMu.Unlock()

	if was {
		slog.Info("PollCache: dirty cursor persisted; memory and disk agree again",
			"key", key, "was_dirty_since", since)
		if gauge != nil {
			gauge(n)
		}
	}
}
