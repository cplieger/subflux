package authdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// Compile-time assertion: *AuthDB implements api.KeyStore.
var _ api.KeyStore = (*AuthDB)(nil)

var apiKeyScanner = txutil.TableScanner[api.Key]{
	Columns:  `id, user_id, key_hash, key_prefix, key_suffix, label, created_at`,
	Scan:     scanAPIKey,
	ScanInto: scanAPIKeyInto,
}

func scanAPIKey(row interface{ Scan(...any) error }) (*api.Key, error) {
	var k api.Key
	if err := scanAPIKeyInto(row, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

func scanAPIKeyInto(row interface{ Scan(...any) error }, k *api.Key) error {
	return row.Scan(
		&k.ID, &k.UserID, &k.KeyHash, &k.KeyPrefix,
		&k.KeySuffix, &k.Label, &k.CreatedAt,
	)
}

// CreateAPIKey inserts a new API key and sets the ID from LastInsertId.
func (a *AuthDB) CreateAPIKey(ctx context.Context, key *api.Key) error {
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_api_keys (user_id, key_hash, key_prefix, key_suffix, label)
		VALUES (?, ?, ?, ?, ?)`,
		key.UserID, key.KeyHash, key.KeyPrefix, key.KeySuffix, key.Label,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	key.ID = id
	slog.Info("api key created", "user_id", key.UserID, "label", key.Label)
	return nil
}

// GetAPIKeyByHash returns the API key with the given hash, or nil if not found.
func (a *AuthDB) GetAPIKeyByHash(ctx context.Context, hash string) (*api.Key, error) {
	k, err := scanAPIKey(a.stmtGetAPIKeyByHash.QueryRowContext(ctx, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// ListAPIKeysByUserID returns all API keys for a user, ordered by creation
// date descending (newest first).
func (a *AuthDB) ListAPIKeysByUserID(ctx context.Context, userID int64) ([]api.Key, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT `+apiKeyScanner.Columns+` FROM auth_api_keys WHERE user_id = ? ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	return queryList(rows, apiKeyScanner.ScanInto)
}

// DeleteAPIKey removes the API key with the given ID owned by the given user.
func (a *AuthDB) DeleteAPIKey(ctx context.Context, id, userID int64) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err == nil {
		slog.Info("api key deleted", "key_id", id, "user_id", userID)
	}
	return err
}
