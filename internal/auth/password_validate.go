package auth

import (
	"context"
	"net/http"

	authlib "github.com/cplieger/auth"
)

// PasswordMinLengthMultiFactor is the minimum password length when password
// login is not the sole sufficient factor.
const PasswordMinLengthMultiFactor = authlib.PasswordMinLengthMultiFactor

// PasswordMinLengthSolo is the minimum password length when password login is
// enabled and thus a sole sufficient factor.
const PasswordMinLengthSolo = authlib.PasswordMinLengthSolo

// ValidatePasswordLength enforces minimum and maximum password length.
func ValidatePasswordLength(password string, passwordOnly bool) error {
	return authlib.ValidatePasswordLength(password, passwordOnly)
}

// ValidatePasswordContext rejects passwords that trivially embed the username
// or the application name.
func ValidatePasswordContext(password, username string) error {
	return authlib.ValidatePasswordContext(password, username, []string{"subflux"})
}

// CheckBreachedPassword checks a password against the Have I Been Pwned
// Passwords API using k-anonymity. Returns true if the password has been
// found in a breach.
func CheckBreachedPassword(ctx context.Context, client *http.Client, password string) (bool, error) {
	return authlib.CheckBreachedPassword(ctx, client, password)
}
