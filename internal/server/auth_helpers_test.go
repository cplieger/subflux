package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/auth/ratelimit"
	"github.com/cplieger/subflux/internal/api"
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

	authDB := authstore.New(db.BoltDB())
	if err := authDB.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authDB.Close() })

	rl := ratelimit.NewRateLimiter(context.Background(), ratelimit.DefaultConfig())
	t.Cleanup(func() { rl.Stop() })

	s := &Server{
		authDeps: authDeps{
			authStore:   authDB,
			adminDB:     authDB,
			secDB:       authDB,
			oidcDB:      authDB,
			rateLimiter: rl,
			authenticator: &authhandlers.Authenticator{
				Store:       authDB,
				IdleTimeout: 24 * time.Hour,
				AbsTimeout:  7 * 24 * time.Hour,
			},
			ceremonies: authhandlers.NewCeremonyStore(),
		},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &authTestConfig{}})
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

// authTestConfig implements api.ConfigProvider for auth tests.
type authTestConfig struct {
	qhMockConfig

	breachedCheck bool
}

func (c *authTestConfig) CheckBreachedPasswords() bool { return c.breachedCheck }
func (c *authTestConfig) OIDCEnabled() bool            { return false }
func (c *authTestConfig) BasicAuthEnabled() bool       { return true }
func (c *authTestConfig) SessionIdleTimeout() time.Duration {
	return 24 * time.Hour
}

func (c *authTestConfig) SessionAbsoluteTimeout() time.Duration {
	return 7 * 24 * time.Hour
}
func (c *authTestConfig) WebAuthnRPID() string { return "" }

// createTestUser creates a user in the DB with the given username and password.
func createTestUser(t *testing.T, db *authstore.Store, username, password string) *api.User {
	t.Helper()
	hash, err := authlib.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	user := &api.User{
		Username:     username,
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	return user
}

// createTestSession creates a session for the given user and returns the
// plaintext token.
func createTestSession(t *testing.T, db *authstore.Store, userID int64) string {
	t.Helper()
	token, hash, err := authlib.GenerateSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sess := &api.Session{
		TokenHash:    hash,
		UserID:       userID,
		AuthMethod:   api.MethodPassword,
		IPAddress:    "127.0.0.1",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := db.CreateSession(context.Background(), sess); err != nil {
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
