// Package timeout provides provider health tracking with sliding-window
// failure detection and cooldown-based timeout.
package timeout

import (
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// ProviderHealth abstracts provider timeout tracking.
type ProviderHealth interface {
	IsTimedOut(provider api.ProviderID) bool
	RecordSuccess(provider api.ProviderID)
	RecordFailure(provider api.ProviderID, err error)
	Status() map[api.ProviderID]api.TimeoutStatus
	Reset()
}

// Config holds provider timeout settings.
type Config struct {
	Now       func() time.Time // Clock function; nil defaults to time.Now.
	Threshold int              // failures within window to trigger (default: DefaultThreshold)
	Window    time.Duration    // sliding window (default: DefaultWindow)
	Cooldown  time.Duration    // cooldown after triggering
}

// DefaultThreshold is the number of failures within the window that triggers
// a provider timeout. Exported so callers can rely on the package default
// without repeating the magic number.
const DefaultThreshold = 5

// DefaultWindow is the sliding window duration for failure counting.
// Exported so callers can rely on the package default without repeating
// the magic number.
const DefaultWindow = 10 * time.Minute

// New creates a provider timeout tracker with the given config.
func New(cfg Config) ProviderHealth {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Hour
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &tracker{
		failures:  make(map[api.ProviderID][]time.Time),
		tripped:   make(map[api.ProviderID]time.Time),
		lastError: make(map[api.ProviderID]string),
		threshold: cfg.Threshold,
		window:    cfg.Window,
		cooldown:  cfg.Cooldown,
		now:       nowFn,
	}
}

// tracker implements ProviderHealth with sliding-window failure detection.
type tracker struct {
	failures  map[api.ProviderID][]time.Time
	tripped   map[api.ProviderID]time.Time
	lastError map[api.ProviderID]string
	now       func() time.Time
	mu        sync.Mutex
	window    time.Duration
	cooldown  time.Duration
	threshold int
}

func (it *tracker) IsTimedOut(provider api.ProviderID) bool {
	it.mu.Lock()
	defer it.mu.Unlock()
	trippedAt, ok := it.tripped[provider]
	if !ok {
		return false
	}
	if it.now().Sub(trippedAt) >= it.cooldown {
		delete(it.tripped, provider)
		delete(it.failures, provider)
		delete(it.lastError, provider)
		slog.Info("provider timeout expired", "provider", provider)
		return false
	}
	return true
}

func (it *tracker) RecordSuccess(provider api.ProviderID) {
	it.mu.Lock()
	defer it.mu.Unlock()
	delete(it.failures, provider)
	delete(it.tripped, provider)
	delete(it.lastError, provider)
}

func (it *tracker) RecordFailure(provider api.ProviderID, err error) {
	it.mu.Lock()
	defer it.mu.Unlock()

	now := it.now()
	cutoff := now.Add(-it.window)

	if err != nil {
		it.lastError[provider] = err.Error()
	} else {
		it.lastError[provider] = ""
	}

	recent := it.failures[provider]
	pruned := recent[:0]
	for _, t := range recent {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, now)
	if cap(pruned) > 2*it.threshold && len(pruned) < it.threshold {
		shrunk := make([]time.Time, len(pruned), it.threshold)
		copy(shrunk, pruned)
		pruned = shrunk
	}
	it.failures[provider] = pruned

	if len(pruned) >= it.threshold {
		if _, already := it.tripped[provider]; !already {
			it.tripped[provider] = now
			slog.Warn("provider timed out",
				"provider", provider,
				"failures", len(pruned),
				"cooldown", it.cooldown)
		}
	}
}

func (it *tracker) Reset() {
	it.mu.Lock()
	defer it.mu.Unlock()
	clear(it.failures)
	clear(it.tripped)
	clear(it.lastError)
	slog.Info("provider timeouts reset, all providers re-enabled")
}

func (it *tracker) Status() map[api.ProviderID]api.TimeoutStatus {
	it.mu.Lock()
	defer it.mu.Unlock()

	now := it.now()
	cutoff := now.Add(-it.window)
	out := make(map[api.ProviderID]api.TimeoutStatus, len(it.failures)+len(it.tripped))

	for prov, times := range it.failures {
		count := 0
		for _, t := range times {
			if t.After(cutoff) {
				count++
			}
		}
		s := api.TimeoutStatus{RecentFailures: count, Threshold: it.threshold, LastError: it.lastError[prov]}
		if trippedAt, ok := it.tripped[prov]; ok {
			remaining := it.cooldown - now.Sub(trippedAt)
			if remaining > 0 {
				s.TimedOut = true
				s.CooldownRemaining = remaining
			}
		}
		out[prov] = s
	}

	for prov, trippedAt := range it.tripped {
		if _, seen := out[prov]; seen {
			continue
		}
		remaining := it.cooldown - now.Sub(trippedAt)
		if remaining > 0 {
			out[prov] = api.TimeoutStatus{
				TimedOut:          true,
				Threshold:         it.threshold,
				CooldownRemaining: remaining,
				LastError:         it.lastError[prov],
			}
		}
	}

	return out
}
