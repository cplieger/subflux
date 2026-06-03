// Package auth implements authentication logic for Subflux:
// password hashing (Argon2id), TOTP, WebAuthn, OIDC, sessions,
// API keys, rate limiting, and RBAC middleware.
package auth

import (
	authlib "github.com/cplieger/auth"
)

// DummyHash returns a pre-computed Argon2id hash used by the login handler
// to equalize timing when the username doesn't exist (H2 mitigation).
// The hash is computed lazily on first call via sync.Once.
func DummyHash() string {
	return authlib.DummyHash()
}

// HashPassword hashes a password using Argon2id with OWASP parameters.
// Returns the hash in PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<base64-salt>$<base64-hash>
func HashPassword(password string) (string, error) {
	return authlib.HashPassword(password)
}

// VerifyPassword verifies a password against an encoded Argon2id hash
// in PHC string format. Uses constant-time comparison.
func VerifyPassword(password, encodedHash string) (bool, error) {
	return authlib.VerifyPassword(password, encodedHash)
}
