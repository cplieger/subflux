package authhandlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
)

// SessionStore is the narrow store interface the Authenticator needs to
// resolve a request to a user via session cookie or API key.
type SessionStore interface {
	GetSessionByHash(ctx context.Context, tokenHash string) (*auth.Session, error)
	GetUserByID(ctx context.Context, id int64) (*auth.User, error)
	GetAPIKeyByHash(ctx context.Context, hash string) (*auth.Key, error)
}

// Compile-time assertion: the composite authstore satisfies SessionStore.
var _ SessionStore = authstore.AuthStore(nil)

// Authenticator resolves an HTTP request to an authenticated user. It chains a
// subflux session-cookie verifier with the library's API-key verifier.
type Authenticator struct {
	Store SessionStore
	// Bypass reports whether all authentication is disabled (nil means never).
	Bypass func() bool
	// Timeouts, when non-nil, resolves the session idle/absolute timeouts per
	// request (the server wires it to the live, hot-reloadable config so a
	// settings change takes effect without a restart). When nil, the static
	// IdleTimeout/AbsTimeout fields below are used.
	Timeouts    func() (idle, absolute time.Duration)
	IdleTimeout time.Duration
	AbsTimeout  time.Duration
}

// syntheticAdminUser is injected when Bypass returns true (auth.disable_auth).
var syntheticAdminUser = &auth.User{
	ID:       0,
	Username: "admin",
	Role:     auth.RoleAdmin,
	Enabled:  true,
}

// Authenticate checks session cookie first, then API key. Returns the user and
// session hash, or [auth.ErrUnauthenticated].
func (a *Authenticator) Authenticate(r *http.Request) (user *auth.User, sessHash string, err error) {
	if a.Bypass != nil && a.Bypass() {
		return syntheticAdminUser, "", nil
	}
	ctx := r.Context()
	for _, v := range a.verifiers() {
		user, hash, err := v.Verify(ctx, r)
		if err != nil {
			return nil, "", err
		}
		if user != nil {
			return user, hash, nil
		}
	}
	return nil, "", auth.ErrUnauthenticated
}

// RequireAuth checks authentication and returns the user. If not authenticated
// it writes the appropriate response (401 JSON for API clients, 302 to /login
// for browsers) and returns ok=false.
func (a *Authenticator) RequireAuth(w http.ResponseWriter, r *http.Request) (user *auth.User, sessHash string, ok bool) {
	user, sessHash, err := a.Authenticate(r)
	if err != nil {
		if auth.IsBrowserRequest(r) {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		} else {
			api.UnauthorizedC(w, r, api.CodeAuthSessionRequired, auth.ErrUnauthenticated.Error())
		}
		return nil, "", false
	}
	return user, sessHash, true
}

// verifiers returns the ordered credential verifiers: subflux session cookie,
// then the library's API-key verifier. Session timeouts come from the Timeouts
// provider when set (live config), falling back to the static fields.
func (a *Authenticator) verifiers() []auth.CredentialVerifier {
	idle, absolute := a.IdleTimeout, a.AbsTimeout
	if a.Timeouts != nil {
		idle, absolute = a.Timeouts()
	}
	return []auth.CredentialVerifier{
		&sessionVerifier{store: a.Store, idleTimeout: idle, absTimeout: absolute},
		auth.NewAPIKeyVerifier(a.Store),
	}
}

// sessionVerifier authenticates via subflux's session cookie. It differs from
// the library's session verifier in two ways: it reads subflux's per-request
// HTTP/HTTPS dual-name cookie, and it does not update session activity inline
// (the server batches activity writes via sessionActivityBatcher).
type sessionVerifier struct {
	store       SessionStore
	idleTimeout time.Duration
	absTimeout  time.Duration
}

// Verify checks the session cookie and returns the user if valid.
func (v *sessionVerifier) Verify(ctx context.Context, r *http.Request) (user *auth.User, sessHash string, err error) {
	token := ReadSessionCookie(r)
	if token == "" {
		return nil, "", nil
	}
	hash := auth.SessionHash(token)
	sess, err := v.store.GetSessionByHash(ctx, hash)
	if err != nil {
		slog.Debug("auth: session lookup failed", "error", err)
		return nil, "", nil
	}
	if sess == nil {
		return nil, "", nil
	}
	if auth.ValidateSession(sess, v.idleTimeout, v.absTimeout, time.Now()) != nil {
		return nil, "", nil
	}
	user, err = v.store.GetUserByID(ctx, sess.UserID)
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
