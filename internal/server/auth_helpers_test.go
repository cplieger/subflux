package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/auth/v5/ratelimit"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/confighandlers"
)

// --- Test helpers ---

// testAuthServer creates a minimal Server backed by a real bbolt database
// for auth handler testing.
func testAuthServer(t *testing.T) (*Server, *authstore.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.bolt")
	db, err := boltstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })

	authDB := authstore.New(db.Bolt())
	if err := authDB.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authDB.Close() })

	// Not t.Context(): the limiter's sweeper is torn down by the rl.Shutdown()
	// cleanup below, so its context must outlive the test body.
	rlCtx, rlCancel := context.WithCancel(context.Background())
	rl := ratelimit.New(rlCtx, ratelimit.DefaultConfig())
	t.Cleanup(func() {
		rlCancel()
		if err := rl.Shutdown(context.Background()); err != nil {
			t.Errorf("rate limiter Shutdown: %v", err)
		}
	})

	// The production authenticator shape (library authenticator + subflux's
	// cookie and 401 policies), minus the live-config timeout source: tests
	// pin static 24h/7d timeouts.
	authn, err := auth.New(authDB,
		auth.WithCookie(authhandlers.SessionCookie),
		auth.WithUnauthorizedResponse(authhandlers.UnauthorizedResponse),
		auth.WithIdleTimeout(24*time.Hour),
		auth.WithAbsTimeout(7*24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		authStore:     authDB,
		authenticator: authn,
		ceremonies:    authhandlers.NewCeremonyStore(),
		activity:      activity.New(50),
		alerts:        activity.NewAlertLog(100),
	}
	// check_breached_passwords: OFF, stated rather than inherited. The real
	// config DEFAULTS IT ON, so these tests have never run the production
	// default; turning it on here would send every password in the suite to
	// HIBP over the network, so the fixture says which value it wants.
	s.live.Store(&liveState{cfg: testConfig(t, "auth:\n  check_breached_passwords: false")})
	s.authH = &authhandlers.Handler{
		Store:       authDB,
		AdminDB:     authDB,
		SecDB:       authDB,
		OidcDB:      authDB,
		RateLimiter: rl,
		Ceremonies:  s.ceremonies,
		Config:      func() authhandlers.AuthConfig { return s.state().cfg },
		Configured:  func() bool { return s.configured.Load() },
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		Configured: func() bool { return s.configured.Load() },
		ConfigPath: func() string { return cfgFilePath },
	})
	return s, authDB
}

// createTestUser creates a user in the DB with the given username and password.
func createTestUser(t *testing.T, db *authstore.Store, username, password string) *auth.User {
	t.Helper()
	hash := auth.HashPassword(password)
	now := time.Now()
	user := &auth.User{
		Username:     username,
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	return user
}

// createTestSession creates a session for the given user and returns the
// plaintext token.
func createTestSession(t *testing.T, db *authstore.Store, userID int64) string {
	t.Helper()
	token, hash := auth.GenerateSessionToken()
	now := time.Now()
	sess := &auth.Session{
		TokenHash:    hash,
		UserID:       userID,
		AuthMethod:   auth.MethodPassword,
		IPAddress:    "127.0.0.1",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := db.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	return token
}

// decodeJSON decodes a JSON response body into the given target.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// loginBody returns a JSON login request body.
func loginBody(username, password string) *strings.Reader {
	return strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
}
