package auth

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
)

// ErrUnauthenticated is returned when no valid credential is found.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator resolves an HTTP request to an authenticated user.
type Authenticator struct {
	Store SessionStore
	// Bypass reports whether all authentication is disabled (nil means never).
	Bypass      func() bool
	IdleTimeout time.Duration
	AbsTimeout  time.Duration
}

// syntheticAdminUser is the user injected when BypassAuth is true.
var syntheticAdminUser = &api.User{
	ID:       0,
	Username: "admin",
	Role:     api.RoleAdmin,
	Enabled:  true,
}

// Authenticate checks session cookie first, then API key header, then API key
// query param. Returns the user and session hash, or [ErrUnauthenticated].
func (a *Authenticator) Authenticate(r *http.Request) (*api.User, string, error) {
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
func HasRole(user *api.User, role api.Role) bool {
	return user.Role == role || user.Role == api.RoleAdmin
}

// ValidateRedirectURI ensures the URI is a safe relative path.
func ValidateRedirectURI(uri string) string {
	return authlib.ValidateRedirectURI(uri)
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
