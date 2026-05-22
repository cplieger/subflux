// Package ratelimit implements a dual sliding-window rate limiter for
// authentication attempts (per-IP and per-account). It is consumed by
// the server's auth handlers via the RateLimitChecker interface.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Config groups all rate-limit tuning parameters into a single
// inspectable value. Use DefaultConfig() for production defaults.
type Config struct {
	IPLimit       int
	IPWindow      time.Duration
	AcctLimit     int
	AcctWindow    time.Duration
	PruneInterval time.Duration
	MaxEntries    int
}

// DefaultConfig returns the production rate-limit configuration:
//   - Per-IP: 10 attempts / 15 minutes
//   - Per-account: 100 attempts / 1 hour
//   - Max tracked entries: 10000
func DefaultConfig() Config {
	return Config{
		IPLimit:       10,
		IPWindow:      15 * time.Minute,
		AcctLimit:     100,
		AcctWindow:    time.Hour,
		PruneInterval: 5 * time.Minute,
		MaxEntries:    10000,
	}
}

// Checker is the narrow interface consumed by the server layer.
// It decouples request handling from the concrete sliding-window implementation.
type Checker interface {
	Allow(ip, username string) (allowed bool, retryAfter time.Duration)
	Record(ip, username string)
}

// Compile-time assertion that *RateLimiter satisfies Checker.
var _ Checker = (*RateLimiter)(nil)

// RateLimiter tracks failed authentication attempts per IP and per account
// using dual sliding windows (OWASP ASVS 2.2.1).
type RateLimiter struct {
	ipWindows     map[string]*slidingWindow
	acctWindows   map[string]*slidingWindow
	nowFunc       func() time.Time
	cancel        context.CancelFunc
	ipWindow      time.Duration
	acctWindow    time.Duration
	pruneInterval time.Duration
	ipLimit       int
	acctLimit     int
	maxEntries    int
	muIP          sync.Mutex
	muAcct        sync.Mutex
}

type slidingWindow struct {
	timestamps []time.Time
}

// NewRateLimiter creates a rate limiter with the given configuration.
// A background goroutine prunes stale entries at cfg.PruneInterval.
// The goroutine stops when ctx is cancelled. Call Stop for explicit shutdown.
func NewRateLimiter(ctx context.Context, cfg Config) *RateLimiter {
	ctx, cancel := context.WithCancel(ctx)
	rl := &RateLimiter{
		ipWindows:     make(map[string]*slidingWindow),
		acctWindows:   make(map[string]*slidingWindow),
		ipLimit:       cfg.IPLimit,
		ipWindow:      cfg.IPWindow,
		acctLimit:     cfg.AcctLimit,
		acctWindow:    cfg.AcctWindow,
		maxEntries:    cfg.MaxEntries,
		pruneInterval: cfg.PruneInterval,
		cancel:        cancel,
		nowFunc:       time.Now,
	}
	go rl.pruneLoop(ctx)
	return rl
}

// Allow checks both IP and account windows. Both must be within limits
// for the request to proceed. Returns false with a retry-after duration
// if either limit is exceeded.
func (rl *RateLimiter) Allow(ip, username string) (allowed bool, retryAfter time.Duration) {
	now := rl.nowFunc()

	var ipRetry, acctRetry time.Duration

	rl.muIP.Lock()
	if w, ok := rl.ipWindows[ip]; ok {
		ipRetry = w.retryAfter(now, rl.ipWindow, rl.ipLimit)
	}
	rl.muIP.Unlock()

	if username != "" {
		rl.muAcct.Lock()
		if w, ok := rl.acctWindows[username]; ok {
			acctRetry = w.retryAfter(now, rl.acctWindow, rl.acctLimit)
		}
		rl.muAcct.Unlock()
	}

	if ipRetry > 0 || acctRetry > 0 {
		ra := max(ipRetry, acctRetry)
		return false, ra
	}

	return true, 0
}

// Record records a failed authentication attempt in both the IP and
// account sliding windows.
func (rl *RateLimiter) Record(ip, username string) {
	now := rl.nowFunc()

	rl.muIP.Lock()
	if w, ok := rl.ipWindows[ip]; ok {
		w.add(now)
	} else if len(rl.ipWindows) < rl.maxEntries {
		rl.ipWindows[ip] = &slidingWindow{}
		rl.ipWindows[ip].add(now)
	}
	rl.muIP.Unlock()

	if username != "" {
		rl.muAcct.Lock()
		if w, ok := rl.acctWindows[username]; ok {
			w.add(now)
		} else if len(rl.acctWindows) < rl.maxEntries {
			rl.acctWindows[username] = &slidingWindow{}
			rl.acctWindows[username].add(now)
		}
		rl.muAcct.Unlock()
	}
}

// Stop stops the background prune goroutine.
func (rl *RateLimiter) Stop() {
	rl.cancel()
}

// pruneLoop removes stale entries at the configured prune interval.
func (rl *RateLimiter) pruneLoop(ctx context.Context) {
	ticker := time.NewTicker(rl.pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.prune()
		}
	}
}

func (rl *RateLimiter) prune() {
	now := rl.nowFunc()

	rl.muIP.Lock()
	for k, w := range rl.ipWindows {
		if w.count(now, rl.ipWindow) == 0 {
			delete(rl.ipWindows, k)
		}
	}
	rl.muIP.Unlock()

	rl.muAcct.Lock()
	for k, w := range rl.acctWindows {
		if w.count(now, rl.acctWindow) == 0 {
			delete(rl.acctWindows, k)
		}
	}
	rl.muAcct.Unlock()
}

// slidingWindow methods

func (w *slidingWindow) add(now time.Time) {
	w.timestamps = append(w.timestamps, now)
}

// count returns the number of timestamps within the window, pruning old ones.
func (w *slidingWindow) count(now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	i := 0
	for i < len(w.timestamps) && w.timestamps[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		w.timestamps = w.timestamps[i:]
	}
	return len(w.timestamps)
}

// retryAfter returns the duration until the oldest relevant entry expires,
// if the window is at or over the limit. Returns 0 if under the limit.
func (w *slidingWindow) retryAfter(now time.Time, window time.Duration, limit int) time.Duration {
	n := w.count(now, window)
	if n < limit {
		return 0
	}
	oldest := w.timestamps[0]
	expires := oldest.Add(window)
	ra := expires.Sub(now)
	if ra < 0 {
		return 0
	}
	return ra
}
