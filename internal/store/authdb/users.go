package authdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// Compile-time assertion: *AuthDB implements api.UserStore.
var _ api.UserStore = (*AuthDB)(nil)

var userScanner = txutil.TableScanner[api.User]{
	Columns: `id, username, email, display_name, password_hash, role,
	oidc_sub, oidc_issuer, totp_secret, totp_enabled,
	enabled, last_totp_step, created_at, updated_at`,
	Scan:     scanUser,
	ScanInto: scanUserInto,
}

func scanUser(row interface{ Scan(...any) error }) (*api.User, error) {
	var u api.User
	if err := scanUserInto(row, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func scanUserInto(row interface{ Scan(...any) error }, u *api.User) error {
	var totpSecret []byte
	return row.Scan(
		&u.ID, &u.Username, &u.Email, &u.DisplayName,
		&u.PasswordHash, &u.Role, &u.OIDCSub, &u.OIDCIssuer,
		&totpSecret, &u.TOTPEnabled, &u.Enabled, &u.LastTOTPStep,
		&u.CreatedAt, &u.UpdatedAt,
	)
}

// CreateUser inserts a new user and sets the ID on the struct from LastInsertId.
func (a *AuthDB) CreateUser(ctx context.Context, user *api.User) error {
	res, err := a.db.ExecContext(ctx, `
		INSERT INTO auth_users (username, email, display_name, password_hash, role,
			oidc_sub, oidc_issuer, totp_secret, totp_enabled,
			enabled, last_totp_step)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		user.Username, user.Email, user.DisplayName, user.PasswordHash, user.Role,
		user.OIDCSub, user.OIDCIssuer, user.TOTPEnabled,
		user.Enabled, user.LastTOTPStep,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	user.ID = id
	slog.Info("user created", "username", user.Username, "role", user.Role)
	return nil
}

// GetUserByID returns the user with the given ID, or nil if not found.
func (a *AuthDB) GetUserByID(ctx context.Context, id int64) (*api.User, error) {
	u, err := scanUser(a.db.QueryRowContext(ctx,
		`SELECT `+userScanner.Columns+` FROM auth_users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetUserByUsername returns the user with the given username (case-insensitive),
// or nil if not found.
func (a *AuthDB) GetUserByUsername(ctx context.Context, username string) (*api.User, error) {
	u, err := scanUser(a.db.QueryRowContext(ctx,
		`SELECT `+userScanner.Columns+` FROM auth_users WHERE username = ?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetUserByEmail returns the user with the given email (case-insensitive),
// or nil if not found.
func (a *AuthDB) GetUserByEmail(ctx context.Context, email string) (*api.User, error) {
	u, err := scanUser(a.db.QueryRowContext(ctx,
		`SELECT `+userScanner.Columns+` FROM auth_users WHERE email = ?`, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetUserByOIDCSub returns the user with the given OIDC subject identifier,
// or nil if not found.
func (a *AuthDB) GetUserByOIDCSub(ctx context.Context, sub string) (*api.User, error) {
	u, err := scanUser(a.db.QueryRowContext(ctx,
		`SELECT `+userScanner.Columns+` FROM auth_users WHERE oidc_sub = ?`, sub))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// ListUsers returns all users ordered by username.
func (a *AuthDB) ListUsers(ctx context.Context) ([]api.User, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT `+userScanner.Columns+` FROM auth_users ORDER BY username`) //nolint:gosec // G202: compile-time columns
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []api.User
	for rows.Next() {
		var u api.User
		if err := userScanner.ScanInto(rows, &u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateUser updates all mutable fields for the given user.
func (a *AuthDB) UpdateUser(ctx context.Context, user *api.User) error {
	_, err := a.db.ExecContext(ctx, `
		UPDATE auth_users SET
			username = ?, email = ?, display_name = ?, password_hash = ?,
			role = ?, oidc_sub = ?, oidc_issuer = ?, totp_enabled = ?,
			enabled = ?, last_totp_step = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		user.Username, user.Email, user.DisplayName, user.PasswordHash,
		user.Role, user.OIDCSub, user.OIDCIssuer, user.TOTPEnabled,
		user.Enabled, user.LastTOTPStep, user.ID,
	)
	return err
}

// DeleteUser removes the user with the given ID. CASCADE handles related rows.
func (a *AuthDB) DeleteUser(ctx context.Context, id int64) error {
	_, err := a.db.ExecContext(ctx,
		`DELETE FROM auth_users WHERE id = ?`, id)
	if err == nil {
		slog.Info("user deleted", "user_id", id)
	}
	return err
}

// UserCount returns the total number of user accounts.
func (a *AuthDB) UserCount(ctx context.Context) (int, error) {
	var count int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_users`).Scan(&count)
	return count, err
}
