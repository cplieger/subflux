package auth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"subflux/internal/api"
)

// ErrUnauthenticated is returned when no valid credential is found.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator resolves an HTTP request to an authenticated user.
//
// When BypassAuth is true (operator escape hatch via config.disable_auth),
// Authenticate short-circuits to a synthetic admin user before touching
// any credentials or the store. This is the single enforcement point for
// the bypass; no caller should re-check the flag.
type Authenticator struct {
	Store       SessionStore
	IdleTimeout time.Duration
	AbsTimeout  time.Duration
	BypassAuth  bool
}

// syntheticAdminUser is the user injected when BypassAuth is true. It has
// ID 0 (no DB row), is enabled, and carries the admin role so role guards
// pass. Shared package-level instance — handlers treat it as read-only.
var syntheticAdminUser = &api.User{
	ID:       0,
	Username: "admin",
	Role:     api.RoleAdmin,
	Enabled:  true,
}

// Authenticate checks session cookie first, then API key header, then API key
// query param. Returns the user and session hash (for session activity updates),
// or [ErrUnauthenticated] if no valid credential is found.
//
// When session validation fails (expired, disabled user, DB error), authentication
// falls through to API key methods rather than returning an error immediately.
//
// When BypassAuth is true, returns the synthetic admin with an empty session
// hash before any credential lookup.
func (a *Authenticator) Authenticate(r *http.Request) (*api.User, string, error) {
	if a.BypassAuth {
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
	return nil, "", ErrUnauthenticated
}

// RequireAuth checks authentication and returns the user. If not
// authenticated, it writes the appropriate response (401 or redirect)
// and returns ok=false.
func (a *Authenticator) RequireAuth(w http.ResponseWriter, r *http.Request) (*api.User, string, bool) {
	user, sessHash, err := a.Authenticate(r)
	if err != nil {
		if IsBrowserRequest(r) {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		} else {
			api.UnauthorizedC(w, r, api.CodeAuthSessionRequired, ErrUnauthenticated.Error())
		}
		return nil, "", false
	}
	return user, sessHash, true
}

// verifiers returns the ordered list of credential verifiers.
func (a *Authenticator) verifiers() []CredentialVerifier {
	return []CredentialVerifier{
		&SessionVerifier{Store: a.Store, IdleTimeout: a.IdleTimeout, AbsTimeout: a.AbsTimeout},
		&APIKeyVerifier{Store: a.Store},
	}
}

// HasRole reports whether the user is authorized for the given role.
// Admin is a superset of user: an admin user passes any role check.
func HasRole(user *api.User, role api.Role) bool {
	return user.Role == role || user.Role == api.RoleAdmin
}

// ValidateRedirectURI ensures the URI is a safe relative path.
// Returns "/" if the URI is empty, absolute, has a scheme, has a host,
// or uses backslashes (some browsers normalize \\evil to //evil).
func ValidateRedirectURI(uri string) string {
	// Canonical open-redirect guard (per CodeQL go/bad-redirect-check):
	// require a leading '/' AND a second character that is neither '/'
	// nor '\\'. Browsers treat "//evil.com" and "/\evil.com" as
	// absolute URLs, so checking only the first character is unsafe.
	// A bare "/" (len 1) is allowed as the safe default below.
	if uri == "/" {
		return "/"
	}
	if len(uri) < 2 || uri[0] != '/' || uri[1] == '/' || uri[1] == '\\' {
		return "/"
	}
	// Reject any explicit scheme ("http://", "javascript:" smuggled
	// mid-path, etc.).
	if strings.Contains(uri, "://") {
		return "/"
	}
	// Reject backslashes anywhere: some browsers normalize "\evil.com"
	// to "//evil.com" before navigation.
	if strings.ContainsAny(uri, "\\") {
		return "/"
	}
	// Defense-in-depth: parse and verify no scheme + no host. url.Parse
	// catches edge cases that string-prefix checks miss (control chars,
	// non-ASCII path segments that some routers interpret).
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "/"
	}
	return uri
}

// CanDisableAuthMethod checks whether disabling the given auth method would
// leave the user with no viable authentication method.
func CanDisableAuthMethod(method api.AuthMethod, hasPassword bool, passkeyCount int, oidcEnabled, oidcLinked bool) bool {
	remaining := 0
	if method != api.MethodPassword && hasPassword {
		remaining++
	}
	if method != api.MethodPasskey && passkeyCount > 0 {
		remaining++
	}
	if method != api.MethodOIDC && oidcEnabled && oidcLinked {
		remaining++
	}
	return remaining > 0
}
