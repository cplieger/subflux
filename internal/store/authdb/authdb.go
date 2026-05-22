// Package authdb provides SQLite-backed persistence for authentication data
// (users, sessions, passkeys, API keys, TOTP, OIDC state). It implements
// authstore.AuthStore and is embedded by the parent store.DB to satisfy that
// interface without coupling auth persistence to subtitle/search state.
package authdb

import (
	"context"
	"database/sql"
	"fmt"

	"subflux/internal/authstore"
)

// Compile-time assertion: *AuthDB implements authstore.AuthStore.
var _ authstore.AuthStore = (*AuthDB)(nil)

// AuthDB wraps a shared *sql.DB for auth-specific persistence.
type AuthDB struct {
	db *sql.DB

	// Prepared statements for high-frequency auth queries.
	stmtGetAPIKeyByHash  *sql.Stmt
	stmtGetSessionByHash *sql.Stmt
}

// New creates an AuthDB using the provided database connection.
// It prepares frequently-used statements for auth queries.
func New(ctx context.Context, db *sql.DB) (*AuthDB, error) {
	a := &AuthDB{db: db}
	if err := a.prepareStatements(ctx); err != nil {
		return nil, fmt.Errorf("authdb: prepare statements: %w", err)
	}
	return a, nil
}

// Close closes prepared statements. The underlying *sql.DB is NOT closed
// because it is shared with the parent store.
func (a *AuthDB) Close(ctx context.Context) error {
	if a.stmtGetAPIKeyByHash != nil {
		a.stmtGetAPIKeyByHash.Close()
	}
	if a.stmtGetSessionByHash != nil {
		a.stmtGetSessionByHash.Close()
	}
	return nil
}

// prepareStatements prepares frequently-used auth queries once at startup.
func (a *AuthDB) prepareStatements(ctx context.Context) error {
	var err error
	a.stmtGetAPIKeyByHash, err = a.db.PrepareContext(ctx,
		`SELECT `+apiKeyScanner.Columns+` FROM auth_api_keys WHERE key_hash = ?`)
	if err != nil {
		return err
	}
	a.stmtGetSessionByHash, err = a.db.PrepareContext(ctx,
		`SELECT `+sessionScanner.Columns+` FROM auth_sessions WHERE token_hash = ?`)
	if err != nil {
		return err
	}
	return nil
}
