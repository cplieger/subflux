package authstore

import (
	"context"
	"log/slog"
	"time"
)

// This file holds the background sweeper that evicts expired sessions and OIDC
// states from the in-memory ephemeral maps, replacing the periodic SQL cleanup
// queries the old SQLite store ran. A single goroutine is started in Open and
// stopped in Close (Requirements 10.3, 10.4).
//
// Where the timings come from. Open takes no arguments (fixed by the AuthStore
// wiring and the design's authstore.New(db) composition root), so the sweep
// interval and the idle/absolute/OIDC durations are stored on the Store,
// seeded by New from the package defaults below:
//
//   - The sweep INTERVAL bounds eviction latency: an expired session or OIDC
//     state is evicted within one interval of crossing its timeout
//     (Requirement 10.3). It matches the old server-side auth-cleanup cadence
//     (internal/server/scheduler.AuthCleanupInterval = 15m).
//   - The session idle/absolute DURATIONS match subflux's config defaults
//     (internal/config/defaults.DefaultSession{Idle,Absolute}Timeout = 24h / 7d),
//     and the OIDC TTL matches scheduler.OIDCStateTTL = 10m. A self-hosted
//     instance that never tunes them therefore behaves exactly like the SQLite
//     cleanup it replaces.
//
// The durations live on the Store (not as Open arguments) so the composition
// root (main.go, task 10.2) MAY override them with configured values without
// changing the New(db) signature.
const (
	// defaultSweepInterval bounds how long an expired entry can linger.
	defaultSweepInterval = 15 * time.Minute
	// defaultSessionIdleTimeout is the no-activity cutoff (cf. config defaults).
	defaultSessionIdleTimeout = 24 * time.Hour
	// defaultSessionAbsoluteTimeout is the max session lifetime (cf. config defaults).
	defaultSessionAbsoluteTimeout = 7 * 24 * time.Hour
	// defaultOIDCStateTTL is the pending-login TTL (cf. scheduler.OIDCStateTTL).
	defaultOIDCStateTTL = 10 * time.Minute
)

// Open starts the single background sweeper goroutine. It evicts expired
// sessions and OIDC states once per sweep interval until Close signals stop
// (Requirements 10.3, 10.4). Open is idempotent: a second call while the
// sweeper is already running is a no-op, so exactly one goroutine ever runs.
func (s *Store) Open() error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	interval := s.sweepInterval
	if interval <= 0 {
		interval = defaultSweepInterval
	}
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()

	go s.sweepLoop(interval, done)
	slog.Info("auth sweeper started", "interval", interval)
	return nil
}

// Close stops the background sweeper and waits for it to exit. It is safe to
// call multiple times and safe to call without a prior Open: the stop channel
// is closed exactly once via closeOnce, so a double Close never panics, and an
// unopened Store has no goroutine to wait on. Close NEVER closes the shared
// *bbolt.DB handle — the core store owns that handle and is the only place it
// is closed.
func (s *Store) Close() error {
	s.closeOnce.Do(func() { close(s.stop) })

	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	if done != nil {
		<-done // wait for the sweeper goroutine to return (no goroutine leak)
	}
	return nil
}

// sweepLoop is the sweeper goroutine body: it evicts expired entries on every
// tick and returns when the stop channel is closed, closing done on the way out
// so Close can join it. The ticker is stopped on return.
func (s *Store) sweepLoop(interval time.Duration, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			slog.Info("auth sweeper stopped")
			return
		case <-ticker.C:
			s.sweepOnce(time.Now())
		}
	}
}

// SetSessionTimeouts overrides the sweeper's session idle/absolute timeouts
// with configured values. The composition root calls it after loading config,
// and the server's hot reload calls it again when the settings change, so the
// sweeper evicts on the SAME cutoffs the request-path session validator
// enforces (sweeper eviction of an in-memory session is a hard logout, so a
// shorter sweeper timeout would silently cap a longer configured one).
// Non-positive values leave the current timeout unchanged. Safe for concurrent
// use with a running sweeper.
func (s *Store) SetSessionTimeouts(idle, absolute time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idle > 0 {
		s.idleTimeout = idle
	}
	if absolute > 0 {
		s.absTimeout = absolute
	}
}

// sweepOnce evicts expired sessions and OIDC states in one pass. The underlying
// CleanupExpired* methods already log their eviction counts at DEBUG, so this
// only adds error logging (also DEBUG, matching their convention) and never
// duplicates the per-eviction lines. The timeout fields are snapshotted under
// the read lock because SetSessionTimeouts may update them concurrently.
func (s *Store) sweepOnce(now time.Time) {
	s.mu.RLock()
	idle, absolute, oidcTTL := s.idleTimeout, s.absTimeout, s.oidcTTL
	s.mu.RUnlock()

	ctx := context.Background()
	if _, err := s.CleanupExpiredSessions(ctx, now, idle, absolute); err != nil {
		slog.Debug("auth sweeper: session cleanup failed", "error", err)
	}
	if _, err := s.CleanupExpiredOIDCStates(ctx, now, oidcTTL); err != nil {
		slog.Debug("auth sweeper: oidc cleanup failed", "error", err)
	}
}
