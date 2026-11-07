// Package cache is a bounded TTL cache for provider lookups that coalesces
// concurrent fetches of one key. A found value is kept 6 hours and an empty
// answer 15 minutes, so a title a provider lacks today is asked again soon;
// each cache holds at most 4,096 entries, so its memory does not grow with
// the library.
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

const (
	hitTTL     = 6 * time.Hour
	emptyTTL   = 15 * time.Minute
	maxEntries = 4096
)

// Cache maps keys to fetched values; the zero value is not usable, New
// makes one.
type Cache[T any] struct {
	found   func(T) bool
	entries map[string]*entry[T]
	flights map[string]*flight[T]
	mu      sync.Mutex
	used    uint64 // the use clock entries' last uses read
}

type entry[T any] struct {
	expires time.Time
	value   T
	used    uint64
}

// flight is one fetch of a key and the callers waiting on it. value, err and
// panicked are set before done closes and never after.
type flight[T any] struct {
	done     chan struct{}
	cancel   context.CancelFunc
	panicked *panicError
	value    T
	err      error
	waiters  int
}

// errFetchExited answers the waiters of a fn that ended its goroutine
// (runtime.Goexit) without returning.
var errFetchExited = errors.New("cache: the fetch exited without returning")

// panicError is fn's panic value and the fetch goroutine's stack, which
// every waiting caller re-panics with.
type panicError struct {
	value any
	stack []byte
}

func (p *panicError) Error() string { return fmt.Sprintf("%v\n\n%s", p.value, p.stack) }

// New makes a cache; found tells a hit from an empty answer.
func New[T any](found func(T) bool) *Cache[T] {
	return &Cache[T]{found: found, entries: map[string]*entry[T]{}, flights: map[string]*flight[T]{}}
}

// NonEmpty is New's found for a slice: a hit holds at least one element.
func NonEmpty[E any](s []E) bool { return len(s) > 0 }

// GetOrFetch returns key's live value, else fn's, which it keeps when fn
// succeeds; an error is never kept. Concurrent calls for one key share one fn,
// run with the first caller's context values. A caller whose ctx ends first
// gets ctx's error while the others keep waiting; fn's context ends only when
// every caller waiting on it has left, and a later call then starts afresh.
// A panic in fn is kept nowhere: every caller still waiting re-panics with
// an error carrying fn's panic value and stack, and with none left it is
// logged.
func (c *Cache[T]) GetOrFetch(ctx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	if v, ok := c.live(key); ok {
		c.mu.Unlock()
		return v, nil
	}
	f, ok := c.flights[key]
	if !ok {
		fctx, cancel := context.WithCancel(context.WithoutCancel(ctx)) //nolint:gosec // G118: fetch's defer and the last leave call f.cancel
		f = &flight[T]{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = f
		go c.fetch(fctx, key, f, fn)
	}
	f.waiters++
	c.mu.Unlock()

	select {
	case <-f.done:
		return f.result()
	case <-ctx.Done():
		if c.leave(key, f) {
			return f.result()
		}
		var zero T
		return zero, ctx.Err()
	}
}

// result is the ended flight's answer; it re-panics fn's panic.
func (f *flight[T]) result() (T, error) {
	if f.panicked != nil {
		panic(f.panicked)
	}
	return f.value, f.err
}

// fetch's goroutine has no caller's recover above it, so the deferred
// finish turns a panic or a Goexit into the flight's result; done closes on
// every path, or the waiters would hang until their contexts end.
func (c *Cache[T]) fetch(ctx context.Context, key string, f *flight[T], fn func(context.Context) (T, error)) {
	var (
		v        T
		err      error
		returned bool
	)
	defer func() {
		var p *panicError
		if !returned {
			if r := recover(); r != nil {
				p = &panicError{value: r, stack: debug.Stack()}
			} else {
				err = errFetchExited
			}
		}
		f.cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.flights[key] == f {
			delete(c.flights, key)
		}
		f.value, f.err, f.panicked = v, err, p
		close(f.done)
		if p != nil && f.waiters == 0 {
			slog.Error("cache: a fetch panicked after every caller left", "key", key, "panic", p.value, "stack", string(p.stack))
		}
	}()
	v, err = fn(ctx)
	if err == nil {
		c.set(key, v)
	}
	returned = true
}

// leave drops a waiter that stopped waiting; the last one ends the fetch and
// unlists it, so no later caller joins a cancelled fetch. It reports instead
// that the fetch already ended, whose result the caller then takes: under
// c.mu, fetch's finish either counts this waiter or has closed done, so a
// panic always reaches a caller or the log.
func (c *Cache[T]) leave(key string, f *flight[T]) (ended bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-f.done:
		return true
	default:
	}
	f.waiters--
	if f.waiters > 0 {
		return false
	}
	f.cancel()
	if c.flights[key] == f {
		delete(c.flights, key)
	}
	return false
}

// live returns key's unexpired value; the caller holds c.mu.
func (c *Cache[T]) live(key string) (T, bool) {
	e, ok := c.entries[key]
	if !ok || !time.Now().Before(e.expires) {
		var zero T
		return zero, false
	}
	c.used++
	e.used = c.used
	return e.value, true
}

// set keeps value, first dropping every expired entry and then the least
// recently used ones while a new key would pass maxEntries.
func (c *Cache[T]) set(key string, value T) {
	now := time.Now()
	ttl := emptyTTL
	if c.found(value) {
		ttl = hitTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= maxEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		for len(c.entries) >= maxEntries {
			c.dropOldest()
		}
	}
	c.used++
	c.entries[key] = &entry[T]{value: value, expires: now.Add(ttl), used: c.used}
}

func (c *Cache[T]) dropOldest() {
	var oldest string
	least := ^uint64(0)
	for k, e := range c.entries {
		if e.used < least {
			oldest, least = k, e.used
		}
	}
	delete(c.entries, oldest)
}
