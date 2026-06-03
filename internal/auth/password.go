// Package auth implements authentication logic for Subflux.
//
// This package delegates core cryptographic operations to the shared
// github.com/cplieger/auth library while preserving the subflux-specific
// API surface and internal/api types.
package auth

import authlib "github.com/cplieger/auth"

// Argon2id parameters per OWASP recommendations (exposed for tests).
const (
	argonMemory      = 19456
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLen     = 16
	argonKeyLen      = 32
)

// HashPassword hashes a password using Argon2id with OWASP parameters.
// Returns the hash in PHC string format.
func HashPassword(password string) (string, error) {
	return authlib.HashPassword(password)
}

// VerifyPassword verifies a password against an encoded Argon2id hash
// in PHC string format. Uses constant-time comparison.
func VerifyPassword(password, encodedHash string) (bool, error) {
	return authlib.VerifyPassword(password, encodedHash)
}

// DummyHash returns a pre-computed Argon2id hash used by the login handler
// to equalize timing when the username does not exist (H2 mitigation).
func DummyHash() string {
	return authlib.DummyHash()
}

// NeedsRehash reports whether the encoded hash was produced with parameters
// different from the current OWASP-recommended defaults.
func NeedsRehash(encodedHash string) bool {
	return authlib.NeedsRehash(encodedHash)
}
