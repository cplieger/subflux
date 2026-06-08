package authdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/txutil"
)

// Compile-time assertion: *AuthDB implements api.PasskeyStore.
var _ api.PasskeyStore = (*AuthDB)(nil)

var passkeyScanner = txutil.TableScanner[api.PasskeyCredential]{
	Columns: `id, user_id, credential_id, public_key, aaguid,
	attestation_type, transport, sign_count, name, backup_eligible,
	backup_state, user_present, user_verified, raw_attestation, created_at`,
	Scan:     scanPasskey,
	ScanInto: scanPasskeyInto,
}

func scanPasskey(row interface{ Scan(...any) error }) (*api.PasskeyCredential, error) {
	var p api.PasskeyCredential
	if err := scanPasskeyInto(row, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func scanPasskeyInto(row interface{ Scan(...any) error }, p *api.PasskeyCredential) error {
	return row.Scan(
		&p.ID, &p.UserID, &p.CredentialID, &p.PublicKey,
		&p.AAGUID, &p.AttestationType, &p.Transport,
		&p.SignCount, &p.Name, &p.BackupEligible, &p.BackupState,
		&p.UserPresent, &p.UserVerified, &p.RawAttestation, &p.CreatedAt,
	)
}

// CreatePasskey inserts a new passkey credential and sets the ID from LastInsertId.
func (a *AuthDB) CreatePasskey(ctx context.Context, cred *api.PasskeyCredential) error {
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_passkeys (user_id, credential_id, public_key, aaguid,
			attestation_type, transport, sign_count, name,
			backup_eligible, backup_state, user_present, user_verified,
			raw_attestation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cred.UserID, cred.CredentialID, cred.PublicKey, cred.AAGUID,
		cred.AttestationType, cred.Transport, cred.SignCount, cred.Name,
		cred.BackupEligible, cred.BackupState, cred.UserPresent, cred.UserVerified,
		cred.RawAttestation,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	cred.ID = id
	slog.Info("passkey registered", "user_id", cred.UserID, "name", cred.Name)
	return nil
}

// GetPasskeysByUserID returns all passkeys for a user, ordered by creation date.
func (a *AuthDB) GetPasskeysByUserID(ctx context.Context, userID int64) ([]api.PasskeyCredential, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT `+passkeyScanner.Columns+` FROM auth_passkeys WHERE user_id = ? ORDER BY created_at`, //nolint:gosec // G202: compile-time columns
		userID)
	if err != nil {
		return nil, err
	}
	return queryList(rows, passkeyScanner.ScanInto)
}

// GetPasskeyByCredentialID returns the passkey with the given credential ID,
// or nil if not found.
func (a *AuthDB) GetPasskeyByCredentialID(ctx context.Context, credID []byte) (*api.PasskeyCredential, error) {
	p, err := scanPasskey(a.db.QueryRowContext(ctx,
		`SELECT `+passkeyScanner.Columns+` FROM auth_passkeys WHERE credential_id = ?`, credID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// UpdatePasskeyAfterLogin updates the sign count and authenticator flags for a
// passkey after a successful login ceremony.
func (a *AuthDB) UpdatePasskeyAfterLogin(ctx context.Context, credID []byte, signCount uint32, flags api.PasskeyFlags) error {
	_, err := a.db.ExecContext(ctx, `
		UPDATE auth_passkeys SET sign_count = ?, backup_eligible = ?,
			backup_state = ?, user_present = ?, user_verified = ?
		WHERE credential_id = ?`,
		signCount, flags.BackupEligible, flags.BackupState,
		flags.UserPresent, flags.UserVerified, credID)
	return err
}

// RenamePasskey updates the friendly name of a passkey owned by the given user.
func (a *AuthDB) RenamePasskey(ctx context.Context, id, userID int64, name string) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE auth_passkeys SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	return err
}

// DeletePasskey removes the passkey with the given ID owned by the given user.
func (a *AuthDB) DeletePasskey(ctx context.Context, id, userID int64) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if err == nil {
		slog.Info("passkey deleted", "passkey_id", id, "user_id", userID)
	}
	return err
}

// PasskeyCountForUser returns the number of passkeys registered for a user.
func (a *AuthDB) PasskeyCountForUser(ctx context.Context, userID int64) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_passkeys WHERE user_id = ?`, userID).Scan(&count)
	return count, err
}
