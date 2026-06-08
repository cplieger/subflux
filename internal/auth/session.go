package auth

import (
	"errors"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
)

// Sentinel errors for session operations.
var (
	ErrSessionExpired  = errors.New("session expired")
	ErrSessionNotFound = errors.New("session not found")
)

// GenerateSessionToken generates a cryptographically random session token
// (256 bits / 32 bytes). It returns the hex-encoded plaintext token and
// its SHA-256 hash (also hex-encoded).
func GenerateSessionToken() (plaintext, hash string, err error) {
	return authlib.GenerateSessionToken()
}

// ValidateSession checks whether a session is still valid given the idle
// and absolute timeout durations.
func ValidateSession(sess *api.Session, idleTimeout, absTimeout time.Duration, now time.Time) error {
	if now.Sub(sess.LastActivity) > idleTimeout {
		return ErrSessionExpired
	}
	if now.Sub(sess.CreatedAt) > absTimeout {
		return ErrSessionExpired
	}
	if sess.OIDCExpiry != nil && now.After(*sess.OIDCExpiry) {
		return ErrSessionExpired
	}
	return nil
}

// SessionHash returns the hex-encoded SHA-256 hash of a plaintext token.
func SessionHash(token string) string {
	return authlib.SessionHash(token)
}
