package store

import (
	"context"
	"time"

	"subflux/internal/api"
)

// --- User delegation ---

func (d *DB) CreateUser(ctx context.Context, user *api.User) error {
	return d.authDB.CreateUser(ctx, user)
}

func (d *DB) GetUserByID(ctx context.Context, id int64) (*api.User, error) {
	return d.authDB.GetUserByID(ctx, id)
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (*api.User, error) {
	return d.authDB.GetUserByUsername(ctx, username)
}

func (d *DB) GetUserByEmail(ctx context.Context, email string) (*api.User, error) {
	return d.authDB.GetUserByEmail(ctx, email)
}

func (d *DB) GetUserByOIDCSub(ctx context.Context, issuer, sub string) (*api.User, error) {
	return d.authDB.GetUserByOIDCSub(ctx, issuer, sub)
}

func (d *DB) ListUsers(ctx context.Context) ([]api.User, error) {
	return d.authDB.ListUsers(ctx)
}

func (d *DB) UpdateUser(ctx context.Context, user *api.User) error {
	return d.authDB.UpdateUser(ctx, user)
}

func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	return d.authDB.DeleteUser(ctx, id)
}

func (d *DB) UserCount(ctx context.Context) (int, error) {
	return d.authDB.UserCount(ctx)
}

// --- Session delegation ---

func (d *DB) CreateSession(ctx context.Context, sess *api.Session) error {
	return d.authDB.CreateSession(ctx, sess)
}

func (d *DB) GetSessionByHash(ctx context.Context, tokenHash string) (*api.Session, error) {
	return d.authDB.GetSessionByHash(ctx, tokenHash)
}

func (d *DB) UpdateSessionActivity(ctx context.Context, tokenHash string, now time.Time) error {
	return d.authDB.UpdateSessionActivity(ctx, tokenHash, now)
}

func (d *DB) BatchUpdateSessionActivity(ctx context.Context, tokenHashes []string, now time.Time) error {
	return d.authDB.BatchUpdateSessionActivity(ctx, tokenHashes, now)
}

func (d *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	return d.authDB.DeleteSession(ctx, tokenHash)
}

func (d *DB) DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error {
	return d.authDB.DeleteUserSessions(ctx, userID, exceptHash)
}

func (d *DB) CleanupExpiredSessions(ctx context.Context, now time.Time, idleTimeout, absTimeout time.Duration) (int64, error) {
	return d.authDB.CleanupExpiredSessions(ctx, now, idleTimeout, absTimeout)
}

// --- Passkey delegation ---

func (d *DB) CreatePasskey(ctx context.Context, cred *api.PasskeyCredential) error {
	return d.authDB.CreatePasskey(ctx, cred)
}

func (d *DB) GetPasskeysByUserID(ctx context.Context, userID int64) ([]api.PasskeyCredential, error) {
	return d.authDB.GetPasskeysByUserID(ctx, userID)
}

func (d *DB) GetPasskeyByCredentialID(ctx context.Context, credID []byte) (*api.PasskeyCredential, error) {
	return d.authDB.GetPasskeyByCredentialID(ctx, credID)
}

func (d *DB) UpdatePasskeyAfterLogin(ctx context.Context, credID []byte, signCount uint32, flags api.PasskeyFlags) error {
	return d.authDB.UpdatePasskeyAfterLogin(ctx, credID, signCount, flags)
}

func (d *DB) RenamePasskey(ctx context.Context, id, userID int64, name string) error {
	return d.authDB.RenamePasskey(ctx, id, userID, name)
}

func (d *DB) DeletePasskey(ctx context.Context, id, userID int64) error {
	return d.authDB.DeletePasskey(ctx, id, userID)
}

func (d *DB) PasskeyCountForUser(ctx context.Context, userID int64) (int, error) {
	return d.authDB.PasskeyCountForUser(ctx, userID)
}

// --- API Key delegation ---

func (d *DB) CreateAPIKey(ctx context.Context, key *api.Key) error {
	return d.authDB.CreateAPIKey(ctx, key)
}

func (d *DB) GetAPIKeyByHash(ctx context.Context, hash string) (*api.Key, error) {
	return d.authDB.GetAPIKeyByHash(ctx, hash)
}

func (d *DB) ListAPIKeysByUserID(ctx context.Context, userID int64) ([]api.Key, error) {
	return d.authDB.ListAPIKeysByUserID(ctx, userID)
}

func (d *DB) DeleteAPIKey(ctx context.Context, id, userID int64) error {
	return d.authDB.DeleteAPIKey(ctx, id, userID)
}

// --- OIDC State delegation ---

func (d *DB) CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error {
	return d.authDB.CreateOIDCState(ctx, state, nonce, codeVerifier, redirectURI)
}

func (d *DB) ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error) {
	return d.authDB.ConsumeOIDCState(ctx, state)
}

func (d *DB) CleanupExpiredOIDCStates(ctx context.Context, now time.Time, maxAge time.Duration) (int64, error) {
	return d.authDB.CleanupExpiredOIDCStates(ctx, now, maxAge)
}
