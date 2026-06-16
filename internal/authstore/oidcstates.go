package authstore

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// This file holds the OIDCStateStore half of AuthStore. OIDC login states are
// EPHEMERAL: they live only in the in-memory map Store.oidc, guarded by
// Store.mu (an RWMutex), and are never persisted to bbolt. They are lost on
// restart by design; an in-flight OIDC login simply retries (Requirements 10.1,
// 10.4).
//
// The observable semantics mirror the old SQLite store/authdb/oidcstate.go even
// though the backing store changed from a table to a map:
//   - CreateOIDCState stores the in-flight login state keyed by the opaque
//     `state` token.
//   - ConsumeOIDCState is SINGLE-USE: it atomically reads and deletes the entry
//     under one write lock, so the first consume returns the stored values and
//     any later consume of the same state returns not-found. This is the
//     CSRF/replay protection (Requirement 16.3); doing the read+delete under a
//     single s.mu.Lock() (not RLock-then-Lock) guarantees two concurrent
//     consumes can never both succeed.
//   - CleanupExpiredOIDCStates evicts entries older than maxAge using the same
//     strict (exclusive) cutoff comparison as sessions.go's
//     CleanupExpiredSessions.
//
// The oidcRec.expiresAt field (defined in authdb.go) holds the record's
// CREATION instant in this in-memory design: there is no per-state TTL at
// create time, so expiry is enforced relative to the maxAge passed to
// CleanupExpiredOIDCStates (driven by the background sweeper, task 8.7),
// mirroring the absolute-timeout pattern sessions use against CreatedAt.

// errOIDCStateNotFound is the not-found signal returned by ConsumeOIDCState when
// no live state matches: the state was never created, was already consumed
// (single-use), or was swept after expiry. The OIDC callback handler treats any
// non-nil error here as an invalid/expired state, so this mirrors the old
// SQLite store's ErrOIDCStateNotFound observable behaviour (Requirement 16.3).
var errOIDCStateNotFound = errors.New("authstore: oidc state not found")

// CreateOIDCState records an in-flight OIDC login state in memory, keyed by the
// opaque state token. The stored instant is the creation time; OIDC states
// never touch disk (Requirement 10.1).
func (s *Store) CreateOIDCState(_ context.Context, state, nonce, codeVerifier, redirectURI string) error {
	s.mu.Lock()
	s.oidc[state] = &oidcRec{
		expiresAt:    time.Now().UTC(),
		nonce:        nonce,
		codeVerifier: codeVerifier,
		redirectURI:  redirectURI,
	}
	s.mu.Unlock()
	return nil
}

// ConsumeOIDCState atomically returns and removes the OIDC state for the given
// token (single-use). The read and delete happen under one s.mu.Lock(), so two
// concurrent consumes of the same state can never both succeed: exactly one
// observes the entry and deletes it, the other sees it already gone. A second
// consume of an already-consumed (or never-created, or swept) state returns
// errOIDCStateNotFound (Requirement 16.3, anti-replay).
func (s *Store) ConsumeOIDCState(_ context.Context, state string) (nonce, codeVerifier, redirectURI string, err error) {
	s.mu.Lock()
	rec, ok := s.oidc[state]
	if ok {
		delete(s.oidc, state)
	}
	s.mu.Unlock()
	if !ok || rec == nil {
		return "", "", "", errOIDCStateNotFound
	}
	slog.Debug("oidc state consumed", "state_prefix", state[:min(8, len(state))])
	return rec.nonce, rec.codeVerifier, rec.redirectURI, nil
}

// CleanupExpiredOIDCStates evicts every OIDC state whose creation instant is
// strictly before now-maxAge and returns the count evicted. The strict
// (exclusive) cutoff matches sessions.go's CleanupExpiredSessions, so a state
// exactly at the cutoff is kept rather than evicted (Requirement 10.3).
func (s *Store) CleanupExpiredOIDCStates(_ context.Context, now time.Time, maxAge time.Duration) (int64, error) {
	cutoff := now.Add(-maxAge)
	var total int64
	s.mu.Lock()
	for state, rec := range s.oidc {
		if rec == nil || rec.expiresAt.Before(cutoff) {
			delete(s.oidc, state)
			total++
		}
	}
	s.mu.Unlock()
	if total > 0 {
		slog.Debug("expired oidc states cleaned", "count", total)
	}
	return total, nil
}
