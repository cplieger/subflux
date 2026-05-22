package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"subflux/internal/api"
)

// SessionVerifier authenticates requests via session cookie.
type SessionVerifier struct {
	Store       SessionStore
	IdleTimeout time.Duration
	AbsTimeout  time.Duration
}

// Verify checks the session cookie and returns the user if valid.
func (v *SessionVerifier) Verify(ctx context.Context, r *http.Request) (*api.User, string, error) {
	token := ReadSessionCookie(r)
	if token == "" {
		return nil, "", nil
	}
	hash := SessionHash(token)
	sess, err := v.Store.GetSessionByHash(ctx, hash)
	if err != nil {
		slog.Debug("auth: session lookup failed", "error", err)
		return nil, "", nil
	}
	if sess == nil {
		return nil, "", nil
	}
	if ValidateSession(sess, v.IdleTimeout, v.AbsTimeout, time.Now()) != nil {
		return nil, "", nil
	}
	user, err := v.Store.GetUserByID(ctx, sess.UserID)
	if err != nil {
		slog.Debug("auth: user lookup failed", "user_id", sess.UserID, "error", err)
		return nil, "", nil
	}
	if user == nil || !user.Enabled {
		if user != nil {
			slog.Debug("auth: disabled user attempted session auth", "user_id", sess.UserID)
		}
		return nil, "", nil
	}
	return user, hash, nil
}
