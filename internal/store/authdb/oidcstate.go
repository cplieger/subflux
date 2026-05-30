package authdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"subflux/internal/api"
)

// Compile-time assertion: *AuthDB implements the OIDC state sub-interface.
var _ api.OIDCStateStore = (*AuthDB)(nil)

// ErrOIDCStateNotFound is returned when ConsumeOIDCState cannot find the
// requested state entry (expired or never existed).
var ErrOIDCStateNotFound = errors.New("oidc state not found")

// --- OIDC State methods ---

// CreateOIDCState inserts a new OIDC state entry for the authorization flow.
func (a *AuthDB) CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error {
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_oidc_states (state, nonce, code_verifier, redirect_uri)
		VALUES (?, ?, ?, ?)`,
		state, nonce, codeVerifier, redirectURI)
	return err
}

// oidcStateMaxAge bounds how long a pending OIDC authorization flow can be
// completed. Enforced at consumption so the validity window doesn't depend on
// the cleanup cadence. Must match scheduler.OIDCStateTTL.
const oidcStateMaxAge = 10 * time.Minute

// ConsumeOIDCState atomically retrieves and deletes an unexpired OIDC state
// entry. An expired entry (older than oidcStateMaxAge) is treated as not found.
// The age check uses SQLite's own clock (UTC, matching created_at's
// CURRENT_TIMESTAMP default) to avoid Go-local vs DB-UTC timezone skew.
func (a *AuthDB) ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error) {
	err = a.db.QueryRowContext(ctx, `
		DELETE FROM auth_oidc_states
		WHERE state = ? AND created_at > datetime('now', ?)
		RETURNING nonce, code_verifier, redirect_uri`,
		state, fmt.Sprintf("-%d seconds", int(oidcStateMaxAge.Seconds()))).
		Scan(&nonce, &codeVerifier, &redirectURI)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("%w: %s", ErrOIDCStateNotFound, state[:min(8, len(state))])
	}
	if err == nil {
		slog.Debug("oidc state consumed", "state_prefix", state[:min(8, len(state))])
	}
	return
}

// CleanupExpiredOIDCStates removes OIDC state entries older than maxAge.
func (a *AuthDB) CleanupExpiredOIDCStates(ctx context.Context, now time.Time, maxAge time.Duration) (int64, error) {
	cutoff := now.Add(-maxAge)
	res, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_oidc_states WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		slog.Debug("expired oidc states cleaned", "count", n)
	}
	return n, nil
}
