package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/auth/ratelimit"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/store"
)

// --- Test helpers ---

// testAuthServer creates a minimal Server backed by a real SQLite database
// for auth handler testing.
func testAuthServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })

	rl := ratelimit.NewRateLimiter(context.Background(), ratelimit.DefaultConfig())
	t.Cleanup(func() { rl.Stop() })

	s := &Server{
		authDeps: authDeps{
			authStore:   db,
			adminDB:     db,
			secDB:       db,
			oidcDB:      db,
			rateLimiter: rl,
			authenticator: &authhandlers.Authenticator{
				Store:       db,
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
		Store:       db,
		AdminDB:     db,
		SecDB:       db,
		OidcDB:      db,
		RateLimiter: rl,
		Ceremonies:  s.ceremonies,
		Config:      func() authhandlers.AuthConfig { return s.state().cfg },
		Configured:  func() bool { return s.configured.Load() },
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		Configured: func() bool { return s.configured.Load() },
		ConfigPath: func() string { return cfgFilePath },
	})
	return s, db
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
func createTestUser(t *testing.T, db *store.DB, username, password string) *api.User {
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
func createTestSession(t *testing.T, db *store.DB, userID int64) string {
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

// =============================================================================
// Task 21.1: Login flow tests
// =============================================================================

func TestLogin_ValidCredentials(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "alice", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("alice", "correct-horse-battery-staple"))
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify session cookie is set.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == authhandlers.CookieNameHTTP || c.Name == authhandlers.CookieNameSecure {
			found = true
			if !c.HttpOnly {
				t.Error("session cookie missing HttpOnly flag")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
		}
	}
	if !found {
		t.Error("no session cookie set in response")
	}

	// Verify response contains user info.
	var resp map[string]any
	decodeJSON(t, rec, &resp)
	user, ok := resp["user"].(map[string]any)
	if !ok {
		t.Fatal("response missing 'user' object")
	}
	if user["username"] != "alice" {
		t.Errorf("username = %v, want alice", user["username"])
	}
}

func TestLogin_InvalidPassword(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "bob", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("bob", "wrong-password-here-now"))
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid credentials" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid credentials")
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("nonexistent", "some-password-here"))
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Same error as invalid password (no username enumeration).
	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid credentials" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid credentials")
	}
}

func TestLogin_RateLimited(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "dave", "correct-horse-battery-staple")

	// Send 10 failed login attempts (IP rate limit is 10/15min).
	for i := range 10 {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			loginBody("dave", "wrong-password-attempt"))
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		s.authH.HandleLogin(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	// 11th attempt should be rate limited.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("dave", "wrong-password-attempt"))
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("missing Retry-After header on 429 response")
	}
}

// =============================================================================
// Task 21.4: Setup flow tests
// =============================================================================

func TestSetup_Required(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/setup", http.NoBody)
	rec := httptest.NewRecorder()
	s.authH.HandleSetupStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["setup_required"] != true {
		t.Errorf("setup_required = %v, want true", resp["setup_required"])
	}
	if resp["config_valid"] != false {
		t.Errorf("config_valid = %v, want false", resp["config_valid"])
	}
}

func TestSetup_Create(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)

	body := `{"username":"admin","password":"super-secure-password-here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
		strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify user was created with admin role.
	user, err := db.GetUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if user == nil {
		t.Fatal("user not created")
	}
	if user.Role != "admin" {
		t.Errorf("role = %q, want %q", user.Role, "admin")
	}

	// Verify session cookie was set.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == authhandlers.CookieNameHTTP || c.Name == authhandlers.CookieNameSecure {
			found = true
		}
	}
	if !found {
		t.Error("no session cookie set after setup")
	}
}

func TestSetup_AlreadyDone(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "existing", "correct-horse-battery-staple")

	body := `{"username":"admin2","password":"another-secure-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
		strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestSetup_ConfigValid(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "admin", "correct-horse-battery-staple")

	// Mark server as configured.
	s.configured.Store(true)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/setup", http.NoBody)
	rec := httptest.NewRecorder()
	s.authH.HandleSetupStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["setup_required"] != false {
		t.Errorf("setup_required = %v, want false", resp["setup_required"])
	}
	if resp["config_valid"] != true {
		t.Errorf("config_valid = %v, want true", resp["config_valid"])
	}
}

// =============================================================================
// Task 21.5: Logout tests
// =============================================================================

func TestLogout_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "eve", "correct-horse-battery-staple")
	token := createTestSession(t, db, user.ID)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
	req.AddCookie(&http.Cookie{
		Name:  authhandlers.CookieNameHTTP,
		Value: token,
	})
	rec := httptest.NewRecorder()
	s.authH.HandleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify session was deleted from DB.
	hash := authlib.SessionHash(token)
	sess, err := db.GetSessionByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if sess != nil {
		t.Error("session still exists after logout")
	}

	// Verify cookie was cleared (MaxAge < 0).
	for _, c := range rec.Result().Cookies() {
		if c.Name == authhandlers.CookieNameHTTP || c.Name == authhandlers.CookieNameSecure {
			if c.MaxAge >= 0 {
				t.Errorf("cookie MaxAge = %d, want < 0 (cleared)", c.MaxAge)
			}
		}
	}
}

func TestLogout_NoCookie(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
	rec := httptest.NewRecorder()
	s.authH.HandleLogout(rec, req)

	// Should succeed (idempotent).
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// =============================================================================
// Task 21.8: Security management endpoint tests
// =============================================================================

func TestChangePassword_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "frank", "old-password-is-here-now")
	token := createTestSession(t, db, user.ID)

	body := `{"current_password":"old-password-is-here-now","new_password":"new-password-is-here-now"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/password",
		strings.NewReader(body))
	// Inject user into context (simulates auth middleware).
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	// Add session cookie so the handler can identify the current session.
	req.AddCookie(&http.Cookie{
		Name:  authhandlers.CookieNameHTTP,
		Value: token,
	})
	rec := httptest.NewRecorder()
	s.authH.HandleChangePassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify new password works.
	updated, err := db.GetUserByUsername(context.Background(), "frank")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := authlib.VerifyPassword("new-password-is-here-now", updated.PasswordHash)
	if err != nil || !ok {
		t.Error("new password verification failed")
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "grace", "correct-horse-battery-staple")

	body := `{"current_password":"wrong-current-password","new_password":"new-password-is-here-now"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/password",
		strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleChangePassword(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid current password" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid current password")
	}
}

func TestListPasskeys_Empty(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "heidi", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleListPasskeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var passkeys []any
	decodeJSON(t, rec, &passkeys)
	if len(passkeys) != 0 {
		t.Errorf("passkeys count = %d, want 0", len(passkeys))
	}
}

func TestListAPIKeys_Empty(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "ivan", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/apikeys", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleListAPIKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var keys []any
	decodeJSON(t, rec, &keys)
	if len(keys) != 0 {
		t.Errorf("api keys count = %d, want 0", len(keys))
	}
}

func TestGenerateAPIKey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "judy", "correct-horse-battery-staple")

	body := `{"label":"test key","password":"correct-horse-battery-staple"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/apikeys",
		strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleGenerateAPIKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)

	key, ok := resp["key"].(string)
	if !ok || key == "" {
		t.Fatal("response missing 'key' field")
	}
	if !strings.HasPrefix(key, "sfx_") {
		t.Errorf("key prefix = %q, want sfx_", key[:4])
	}

	// Verify the key is stored as a hash in the DB.
	h := sha256.Sum256([]byte(key))
	expectedHash := hex.EncodeToString(h[:])
	apiKey, err := db.GetAPIKeyByHash(context.Background(), expectedHash)
	if err != nil {
		t.Fatal(err)
	}
	if apiKey == nil {
		t.Error("API key not found in DB by hash")
	}
}

func TestRevokeAPIKey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "karl", "correct-horse-battery-staple")

	// Generate a key first.
	plaintext, hash, prefix, suffix, err := authlib.GenerateAPIKey("sfx_")
	if err != nil {
		t.Fatal(err)
	}
	_ = plaintext
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "to-revoke",
		CreatedAt: time.Now(),
	}
	if err := db.CreateAPIKey(context.Background(), apiKey); err != nil {
		t.Fatal(err)
	}

	// List keys to get the ID.
	keys, err := db.ListAPIKeysByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("no API keys found")
	}
	keyID := keys[0].ID

	// Revoke the key.
	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/apikeys/"+strconv.FormatInt(keyID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRevokeAPIKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify key is gone.
	found, err := db.GetAPIKeyByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if found != nil {
		t.Error("API key still exists after revocation")
	}
}

// =============================================================================
// Task 21.7: CLI command underlying function tests
// =============================================================================

// These test the underlying auth operations that the CLI commands use,
// since the CLI commands themselves call os.Exit and can't be tested directly.

func TestResetPassword_UpdatesHash(t *testing.T) {
	t.Parallel()
	_, db := testAuthServer(t)
	user := createTestUser(t, db, "mallory", "old-password-for-reset")

	// Hash a new password and update the user (same logic as CLI).
	newPassword := "new-password-for-reset"
	hash, err := authlib.HashPassword(newPassword)
	if err != nil {
		t.Fatal(err)
	}
	user.PasswordHash = hash
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	// Invalidate all sessions.
	if err := db.DeleteUserSessions(context.Background(), user.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Verify new password works.
	updated, err := db.GetUserByUsername(context.Background(), "mallory")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := authlib.VerifyPassword(newPassword, updated.PasswordHash)
	if err != nil || !ok {
		t.Error("new password verification failed after reset")
	}

	// Verify old password no longer works.
	ok, err = authlib.VerifyPassword("old-password-for-reset", updated.PasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("old password still works after reset")
	}
}

func TestGenerateAPIKey_StoresHash(t *testing.T) {
	t.Parallel()
	_, db := testAuthServer(t)
	user := createTestUser(t, db, "nancy", "correct-horse-battery-staple")

	// Generate an API key (same logic as CLI).
	plaintext, hash, prefix, suffix, err := authlib.GenerateAPIKey("sfx_")
	if err != nil {
		t.Fatal(err)
	}

	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test-cli-key",
		CreatedAt: time.Now(),
	}
	if err := db.CreateAPIKey(context.Background(), apiKey); err != nil {
		t.Fatal(err)
	}

	// Verify the stored hash matches SHA-256 of the plaintext key.
	h := sha256.Sum256([]byte(plaintext))
	expectedHash := hex.EncodeToString(h[:])
	if hash != expectedHash {
		t.Errorf("stored hash = %q, want SHA-256(%q) = %q", hash, plaintext, expectedHash)
	}

	// Verify the key can be looked up by hash.
	found, err := db.GetAPIKeyByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Error("API key not found in DB by hash")
	}
	if found != nil && found.Label != "test-cli-key" {
		t.Errorf("label = %q, want %q", found.Label, "test-cli-key")
	}
}

// =============================================================================
// Admin user management tests (TEST-AUTH-01)
// =============================================================================

func TestListUsers_AdminOnly(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "list-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	// Second user exists so list has more than one entry.
	regular := createTestUser(t, db, "list-user", "correct-horse-battery-staple")
	regular.Role = "user"
	db.UpdateUser(context.Background(), regular)

	// Admin can list users. Non-admin rejection is enforced by the
	// requireRole(admin) middleware (see routes.go) and verified by the
	// integration test suite; this handler does not re-check the role.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/users", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListUsers(admin) status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var users []map[string]any
	decodeJSON(t, rec, &users)
	if len(users) != 2 {
		t.Errorf("handleListUsers(admin) returned %d users, want 2", len(users))
	}
}

func TestCreateUser_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "create-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	body := `{"username":"newuser","password":"new-user-password-here","role":"user","email":"new@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCreateUser status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["username"] != "newuser" {
		t.Errorf("username = %v, want newuser", resp["username"])
	}
	if resp["role"] != "user" {
		t.Errorf("role = %v, want user", resp["role"])
	}

	// Verify user exists in DB.
	u, err := db.GetUserByUsername(context.Background(), "newuser")
	if err != nil || u == nil {
		t.Fatal("created user not found in DB")
	}
	if u.Email != "new@example.com" {
		t.Errorf("email = %q, want %q", u.Email, "new@example.com")
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "role-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	body := `{"username":"badrole","password":"some-password-here-now","role":"superadmin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(invalid role) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateUser_EmptyUsername(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "empty-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	body := `{"username":"","password":"some-password-here-now"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(empty username) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDeleteUser_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "del-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	victim := createTestUser(t, db, "del-victim", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/users/"+strconv.FormatInt(victim.ID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleDeleteUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleDeleteUser status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify user is gone.
	u, err := db.GetUserByUsername(context.Background(), "del-victim")
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		t.Error("deleted user still exists in DB")
	}
}

func TestDeleteUser_CannotDeleteSelf(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "self-del", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/users/"+strconv.FormatInt(admin.ID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleDeleteUser(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("handleDeleteUser(self) status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestDeleteUser_InvalidID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "inv-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/users/abc", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeleteUser(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Passkey CRUD tests (TEST-AUTH-03)
// =============================================================================

func TestRenamePasskey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-pk", "correct-horse-battery-staple")

	passkey := &api.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("test-cred-id"),
		PublicKey:    []byte("test-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "Old Name",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(context.Background(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.GetPasskeysByUserID(context.Background(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	body := `{"name":"New Name"}`
	req := httptest.NewRequest(http.MethodPut,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRenamePasskey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleRenamePasskey status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRenamePasskey_EmptyName(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-empty", "correct-horse-battery-staple")

	body := `{"name":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/passkeys/1", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRenamePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRenamePasskey(empty name) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRenamePasskey_InvalidID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-inv", "correct-horse-battery-staple")

	body := `{"name":"New"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/passkeys/abc", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRenamePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRenamePasskey(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDeletePasskey_InvalidID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "delpk-inv", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/passkeys/xyz", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeletePasskey(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDeletePasskey_MissingID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "delpk-miss", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/passkeys/", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeletePasskey(missing id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Pure function tests (TEST-AUTH-04)
// =============================================================================

func TestClientIP_table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "ipv4 with port", remoteAddr: "192.168.1.1:12345", want: "192.168.1.1"},
		{name: "ipv6 with port", remoteAddr: "[::1]:8080", want: "::1"},
		{name: "no port", remoteAddr: "192.168.1.1", want: "192.168.1.1"},
		{name: "empty", remoteAddr: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			got := authhandlers.ClientIP(req)
			if got != tt.want {
				t.Errorf("ClientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

func TestBase64URLEncode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		want  string
		input []byte
	}{
		{name: "empty", input: []byte{}, want: ""},
		{name: "nil", input: nil, want: ""},
		{name: "hello", input: []byte("hello"), want: "aGVsbG8"},
		{name: "binary", input: []byte{0xff, 0xfe, 0xfd}, want: "__79"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := authhandlers.Base64URLEncode(tt.input)
			if got != tt.want {
				t.Errorf("Base64URLEncode(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCleanupCeremonies_removes_expired(t *testing.T) {
	t.Parallel()

	cs := authhandlers.NewCeremonyStore()

	expiredWAToken := "expired-wa-cleanup"
	freshWAToken := "fresh-wa-cleanup"

	cs.WebAuthn.Store(expiredWAToken, &authhandlers.WebAuthnSession{
		CreatedAt: time.Now().Add(-10 * time.Minute),
	})
	cs.WebAuthn.Store(freshWAToken, &authhandlers.WebAuthnSession{
		CreatedAt: time.Now(),
	})

	cs.Cleanup()

	_, expiredWAExists := cs.WebAuthn.LoadAndDelete(expiredWAToken)
	_, freshWAExists := cs.WebAuthn.LoadAndDelete(freshWAToken)

	if expiredWAExists {
		t.Error("cleanup() did not remove expired WebAuthn session")
	}
	if !freshWAExists {
		t.Error("cleanup() removed fresh WebAuthn session")
	}
}

func TestConsumeWebAuthnSession_expired(t *testing.T) {
	t.Parallel()

	cs := authhandlers.NewCeremonyStore()
	token := "consume-expired-test"
	cs.WebAuthn.Store(token, &authhandlers.WebAuthnSession{
		CreatedAt: time.Now().Add(-10 * time.Minute),
	})

	result := cs.ConsumeWebAuthnSession(token)
	if result != nil {
		t.Error("consumeWebAuthnSession(expired) should return nil")
	}

	_, exists := cs.WebAuthn.Load(token)
	if exists {
		t.Error("expired session not removed from map")
	}
}

func TestConsumeWebAuthnSession_missing(t *testing.T) {
	t.Parallel()
	cs := authhandlers.NewCeremonyStore()
	result := cs.ConsumeWebAuthnSession("nonexistent-token")
	if result != nil {
		t.Error("consumeWebAuthnSession(missing) should return nil")
	}
}

// =============================================================================
// TOTP enable/confirm/disable tests (TEST-AUTH-05, TEST-AUTH-06, TEST-AUTH-07)
// =============================================================================

// =============================================================================
// Edge case tests (TEST-AUTH-08)
// =============================================================================

func TestLogin_DisabledUser(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "disabled", "correct-horse-battery-staple")
	user.Enabled = false
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("disabled", "correct-horse-battery-staple"))
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleLogin(disabled user) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid credentials" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid credentials")
	}
}

func TestLogin_InvalidJSON(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleLogin(invalid json) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetup_ShortPassword(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	body := `{"username":"admin","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleSetupCreate(short password) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetup_EmptyUsername(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	body := `{"username":"","password":"super-secure-password-here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleSetupCreate(empty username) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "short-pw", "correct-horse-battery-staple")

	body := `{"current_password":"correct-horse-battery-staple","new_password":"ab"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/password", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleChangePassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleChangePassword(short) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Delete passkey tests (T-U10C2-03)
// =============================================================================

func TestDeletePasskey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "delpk-ok", "correct-horse-battery-staple")

	passkey := &api.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("del-cred-id"),
		PublicKey:    []byte("del-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "To Delete",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(context.Background(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.GetPasskeysByUserID(context.Background(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleDeletePasskey status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify passkey is gone.
	remaining, err := db.GetPasskeysByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("passkey count = %d, want 0", len(remaining))
	}
}

func TestDeletePasskey_LastMethodGuard(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)

	// Create a user with no password (OIDC-only style) and one passkey.
	now := time.Now()
	user := &api.User{
		Username:  "delpk-lastmethod",
		Role:      "admin",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	passkey := &api.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("last-cred-id"),
		PublicKey:    []byte("last-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "Only Passkey",
		CreatedAt:    now,
	}
	if err := db.CreatePasskey(context.Background(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.GetPasskeysByUserID(context.Background(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("handleDeletePasskey(last method) status = %d, want %d", rec.Code, http.StatusConflict)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "cannot remove last authentication method" {
		t.Errorf("error = %q, want %q", resp["error"], "cannot remove last authentication method")
	}
}

// =============================================================================
// List passkeys/API keys with data tests (T-U10C2-04)
// =============================================================================

func TestListPasskeys_WithData(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "listpk-data", "correct-horse-battery-staple")

	for i := range 2 {
		pk := &api.PasskeyCredential{
			UserID:       user.ID,
			CredentialID: []byte("cred-" + strconv.Itoa(i)),
			PublicKey:    []byte("pub-" + strconv.Itoa(i)),
			AAGUID:       make([]byte, 16),
			Name:         "Key " + strconv.Itoa(i),
			CreatedAt:    time.Now(),
		}
		if err := db.CreatePasskey(context.Background(), pk); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleListPasskeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListPasskeys status = %d, want %d", rec.Code, http.StatusOK)
	}

	var passkeys []map[string]any
	decodeJSON(t, rec, &passkeys)
	if len(passkeys) != 2 {
		t.Fatalf("passkey count = %d, want 2", len(passkeys))
	}
	if passkeys[0]["name"] == nil || passkeys[0]["name"] == "" {
		t.Error("passkey missing 'name' field")
	}
}

func TestListAPIKeys_WithData(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "listkeys-data", "correct-horse-battery-staple")

	for i := range 2 {
		_, hash, prefix, suffix, err := authlib.GenerateAPIKey("sfx_")
		if err != nil {
			t.Fatal(err)
		}
		key := &api.Key{
			UserID:    user.ID,
			KeyHash:   hash,
			KeyPrefix: prefix,
			KeySuffix: suffix,
			Label:     "key-" + strconv.Itoa(i),
			CreatedAt: time.Now(),
		}
		if err := db.CreateAPIKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/apikeys", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleListAPIKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListAPIKeys status = %d, want %d", rec.Code, http.StatusOK)
	}

	var keys []map[string]any
	decodeJSON(t, rec, &keys)
	if len(keys) != 2 {
		t.Fatalf("api key count = %d, want 2", len(keys))
	}
	if keys[0]["label"] == nil || keys[0]["label"] == "" {
		t.Error("api key missing 'label' field")
	}
	if keys[0]["key_prefix"] == nil {
		t.Error("api key missing 'key_prefix' field")
	}
}

// =============================================================================
// API key edge case tests (T-U10C2-05)
// =============================================================================

func TestGenerateAPIKey_LabelTooLong(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "genkey-long", "correct-horse-battery-staple")

	longLabel := strings.Repeat("x", 129) // exceeds maxAPIKeyLabelLen=128
	body := `{"label":"` + longLabel + `","password":"correct-horse-battery-staple"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/apikeys", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleGenerateAPIKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleGenerateAPIKey(long label) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "label too long" {
		t.Errorf("error = %q, want %q", resp["error"], "label too long")
	}
}

func TestRevokeAPIKey_InvalidID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "revoke-inv", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/apikeys/abc", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRevokeAPIKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRevokeAPIKey(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRevokeAPIKey_MissingID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "revoke-miss", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/apikeys/", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRevokeAPIKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRevokeAPIKey(missing id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Misc edge case tests (T-U10C2-06)
// =============================================================================

func TestSetup_UsernameTooLong(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	longName := strings.Repeat("a", 65) // exceeds maxUsernameLen=64
	body := `{"username":"` + longName + `","password":"super-secure-password-here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleSetupCreate(long username) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "username too long" {
		t.Errorf("error = %q, want %q", resp["error"], "username too long")
	}
}

func TestAuthMe_WithPasskeys(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "me-passkeys", "correct-horse-battery-staple")

	pk := &api.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("me-cred-id"),
		PublicKey:    []byte("me-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "My Passkey",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(context.Background(), pk); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleAuthMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleAuthMe status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["has_passkeys"] != true {
		t.Errorf("has_passkeys = %v, want true", resp["has_passkeys"])
	}
}

func TestRenamePasskey_NameTooLong(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-long", "correct-horse-battery-staple")

	longName := strings.Repeat("x", 129) // exceeds maxPasskeyNameLen=128
	body := `{"name":"` + longName + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/passkeys/1", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRenamePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRenamePasskey(long name) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "name too long" {
		t.Errorf("error = %q, want %q", resp["error"], "name too long")
	}
}

func TestCreateUser_UsernameTooLong(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "create-long-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	longName := strings.Repeat("u", 65) // exceeds maxUsernameLen=64
	body := `{"username":"` + longName + `","password":"some-password-here-now"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(long username) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
