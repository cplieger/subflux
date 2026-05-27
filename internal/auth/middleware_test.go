package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 4: Session invalidation on password change
// **Validates: Requirements 1.5**
func TestProperty_SessionInvalidationOnPasswordChange(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		db := newFakeSessionStore()
		ctx := context.Background()

		// Create a user.
		user := &api.User{
			Username:     "testuser",
			PasswordHash: "dummy",
			Role:         "admin",
			Enabled:      true,
		}
		if err := db.CreateUser(ctx, user); err != nil {
			rt.Fatalf("CreateUser: %v", err)
		}

		// Create N sessions (1 to 10).
		n := rapid.IntRange(1, 10).Draw(rt, "numSessions")
		hashes := make([]string, n)
		for i := range n {
			_, hash, err := GenerateSessionToken()
			if err != nil {
				rt.Fatalf("GenerateSessionToken[%d]: %v", i, err)
			}
			hashes[i] = hash
			sess := &api.Session{
				TokenHash:  hash,
				UserID:     user.ID,
				AuthMethod: "password",
				IPAddress:  "127.0.0.1",
			}
			if err := db.CreateSession(ctx, sess); err != nil {
				rt.Fatalf("CreateSession[%d]: %v", i, err)
			}
		}

		// Pick one session to keep (the "current" session).
		keepIdx := rapid.IntRange(0, n-1).Draw(rt, "keepIdx")
		exceptHash := hashes[keepIdx]

		// Simulate password change: delete all sessions except the current one.
		if err := db.DeleteUserSessions(ctx, user.ID, exceptHash); err != nil {
			rt.Fatalf("DeleteUserSessions: %v", err)
		}

		// Verify exactly 1 session remains (the excepted one).
		remaining := 0
		for _, h := range hashes {
			s, err := db.GetSessionByHash(ctx, h)
			if err != nil {
				rt.Fatalf("GetSessionByHash(%s): %v", h, err)
			}
			if s != nil {
				remaining++
				if h != exceptHash {
					rt.Fatalf("session %s should have been deleted", h)
				}
			}
		}
		if remaining != 1 {
			rt.Fatalf("expected 1 remaining session, got %d", remaining)
		}
	})
}

// Feature: subflux-authentication, Property 8: Last authentication method guard
// **Validates: Requirements 3.4, 6.3**
func TestProperty_LastAuthMethodGuard(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		hasPassword := rapid.Bool().Draw(t, "hasPassword")
		passkeyCount := rapid.IntRange(0, 5).Draw(t, "passkeyCount")
		oidcEnabled := rapid.Bool().Draw(t, "oidcEnabled")
		oidcLinked := rapid.Bool().Draw(t, "oidcLinked")

		// Count viable methods.
		viable := 0
		if hasPassword {
			viable++
		}
		if passkeyCount > 0 {
			viable++
		}
		if oidcEnabled && oidcLinked {
			viable++
		}

		methods := []api.AuthMethod{api.MethodPassword, api.MethodPasskey, api.MethodOIDC}
		for _, method := range methods {
			canDisable := CanDisableAuthMethod(method, hasPassword, passkeyCount, oidcEnabled, oidcLinked)

			// Count how many methods remain after disabling this one.
			remainingViable := viable
			switch method {
			case api.MethodPassword:
				if hasPassword {
					remainingViable--
				}
			case api.MethodPasskey:
				if passkeyCount > 0 {
					remainingViable--
				}
			case api.MethodOIDC:
				if oidcEnabled && oidcLinked {
					remainingViable--
				}
			}

			if remainingViable > 0 && !canDisable {
				t.Fatalf("should allow disabling %s: hasPassword=%v passkeys=%d oidc=%v/%v",
					method, hasPassword, passkeyCount, oidcEnabled, oidcLinked)
			}
			if remainingViable <= 0 && canDisable {
				t.Fatalf("should NOT allow disabling %s (last method): hasPassword=%v passkeys=%d oidc=%v/%v",
					method, hasPassword, passkeyCount, oidcEnabled, oidcLinked)
			}
		}
	})
}

// Feature: subflux-authentication, Property 16: API key role inheritance
// **Validates: Requirements 11.8**
func TestProperty_APIKeyRoleInheritance(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		db := newFakeSessionStore()
		ctx := context.Background()

		role := rapid.SampledFrom([]string{"admin", "user"}).Draw(rt, "role")
		username := fmt.Sprintf("user_%s", rapid.StringMatching(`[a-z]{4,8}`).Draw(rt, "username"))

		// Create a user with the given role.
		user := &api.User{
			Username:     username,
			PasswordHash: "dummy",
			Role:         api.Role(role),
			Enabled:      true,
		}
		if err := db.CreateUser(ctx, user); err != nil {
			rt.Fatalf("CreateUser: %v", err)
		}

		// Generate and store an API key for this user.
		plaintext, hash, prefix, suffix, err := GenerateAPIKey()
		if err != nil {
			rt.Fatalf("GenerateAPIKey: %v", err)
		}
		apiKey := &api.Key{
			UserID:    user.ID,
			KeyHash:   hash,
			KeyPrefix: prefix,
			KeySuffix: suffix,
			Label:     "test",
		}
		if err := db.CreateAPIKey(ctx, apiKey); err != nil {
			rt.Fatalf("CreateAPIKey: %v", err)
		}

		// Verify the API key lookup returns the correct user.
		verified, err := VerifyAPIKey(ctx, db, plaintext)
		if err != nil {
			rt.Fatalf("VerifyAPIKey: %v", err)
		}
		if verified.UserID != user.ID {
			rt.Fatalf("API key user ID mismatch: got %d, want %d", verified.UserID, user.ID)
		}

		// Look up the user to get their role.
		resolvedUser, err := db.GetUserByID(ctx, verified.UserID)
		if err != nil {
			rt.Fatalf("GetUserByID: %v", err)
		}
		if resolvedUser.Role != api.Role(role) {
			rt.Fatalf("role mismatch: got %s, want %s", resolvedUser.Role, role)
		}
	})
}

// --- Unit tests for pure/near-pure middleware functions ---

func TestIsBrowserRequest_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		accept string
		apiKey string
		want   bool
	}{
		{name: "browser html", accept: "text/html,application/xhtml+xml", apiKey: "", want: true},
		{name: "browser with wildcard", accept: "text/html, */*", apiKey: "", want: true},
		{name: "api client json", accept: "application/json", apiKey: "", want: false},
		{name: "api key overrides browser", accept: "text/html", apiKey: "sfx_abc123", want: false},
		{name: "empty accept", accept: "", apiKey: "", want: false},
		{name: "api key with empty accept", accept: "", apiKey: "sfx_abc123", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tt.accept != "" {
				r.Header.Set("Accept", tt.accept)
			}
			if tt.apiKey != "" {
				r.Header.Set("X-API-Key", tt.apiKey)
			}
			got := IsBrowserRequest(r)
			if got != tt.want {
				t.Errorf("IsBrowserRequest(accept=%q, apiKey=%q) = %v, want %v",
					tt.accept, tt.apiKey, got, tt.want)
			}
		})
	}
}

func TestIsHTTPS_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		forwardedProto string
		useTLS         bool
		want           bool
	}{
		{name: "plain http", useTLS: false, forwardedProto: "", want: false},
		{name: "tls connection", useTLS: true, forwardedProto: "", want: true},
		{name: "forwarded https", useTLS: false, forwardedProto: "https", want: true},
		{name: "forwarded http", useTLS: false, forwardedProto: "http", want: false},
		{name: "tls plus forwarded https", useTLS: true, forwardedProto: "https", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tt.useTLS {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			got := isHTTPS(r)
			if got != tt.want {
				t.Errorf("isHTTPS(tls=%v, forwarded=%q) = %v, want %v",
					tt.useTLS, tt.forwardedProto, got, tt.want)
			}
		})
	}
}

func TestSessionCookieName_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want string
		tls  bool
	}{
		{name: "http returns plain cookie", tls: false, want: CookieNameHTTP},
		{name: "https returns secure cookie", tls: true, want: CookieNameSecure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got := SessionCookieName(r)
			if got != tt.want {
				t.Errorf("SessionCookieName(tls=%v) = %q, want %q", tt.tls, got, tt.want)
			}
		})
	}
}

func TestSetSessionCookie_sets_correct_attributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		token    string
		wantName string
		maxAge   int
		tls      bool
		wantSec  bool
	}{
		{name: "http session", tls: false, token: "tok123", maxAge: 3600, wantName: CookieNameHTTP, wantSec: false},
		{name: "https session", tls: true, token: "tok456", maxAge: 7200, wantName: CookieNameSecure, wantSec: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			SetSessionCookie(w, r, tt.token, tt.maxAge)

			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("SetSessionCookie: got %d cookies, want 1", len(cookies))
			}
			c := cookies[0]
			if c.Name != tt.wantName {
				t.Errorf("cookie name = %q, want %q", c.Name, tt.wantName)
			}
			if c.Value != tt.token {
				t.Errorf("cookie value = %q, want %q", c.Value, tt.token)
			}
			if c.MaxAge != tt.maxAge {
				t.Errorf("cookie MaxAge = %d, want %d", c.MaxAge, tt.maxAge)
			}
			if !c.HttpOnly {
				t.Error("cookie HttpOnly = false, want true")
			}
			if c.Secure != tt.wantSec {
				t.Errorf("cookie Secure = %v, want %v", c.Secure, tt.wantSec)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
			}
			if c.Path != "/" {
				t.Errorf("cookie Path = %q, want %q", c.Path, "/")
			}
		})
	}
}

func TestClearSessionCookie_sets_negative_max_age(t *testing.T) {
	t.Parallel()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ClearSessionCookie(w, r)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("ClearSessionCookie: got %d cookies, want 1", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("ClearSessionCookie MaxAge = %d, want -1", cookies[0].MaxAge)
	}
	if cookies[0].Value != "" {
		t.Errorf("ClearSessionCookie Value = %q, want empty", cookies[0].Value)
	}
}

func TestReadSessionCookie_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cookieName string
		value      string
		want       string
		tls        bool
	}{
		{name: "http cookie present", cookieName: CookieNameHTTP, value: "mytoken", tls: false, want: "mytoken"},
		{name: "https cookie present", cookieName: CookieNameSecure, value: "sectoken", tls: true, want: "sectoken"},
		{name: "no cookie", cookieName: "", value: "", tls: false, want: ""},
		{name: "wrong cookie name", cookieName: "other_cookie", value: "val", tls: false, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.cookieName != "" {
				r.AddCookie(&http.Cookie{Name: tt.cookieName, Value: tt.value})
			}
			got := ReadSessionCookie(r)
			if got != tt.want {
				t.Errorf("ReadSessionCookie(cookie=%q) = %q, want %q",
					tt.cookieName, got, tt.want)
			}
		})
	}
}

func TestHasRole_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		userRole api.Role
		required api.Role
		want     bool
	}{
		{name: "admin accessing admin path", userRole: "admin", required: "admin", want: true},
		{name: "admin accessing user path", userRole: "admin", required: "user", want: true},
		{name: "user accessing user path", userRole: "user", required: "user", want: true},
		{name: "user accessing admin path", userRole: "user", required: "admin", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			user := &api.User{Role: tt.userRole}
			got := HasRole(user, tt.required)
			if got != tt.want {
				t.Errorf("HasRole(role=%q, required=%q) = %v, want %v",
					tt.userRole, tt.required, got, tt.want)
			}
		})
	}
}

func TestValidateRedirectURI_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{name: "empty", uri: "", want: "/"},
		{name: "root", uri: "/", want: "/"},
		{name: "relative path", uri: "/dashboard", want: "/dashboard"},
		{name: "relative with query", uri: "/search?q=test", want: "/search?q=test"},
		{name: "absolute http", uri: "http://evil.com", want: "/"},
		{name: "absolute https", uri: "https://evil.com", want: "/"},
		{name: "protocol-relative", uri: "//evil.com", want: "/"},
		{name: "scheme in path", uri: "/foo://bar", want: "/"},
		{name: "no leading slash", uri: "evil.com", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateRedirectURI(tt.uri)
			if got != tt.want {
				t.Errorf("ValidateRedirectURI(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

// --- Authenticate integration tests ---

func TestAuthenticate_session_cookie_valid(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "sessuser",
		PasswordHash: "dummy",
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sess := &api.Session{
		TokenHash:    hash,
		UserID:       user.ID,
		AuthMethod:   "password",
		IPAddress:    "127.0.0.1",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/config", nil)
	r.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: plaintext})

	gotUser, gotHash, gotErr := a.Authenticate(r)
	if gotErr != nil {
		t.Fatalf("Authenticate() error = %v, want nil", gotErr)
	}
	if gotUser.ID != user.ID {
		t.Errorf("Authenticate() user ID = %d, want %d", gotUser.ID, user.ID)
	}
	if gotHash != hash {
		t.Errorf("Authenticate() session hash = %q, want %q", gotHash, hash)
	}
}

func TestAuthenticate_expired_session_falls_through(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "expuser",
		PasswordHash: "dummy",
		Role:         "user",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	sess := &api.Session{
		TokenHash:    hash,
		UserID:       user.ID,
		AuthMethod:   "password",
		IPAddress:    "127.0.0.1",
		CreatedAt:    old,
		LastActivity: old.Add(23 * time.Hour),
	}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: plaintext})

	_, _, gotErr := a.Authenticate(r)
	if gotErr == nil {
		t.Fatal("Authenticate() expected error for expired session, got nil")
	}
}

func TestAuthenticate_api_key_header(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "apiuser",
		PasswordHash: "dummy",
		Role:         "user",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/search", nil)
	r.Header.Set("X-API-Key", plaintext)

	gotUser, _, gotErr := a.Authenticate(r)
	if gotErr != nil {
		t.Fatalf("Authenticate(X-API-Key) error = %v, want nil", gotErr)
	}
	if gotUser.ID != user.ID {
		t.Errorf("Authenticate(X-API-Key) user ID = %d, want %d", gotUser.ID, user.ID)
	}
}

func TestAuthenticate_api_key_query_param(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "queryuser",
		PasswordHash: "dummy",
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/search?api_key="+plaintext, nil)

	gotUser, _, gotErr := a.Authenticate(r)
	if gotErr != nil {
		t.Fatalf("Authenticate(api_key query) error = %v, want nil", gotErr)
	}
	if gotUser.ID != user.ID {
		t.Errorf("Authenticate(api_key query) user ID = %d, want %d", gotUser.ID, user.ID)
	}
}

func TestAuthenticate_no_credentials(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	_, _, gotErr := a.Authenticate(r)
	if !errors.Is(gotErr, ErrUnauthenticated) {
		t.Errorf("Authenticate(no creds) error = %v, want ErrUnauthenticated", gotErr)
	}
}

func TestAuthenticate_invalid_api_key(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "sfx_invalid_key_value")

	_, _, gotErr := a.Authenticate(r)
	if !errors.Is(gotErr, ErrUnauthenticated) {
		t.Errorf("Authenticate(invalid key) error = %v, want ErrUnauthenticated", gotErr)
	}
}

func TestAuthenticate_disabled_user_session(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "disableduser",
		PasswordHash: "dummy",
		Role:         "user",
		Enabled:      false,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sess := &api.Session{
		TokenHash:    hash,
		UserID:       user.ID,
		AuthMethod:   "password",
		IPAddress:    "127.0.0.1",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: plaintext})

	_, _, gotErr := a.Authenticate(r)
	if !errors.Is(gotErr, ErrUnauthenticated) {
		t.Errorf("Authenticate(disabled user) error = %v, want ErrUnauthenticated", gotErr)
	}
}

func TestAuthenticate_disabled_user_api_key(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "disabledapi",
		PasswordHash: "dummy",
		Role:         "user",
		Enabled:      false,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", plaintext)

	_, _, gotErr := a.Authenticate(r)
	if !errors.Is(gotErr, ErrUnauthenticated) {
		t.Errorf("Authenticate(disabled user API key) error = %v, want ErrUnauthenticated", gotErr)
	}
}

// --- RequireAuth integration tests ---

func TestRequireAuth_unauthenticated_browser_redirects(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/config", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()

	_, _, ok := a.RequireAuth(w, r)
	if ok {
		t.Fatal("RequireAuth() returned ok=true for unauthenticated browser request")
	}
	if w.Code != http.StatusFound {
		t.Errorf("RequireAuth(browser) status = %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Error("RequireAuth(browser) missing Location header")
	}
}

func TestRequireAuth_unauthenticated_api_returns_401(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/config", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()

	_, _, ok := a.RequireAuth(w, r)
	if ok {
		t.Fatal("RequireAuth() returned ok=true for unauthenticated API request")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("RequireAuth(api) status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuth_authenticated_returns_user(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "authuser",
		PasswordHash: "dummy",
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	r, _ := http.NewRequest(http.MethodGet, "/api/search", nil)
	r.Header.Set("X-API-Key", plaintext)
	w := httptest.NewRecorder()

	gotUser, _, ok := a.RequireAuth(w, r)
	if !ok {
		t.Fatal("RequireAuth() returned ok=false for authenticated request")
	}
	if gotUser.ID != user.ID {
		t.Errorf("RequireAuth() user ID = %d, want %d", gotUser.ID, user.ID)
	}
}

func TestAuthenticate_session_not_found_falls_through(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	// Provide a cookie with a token that has no matching session in the DB.
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: "nonexistent-session-token"})

	_, _, gotErr := a.Authenticate(r)
	if !errors.Is(gotErr, ErrUnauthenticated) {
		t.Errorf("Authenticate(stale session cookie) error = %v, want ErrUnauthenticated", gotErr)
	}
}

func TestAuthenticate_stale_session_falls_through_to_api_key(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	user := &api.User{
		Username:     "fallthrough_user",
		PasswordHash: "dummy",
		Role:         "user",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatal(err)
	}

	a := &Authenticator{
		Store:       db,
		IdleTimeout: time.Hour,
		AbsTimeout:  24 * time.Hour,
	}

	// Stale session cookie + valid API key header.
	r, _ := http.NewRequest(http.MethodGet, "/api/search", nil)
	r.AddCookie(&http.Cookie{Name: CookieNameHTTP, Value: "stale-session-token"})
	r.Header.Set("X-API-Key", plaintext)

	gotUser, _, gotErr := a.Authenticate(r)
	if gotErr != nil {
		t.Fatalf("Authenticate(stale session + valid API key) error = %v, want nil", gotErr)
	}
	if gotUser.ID != user.ID {
		t.Errorf("Authenticate(stale session + valid API key) user ID = %d, want %d", gotUser.ID, user.ID)
	}
}
