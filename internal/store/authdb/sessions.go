package authdb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// Compile-time assertion: *AuthDB implements session sub-interface.
var _ api.SessionPersister = (*AuthDB)(nil)

var sessionScanner = txutil.TableScanner[api.Session]{
	Columns: `token_hash, user_id, auth_method, ip_address,
	created_at, last_activity, oidc_expiry`,
	Scan:     scanSession,
	ScanInto: scanSessionInto,
}

func scanSession(row interface{ Scan(...any) error }) (*api.Session, error) {
	var s api.Session
	if err := scanSessionInto(row, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanSessionInto(row interface{ Scan(...any) error }, s *api.Session) error {
	var oidcExpiry sql.NullTime
	err := row.Scan(
		&s.TokenHash, &s.UserID, &s.AuthMethod, &s.IPAddress,
		&s.CreatedAt, &s.LastActivity, &oidcExpiry,
	)
	if err != nil {
		return err
	}
	if oidcExpiry.Valid {
		s.OIDCExpiry = &oidcExpiry.Time
	}
	return nil
}

// --- Session methods ---

// CreateSession inserts a new session record.
func (a *AuthDB) CreateSession(ctx context.Context, sess *api.Session) error {
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (token_hash, user_id, auth_method, ip_address,
			created_at, last_activity, oidc_expiry)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.AuthMethod, sess.IPAddress,
		sess.CreatedAt, sess.LastActivity, sess.OIDCExpiry,
	)
	if err == nil {
		slog.Debug("session created", "user_id", sess.UserID, "method", sess.AuthMethod)
	}
	return err
}

// GetSessionByHash returns the session with the given token hash, or nil if
// not found.
func (a *AuthDB) GetSessionByHash(ctx context.Context, tokenHash string) (*api.Session, error) {
	s, err := scanSession(a.stmtGetSessionByHash.QueryRowContext(ctx, tokenHash))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// UpdateSessionActivity updates the last_activity timestamp for a session.
func (a *AuthDB) UpdateSessionActivity(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE auth_sessions SET last_activity = ? WHERE token_hash = ?`,
		now, tokenHash)
	return err
}

// BatchUpdateSessionActivity updates the last_activity timestamp for multiple
// sessions in a single transaction.
func (a *AuthDB) BatchUpdateSessionActivity(ctx context.Context, tokenHashes []string, now time.Time) error {
	if len(tokenHashes) == 0 {
		return nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer txutil.DeferRollback(tx)
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE auth_sessions SET last_activity = ? WHERE token_hash = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range tokenHashes {
		if _, err := stmt.ExecContext(ctx, now, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteSession removes the session with the given token hash.
func (a *AuthDB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions removes all sessions for a user except the one identified
// by exceptHash (the current session to keep).
func (a *AuthDB) DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_sessions WHERE user_id = ? AND token_hash != ?`,
		userID, exceptHash)
	return err
}

// CleanupExpiredSessions removes sessions that have exceeded either the idle
// timeout or the absolute timeout. Deletes in batches of 100. Returns total deleted.
func (a *AuthDB) CleanupExpiredSessions(ctx context.Context, now time.Time, idleTimeout, absTimeout time.Duration) (int64, error) {
	idleCutoff := now.Add(-idleTimeout)
	absCutoff := now.Add(-absTimeout)

	const batchSize = 100
	var total int64
	for {
		res, err := a.db.ExecContext(ctx, `
			DELETE FROM auth_sessions
			WHERE rowid IN (
				SELECT rowid FROM auth_sessions
				WHERE last_activity < ? OR created_at < ?
				LIMIT ?
			)`, idleCutoff, absCutoff, batchSize)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < batchSize {
			break
		}
	}
	if total > 0 {
		slog.Debug("expired sessions cleaned", "count", total)
	}
	return total, nil
}
