package authstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/auth"
)

// This file holds the SessionPersister half of AuthStore plus the
// BatchUpdateSessionActivity extension consumed by the server's session
// batcher (sessionbatch.BatchUpdater, Requirement 16.7).
//
// Sessions are EPHEMERAL: they live only in the in-memory map Store.sessions,
// guarded by Store.mu (an RWMutex — RLock for reads, Lock for writes), and are
// never persisted to bbolt. They are lost on restart by design; users simply
// re-authenticate (Requirement 10.1, 10.4). last_activity updates touch memory
// only, with zero disk churn (Requirement 10.2).
//
// The observable method semantics mirror the old SQLite store/authdb/sessions.go
// even though the backing store changed from a table to a map:
//   - CreateSession stores by token hash; a copy is held so a caller mutating
//     the passed struct afterwards cannot mutate stored state.
//   - GetSessionByHash returns a copy (callers cannot mutate the stored row),
//     and (nil, nil) when absent (matching sql.ErrNoRows -> nil).
//   - UpdateSessionActivity / DeleteSession / DeleteUserSessions on an absent
//     row are no-ops returning nil (matching an UPDATE/DELETE affecting 0 rows).
//   - CleanupExpiredSessions evicts a session past EITHER the idle timeout
//     (last_activity < now-idle) OR the absolute timeout (created_at < now-abs),
//     using the same strict (exclusive) comparison the SQLite cutoffs used.

// cloneSession returns a deep copy of sess so the in-memory map never shares
// mutable state with a caller. auth.Session is a flat struct except for the
// OIDCExpiry *time.Time, which is copied to its own pointer so a caller cannot
// reach through and mutate the stored expiry.
func cloneSession(sess *auth.Session) *auth.Session {
	if sess == nil {
		return nil
	}
	cp := *sess
	if sess.OIDCExpiry != nil {
		t := *sess.OIDCExpiry
		cp.OIDCExpiry = &t
	}
	return &cp
}

// CreateSession stores a new session in memory, keyed by its token hash. A copy
// is stored so a later mutation of the caller's struct does not affect stored
// state. Sessions never touch disk (Requirement 10.1).
func (s *Store) CreateSession(_ context.Context, sess *auth.Session) error {
	if sess == nil {
		return nil
	}
	s.mu.Lock()
	s.sessions[sess.TokenHash] = cloneSession(sess)
	s.mu.Unlock()
	slog.Debug("session created", "user_id", sess.UserID, "method", sess.AuthMethod)
	return nil
}

// GetSessionByHash returns a copy of the session with the given token hash, or
// (nil, nil) when none exists (matching the old store's sql.ErrNoRows -> nil
// mapping). A copy is returned so callers cannot mutate the stored session
// through the returned pointer.
func (s *Store) GetSessionByHash(_ context.Context, tokenHash string) (*auth.Session, error) {
	// cloneSession reads the stored struct's fields, so it must run while the
	// read lock is held: a concurrent UpdateSessionActivity /
	// BatchUpdateSessionActivity mutates LastActivity under the write lock, and
	// cloning after RUnlock would race that write (caught by -race in
	// TestSessions_concurrentAccess).
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return nil, nil
	}
	return cloneSession(sess), nil
}

// UpdateSessionActivity touches a session's last-activity time (memory only,
// Requirement 10.2). An update for an absent session is a no-op returning nil,
// matching the SQLite UPDATE that affects zero rows.
func (s *Store) UpdateSessionActivity(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	if sess, ok := s.sessions[tokenHash]; ok && sess != nil {
		sess.LastActivity = now
	}
	s.mu.Unlock()
	return nil
}

// BatchUpdateSessionActivity touches the last-activity time of many sessions in
// one locked pass (memory only). Consumed by the server's session-activity
// batcher via sessionbatch.BatchUpdater (Requirement 16.7). An empty slice is a
// no-op, and absent hashes are skipped, matching the old store's per-hash
// UPDATE semantics.
func (s *Store) BatchUpdateSessionActivity(_ context.Context, tokenHashes []string, now time.Time) error {
	if len(tokenHashes) == 0 {
		return nil
	}
	s.mu.Lock()
	for _, h := range tokenHashes {
		if sess, ok := s.sessions[h]; ok && sess != nil {
			sess.LastActivity = now
		}
	}
	s.mu.Unlock()
	return nil
}

// DeleteSession removes the session with the given token hash. Deleting an
// absent session is a no-op returning nil (matching a DELETE affecting zero
// rows).
func (s *Store) DeleteSession(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	delete(s.sessions, tokenHash)
	s.mu.Unlock()
	return nil
}

// DeleteUserSessions removes all of a user's sessions except the one identified
// by exceptHash (the keep-one exception used by "log out everywhere else" to
// keep the current session, Requirement 16.5). Pass exceptHash="" to drop all
// of the user's sessions (the DeleteUser cascade path); no real session carries
// an empty hash, so nothing is spuriously kept.
func (s *Store) DeleteUserSessions(_ context.Context, userID int64, exceptHash string) error {
	s.deleteUserSessionsExcept(userID, exceptHash)
	return nil
}

// deleteUserSessionsExcept drops every in-memory session belonging to userID
// whose hash is not exceptHash. It is the single locked implementation shared
// by the public DeleteUserSessions (keep-one) and the DeleteUser cascade
// (exceptHash=""), so the guarded-map-walk pattern lives in exactly one place.
// The sessions map is ephemeral and guarded by s.mu.
func (s *Store) deleteUserSessionsExcept(userID int64, exceptHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, sess := range s.sessions {
		if sess != nil && sess.UserID == userID && hash != exceptHash {
			delete(s.sessions, hash)
		}
	}
}

// CleanupExpiredSessions evicts every session past its idle OR absolute timeout
// and returns the count evicted (Requirement 10.3). A session is expired when
// last_activity is strictly before now-idleTimeout OR created_at is strictly
// before now-absTimeout, matching the exclusive cutoff comparison of the old
// SQLite delete (last_activity < idleCutoff OR created_at < absCutoff). Live
// sessions are kept.
func (s *Store) CleanupExpiredSessions(_ context.Context, now time.Time, idleTimeout, absTimeout time.Duration) (int64, error) {
	idleCutoff := now.Add(-idleTimeout)
	absCutoff := now.Add(-absTimeout)

	var total int64
	s.mu.Lock()
	for hash, sess := range s.sessions {
		if sess == nil || sess.LastActivity.Before(idleCutoff) || sess.CreatedAt.Before(absCutoff) {
			delete(s.sessions, hash)
			total++
		}
	}
	s.mu.Unlock()
	if total > 0 {
		slog.Debug("expired sessions cleaned", "count", total)
	}
	return total, nil
}
