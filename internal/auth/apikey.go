package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"

	"subflux/internal/api"
)

// ErrInvalidAPIKey is returned when an API key cannot be verified.
var ErrInvalidAPIKey = errors.New("invalid API key")

// GenerateAPIKey generates a new API key with 256 bits of entropy.
// It returns the plaintext key (prefixed with "sfx_"), its SHA-256 hash,
// a display prefix (first 8 chars), and a display suffix (last 4 chars).
func GenerateAPIKey() (plaintext, hash, prefix, suffix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", "", err
	}
	plaintext = "sfx_" + hex.EncodeToString(b)
	hash = APIKeyHash(plaintext)
	prefix = plaintext[:8]
	suffix = plaintext[len(plaintext)-4:]
	return plaintext, hash, prefix, suffix, nil
}

// VerifyAPIKey hashes the provided key, looks it up in the store, and
// returns the matching APIKey record. Uses constant-time comparison to
// prevent timing side-channel leaks.
func VerifyAPIKey(ctx context.Context, store SessionStore, key string) (*api.Key, error) {
	hash := APIKeyHash(key)
	apiKey, err := store.GetAPIKeyByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrInvalidAPIKey
	}
	// Constant-time comparison even though the DB lookup is by exact match,
	// to prevent any timing leak from the comparison itself.
	if subtle.ConstantTimeCompare([]byte(hash), []byte(apiKey.KeyHash)) != 1 {
		return nil, ErrInvalidAPIKey
	}
	return apiKey, nil
}

// APIKeyHash returns the hex-encoded SHA-256 hash of a key string.
func APIKeyHash(key string) string {
	return HexSHA256(key)
}
