// Package providerhealth tracks how a provider is behaving and takes a failing
// one out of rotation: it counts failures in a sliding window and, past a
// threshold, times the provider out for a cooldown.
//
// It stays its own package rather than folding into internal/search, which is
// its only importer. Consumer count is not the test — a self-contained state
// machine with its own property and fuzz tests is a package, the same reason
// subsync/fft and subsync/framerate stayed theirs, and internal/search is
// already the largest package in this domain. The name states that capability
// rather than the mechanism: the folder used to be called timeout, which named
// one of its two outcomes, and health alone would shadow cplieger/health, the
// first-party library main.go imports.
//
// The interface its consumer needs is declared AT that consumer, beside the
// no-op implementation it selects when timeouts are disabled. New returns the
// concrete type.
package providerhealth

import (
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

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
func New(cfg Config) *Tracker {
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
	return &Tracker{
		failures:  make(map[subflux.ProviderID][]time.Time),
		tripped:   make(map[subflux.ProviderID]time.Time),
		lastError: make(map[subflux.ProviderID]string),
		threshold: cfg.Threshold,
		window:    cfg.Window,
		cooldown:  cfg.Cooldown,
		now:       nowFn,
	}
}

// Tracker counts per-provider failures in a sliding window and times a
// provider out for a cooldown once the threshold is reached. Its zero value is
// NOT usable: New allocates the three maps and the clock. Safe for concurrent
// use.
type Tracker struct {
	failures  map[subflux.ProviderID][]time.Time
	tripped   map[subflux.ProviderID]time.Time
	lastError map[subflux.ProviderID]string
	now       func() time.Time
	mu        sync.Mutex
	window    time.Duration
	cooldown  time.Duration
	threshold int
}

func (it *Tracker) IsTimedOut(provider subflux.ProviderID) bool {
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

func (it *Tracker) RecordSuccess(provider subflux.ProviderID) {
	it.mu.Lock()
	defer it.mu.Unlock()
	delete(it.failures, provider)
	delete(it.tripped, provider)
	delete(it.lastError, provider)
}

func (it *Tracker) RecordFailure(provider subflux.ProviderID, err error) {
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

func (it *Tracker) Reset() {
	it.mu.Lock()
	defer it.mu.Unlock()
	clear(it.failures)
	clear(it.tripped)
	clear(it.lastError)
	slog.Info("provider timeouts reset, all providers re-enabled")
}

// countAfter returns how many timestamps are strictly after cutoff.
func countAfter(times []time.Time, cutoff time.Time) int {
	count := 0
	for _, t := range times {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

func (it *Tracker) Status() map[subflux.ProviderID]subflux.ProviderStatus {
	it.mu.Lock()
	defer it.mu.Unlock()

	now := it.now()
	cutoff := now.Add(-it.window)
	out := make(map[subflux.ProviderID]subflux.ProviderStatus, len(it.failures)+len(it.tripped))

	for prov, times := range it.failures {
		s := subflux.ProviderStatus{
			RecentFailures: countAfter(times, cutoff),
			Threshold:      it.threshold,
			LastError:      it.lastError[prov],
		}
		if trippedAt, ok := it.tripped[prov]; ok {
			if remaining := it.cooldown - now.Sub(trippedAt); remaining > 0 {
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
		if remaining := it.cooldown - now.Sub(trippedAt); remaining > 0 {
			out[prov] = subflux.ProviderStatus{
				TimedOut:          true,
				Threshold:         it.threshold,
				CooldownRemaining: remaining,
				LastError:         it.lastError[prov],
			}
		}
	}

	return out
}
