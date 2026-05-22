package authdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// Compile-time assertions: *AuthDB implements TOTP and OIDC sub-interfaces.
var (
	_ api.TOTPStore      = (*AuthDB)(nil)
	_ api.OIDCStateStore = (*AuthDB)(nil)
)

// ErrOIDCStateNotFound is returned when ConsumeOIDCState cannot find the
// requested state entry (expired or never existed).
var ErrOIDCStateNotFound = errors.New("oidc state not found")

// --- TOTP methods ---

// SetTOTPSecret enables TOTP for a user by storing the encrypted secret.
func (a *AuthDB) SetTOTPSecret(ctx context.Context, userID int64, encryptedSecret []byte) error {
	_, err := a.db.ExecContext(ctx, `
		UPDATE auth_users SET totp_secret = ?, totp_enabled = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		encryptedSecret, userID)
	if err == nil {
		slog.Info("totp enabled", "user_id", userID)
	}
	return err
}

// GetTOTPSecret returns the encrypted TOTP secret for a user, or nil if none
// is set.
func (a *AuthDB) GetTOTPSecret(ctx context.Context, userID int64) ([]byte, error) {
	var secret []byte
	err := a.db.QueryRowContext(ctx,
		`SELECT totp_secret FROM auth_users WHERE id = ?`, userID).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return secret, err
}

// ClearTOTPSecret removes the TOTP secret and disables TOTP for a user.
func (a *AuthDB) ClearTOTPSecret(ctx context.Context, userID int64) error {
	_, err := a.db.ExecContext(ctx, `
		UPDATE auth_users SET totp_secret = NULL, totp_enabled = 0, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		userID)
	if err == nil {
		slog.Info("totp disabled", "user_id", userID)
	}
	return err
}

// --- Recovery code methods ---

// SetRecoveryCodes replaces all recovery codes for a user.
func (a *AuthDB) SetRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer txutil.DeferRollback(tx)

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM auth_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}

	if len(hashes) > 0 {
		var sb strings.Builder
		sb.WriteString(`INSERT INTO auth_recovery_codes (user_id, code_hash) VALUES `)
		args := make([]any, 0, len(hashes)*2)
		for i, h := range hashes {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?)")
			args = append(args, userID, h)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info("recovery codes set", "user_id", userID, "count", len(hashes))
	return nil
}

// UseRecoveryCode marks a recovery code as used. Returns true if a matching
// unused code was found and consumed.
func (a *AuthDB) UseRecoveryCode(ctx context.Context, userID int64, codeHash string) (bool, error) {
	res, err := a.db.ExecContext(ctx, `
		UPDATE auth_recovery_codes SET used = 1
		WHERE user_id = ? AND code_hash = ? AND used = 0`,
		userID, codeHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		slog.Info("recovery code used", "user_id", userID)
	}
	return n > 0, nil
}

// RecoveryCodeCount returns the number of unused recovery codes for a user.
func (a *AuthDB) RecoveryCodeCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_recovery_codes WHERE user_id = ? AND used = 0`,
		userID).Scan(&count)
	return count, err
}

// --- OIDC State methods ---

// CreateOIDCState inserts a new OIDC state entry for the authorization flow.
func (a *AuthDB) CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error {
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_oidc_states (state, nonce, code_verifier, redirect_uri)
		VALUES (?, ?, ?, ?)`,
		state, nonce, codeVerifier, redirectURI)
	return err
}

// ConsumeOIDCState atomically retrieves and deletes an OIDC state entry.
func (a *AuthDB) ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error) {
	err = a.db.QueryRowContext(ctx, `
		DELETE FROM auth_oidc_states WHERE state = ?
		RETURNING nonce, code_verifier, redirect_uri`, state).
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
