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

	"subflux/internal/api"
	"subflux/internal/auth"
	"subflux/internal/ratelimit"
	"subflux/internal/server/authhandlers"
	"subflux/internal/server/confighandlers"
	"subflux/internal/store"

	"subflux/internal/server/activity"

	"github.com/pquerna/otp/totp"
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
			authenticator: &auth.Authenticator{
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
func (c *authTestConfig) TOTPEncryptionKey() ([]byte, error) {
	return []byte("0123456789abcdef0123456789abcdef"), nil // 32 bytes for AES-256
}
func (c *authTestConfig) WebAuthnRPID() string { return "" }

// createTestUser creates a user in the DB with the given username and password.
func createTestUser(t *testing.T, db *store.DB, username, password string) *api.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
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
	token, hash, err := auth.GenerateSessionToken()
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
	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify session cookie is set.
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == auth.CookieNameHTTP || c.Name == auth.CookieNameSecure {
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
	s.handleLogin(rec, req)

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
	s.handleLogin(rec, req)

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

func TestLogin_TOTPRequired(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "carol", "correct-horse-battery-staple")

	// Enable TOTP on the user.
	user.TOTPEnabled = true
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("carol", "correct-horse-battery-staple"))
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["totp_required"] != true {
		t.Errorf("totp_required = %v, want true", resp["totp_required"])
	}
	if resp["totp_token"] == nil || resp["totp_token"] == "" {
		t.Error("missing totp_token in response")
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
		s.handleLogin(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	// 11th attempt should be rate limited.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("dave", "wrong-password-attempt"))
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

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
	s.handleSetupStatus(rec, req)

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
	s.handleSetupCreate(rec, req)

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
		if c.Name == auth.CookieNameHTTP || c.Name == auth.CookieNameSecure {
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
	s.handleSetupCreate(rec, req)

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
	s.handleSetupStatus(rec, req)

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
		Name:  auth.CookieNameHTTP,
		Value: token,
	})
	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify session was deleted from DB.
	hash := auth.SessionHash(token)
	sess, err := db.GetSessionByHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if sess != nil {
		t.Error("session still exists after logout")
	}

	// Verify cookie was cleared (MaxAge < 0).
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieNameHTTP || c.Name == auth.CookieNameSecure {
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
	s.handleLogout(rec, req)

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
		Name:  auth.CookieNameHTTP,
		Value: token,
	})
	rec := httptest.NewRecorder()
	s.handleChangePassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify new password works.
	updated, err := db.GetUserByUsername(context.Background(), "frank")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.VerifyPassword("new-password-is-here-now", updated.PasswordHash)
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
	s.handleChangePassword(rec, req)

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
	s.handleListPasskeys(rec, req)

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
	s.handleListAPIKeys(rec, req)

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

	body := `{"label":"test key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/apikeys",
		strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleGenerateAPIKey(rec, req)

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
	plaintext, hash, prefix, suffix, err := auth.GenerateAPIKey()
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
	s.handleRevokeAPIKey(rec, req)

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
	hash, err := auth.HashPassword(newPassword)
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
	ok, err := auth.VerifyPassword(newPassword, updated.PasswordHash)
	if err != nil || !ok {
		t.Error("new password verification failed after reset")
	}

	// Verify old password no longer works.
	ok, err = auth.VerifyPassword("old-password-for-reset", updated.PasswordHash)
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
	plaintext, hash, prefix, suffix, err := auth.GenerateAPIKey()
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
	s.handleListUsers(rec, req)

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
	s.handleCreateUser(rec, req)

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
	s.handleCreateUser(rec, req)

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
	s.handleCreateUser(rec, req)

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
	s.handleDeleteUser(rec, req)

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
	s.handleDeleteUser(rec, req)

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
	s.handleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeleteUser(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Reauth tests (TEST-AUTH-02)
// =============================================================================

func TestReauth_Password_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "reauth-pw", "correct-horse-battery-staple")
	token := createTestSession(t, db, user.ID)

	body := `{"method":"password","password":"correct-horse-battery-staple"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	req.AddCookie(&http.Cookie{Name: auth.CookieNameHTTP, Value: token})
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleReauth(password) status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestReauth_Password_Wrong(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "reauth-wrong", "correct-horse-battery-staple")

	body := `{"method":"password","password":"wrong-password-here-now"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleReauth(wrong password) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestReauth_UnsupportedMethod(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "reauth-bad", "correct-horse-battery-staple")

	body := `{"method":"magic"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleReauth(unsupported) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestReauth_NoPasswordSet(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	now := time.Now()
	user := &api.User{
		Username:  "oidc-only",
		Role:      "admin",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	body := `{"method":"password","password":"anything"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleReauth(no password) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "no password set" {
		t.Errorf("error = %q, want %q", resp["error"], "no password set")
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
	s.handleRenamePasskey(rec, req)

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
	s.handleRenamePasskey(rec, req)

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
	s.handleRenamePasskey(rec, req)

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
	s.handleDeletePasskey(rec, req)

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
	s.handleDeletePasskey(rec, req)

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

	expiredToken := "expired-token-cleanup"
	freshToken := "fresh-token-cleanup"

	cs.TOTP.Store(expiredToken, &authhandlers.PendingTOTP{
		UserID:    1,
		IP:        "127.0.0.1",
		CreatedAt: time.Now().Add(-10 * time.Minute),
	})
	cs.TOTP.Store(freshToken, &authhandlers.PendingTOTP{
		UserID:    2,
		IP:        "127.0.0.1",
		CreatedAt: time.Now(),
	})

	expiredWAToken := "expired-wa-cleanup"
	freshWAToken := "fresh-wa-cleanup"

	cs.WebAuthn.Store(expiredWAToken, &authhandlers.WebAuthnSession{
		CreatedAt: time.Now().Add(-10 * time.Minute),
	})
	cs.WebAuthn.Store(freshWAToken, &authhandlers.WebAuthnSession{
		CreatedAt: time.Now(),
	})

	cs.Cleanup()

	_, expiredExists := cs.TOTP.LoadAndDelete(expiredToken)
	_, freshExists := cs.TOTP.LoadAndDelete(freshToken)

	if expiredExists {
		t.Error("cleanup() did not remove expired TOTP entry")
	}
	if !freshExists {
		t.Error("cleanup() removed fresh TOTP entry")
	}

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

func TestTOTPEnable_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-enable", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp/enable", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPEnable(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleTOTPEnable status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["secret"] == nil || resp["secret"] == "" {
		t.Error("response missing 'secret' field")
	}
	if resp["uri"] == nil || resp["uri"] == "" {
		t.Error("response missing 'uri' field")
	}
}

func TestTOTPConfirm_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-confirm", "correct-horse-battery-staple")

	secret, _, err := auth.GenerateTOTPSecret(user.Username, "Subflux")
	if err != nil {
		t.Fatal(err)
	}

	code := generateTOTPCode(t, secret)

	body := `{"secret":"` + secret + `","code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp/confirm", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPConfirm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleTOTPConfirm status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	codes, ok := resp["recovery_codes"].([]any)
	if !ok || len(codes) == 0 {
		t.Error("response missing recovery_codes")
	}

	// Verify TOTP is enabled on the user.
	updated, err := db.GetUserByID(context.Background(), user.ID)
	if err != nil || updated == nil {
		t.Fatal("user not found")
	}
	if !updated.TOTPEnabled {
		t.Error("TOTP not enabled on user after confirm")
	}

	// Verify encrypted secret is stored.
	encSecret, err := db.GetTOTPSecret(context.Background(), user.ID)
	if err != nil || len(encSecret) == 0 {
		t.Error("TOTP secret not stored in DB")
	}
}

func TestTOTPConfirm_InvalidCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-bad-code", "correct-horse-battery-staple")

	secret, _, err := auth.GenerateTOTPSecret(user.Username, "Subflux")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"secret":"` + secret + `","code":"000000"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp/confirm", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPConfirm(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleTOTPConfirm(invalid code) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestTOTPDisable_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-disable", "correct-horse-battery-staple")

	secret, _, err := auth.GenerateTOTPSecret(user.Username, "Subflux")
	if err != nil {
		t.Fatal(err)
	}
	encKey := []byte("0123456789abcdef0123456789abcdef")
	encSecret, err := auth.Encrypt([]byte(secret), encKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.SetTOTPSecret(ctx, user.ID, encSecret); err != nil {
		t.Fatal(err)
	}
	user.TOTPEnabled = true
	if err := db.UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	code := generateTOTPCode(t, secret)

	body := `{"code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/totp", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPDisable(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleTOTPDisable status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify TOTP is disabled.
	updated, err := db.GetUserByID(ctx, user.ID)
	if err != nil || updated == nil {
		t.Fatal("user not found")
	}
	if updated.TOTPEnabled {
		t.Error("TOTP still enabled after disable")
	}
}

func TestTOTPDisable_InvalidCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-dis-bad", "correct-horse-battery-staple")

	secret, _, err := auth.GenerateTOTPSecret(user.Username, "Subflux")
	if err != nil {
		t.Fatal(err)
	}
	encKey := []byte("0123456789abcdef0123456789abcdef")
	encSecret, err := auth.Encrypt([]byte(secret), encKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.SetTOTPSecret(ctx, user.ID, encSecret); err != nil {
		t.Fatal(err)
	}
	user.TOTPEnabled = true
	if err := db.UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	body := `{"code":"000000"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/totp", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPDisable(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleTOTPDisable(invalid code) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

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
	s.handleLogin(rec, req)

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
	s.handleLogin(rec, req)

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
	s.handleSetupCreate(rec, req)

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
	s.handleSetupCreate(rec, req)

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
	s.handleChangePassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleChangePassword(short) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// generateTOTPCode generates a valid TOTP code from a secret for testing.
func generateTOTPCode(t *testing.T, secret string) string {
	t.Helper()
	// Use the auth package's ValidateTOTPCode to verify our code works.
	// Generate code using the OTP library directly.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	return code
}

// =============================================================================
// TOTP verify tests (T-U10C2-01)
// =============================================================================

// setupTOTPUser creates a user with TOTP enabled and returns the plaintext secret.
func setupTOTPUser(t *testing.T, db *store.DB, username string) (*api.User, string) {
	t.Helper()
	user := createTestUser(t, db, username, "correct-horse-battery-staple")
	secret, _, err := auth.GenerateTOTPSecret(username, "Subflux")
	if err != nil {
		t.Fatal(err)
	}
	encKey := []byte("0123456789abcdef0123456789abcdef")
	encSecret, err := auth.Encrypt([]byte(secret), encKey)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.SetTOTPSecret(ctx, user.ID, encSecret); err != nil {
		t.Fatal(err)
	}
	user.TOTPEnabled = true
	if err := db.UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	return user, secret
}

// insertPendingTOTP inserts a pending TOTP entry and returns the token.
func insertPendingTOTP(t *testing.T, cs *authhandlers.CeremonyStore, userID int64, ip string) string {
	t.Helper()
	token, err := authhandlers.GenerateCeremonyToken()
	if err != nil {
		t.Fatal(err)
	}
	cs.TOTP.Store(token, &authhandlers.PendingTOTP{
		UserID:    userID,
		IP:        ip,
		CreatedAt: time.Now(),
	})
	t.Cleanup(func() {
		cs.TOTP.LoadAndDelete(token)
	})
	return token
}

func TestTOTPVerify_ValidCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, secret := setupTOTPUser(t, db, "totp-verify-ok")
	totpToken := insertPendingTOTP(t, s.ceremonies, user.ID, "127.0.0.1")

	code := generateTOTPCode(t, secret)
	body := `{"code":"` + code + `","totp_token":"` + totpToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleTOTPVerify(valid) status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify session cookie was set.
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieNameHTTP || c.Name == auth.CookieNameSecure {
			found = true
		}
	}
	if !found {
		t.Error("no session cookie set after TOTP verify")
	}
}

func TestTOTPVerify_RecoveryCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, _ := setupTOTPUser(t, db, "totp-verify-recovery")
	totpToken := insertPendingTOTP(t, s.ceremonies, user.ID, "127.0.0.1")

	// Store a recovery code hash.
	recoveryCode := "ABCD-EFGH-1234"
	codeHash := auth.HexSHA256(recoveryCode)
	if err := db.SetRecoveryCodes(context.Background(), user.ID, []string{codeHash}); err != nil {
		t.Fatal(err)
	}

	body := `{"code":"` + recoveryCode + `","totp_token":"` + totpToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleTOTPVerify(recovery) status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestTOTPVerify_InvalidToken(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	body := `{"code":"123456","totp_token":"nonexistent-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleTOTPVerify(invalid token) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid or expired totp token" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid or expired totp token")
	}
}

func TestTOTPVerify_ExpiredToken(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "totp-verify-expired", "correct-horse-battery-staple")

	token, err := authhandlers.GenerateCeremonyToken()
	if err != nil {
		t.Fatal(err)
	}
	s.ceremonies.TOTP.Store(token, &authhandlers.PendingTOTP{
		UserID:    user.ID,
		IP:        "127.0.0.1",
		CreatedAt: time.Now().Add(-10 * time.Minute), // expired
	})
	t.Cleanup(func() {
		s.ceremonies.TOTP.LoadAndDelete(token)
	})

	body := `{"code":"123456","totp_token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleTOTPVerify(expired) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestTOTPVerify_InvalidCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, _ := setupTOTPUser(t, db, "totp-verify-bad")
	totpToken := insertPendingTOTP(t, s.ceremonies, user.ID, "127.0.0.1")

	body := `{"code":"000000","totp_token":"` + totpToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleTOTPVerify(invalid code) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "invalid code" {
		t.Errorf("error = %q, want %q", resp["error"], "invalid code")
	}
}

func TestTOTPVerify_ReplayDetection(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, secret := setupTOTPUser(t, db, "totp-verify-replay")

	// Set LastTOTPStep to current step to simulate a recently used code.
	currentStep := time.Now().Unix() / 30
	user.LastTOTPStep = currentStep
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	totpToken := insertPendingTOTP(t, s.ceremonies, user.ID, "127.0.0.1")
	code := generateTOTPCode(t, secret)

	body := `{"code":"` + code + `","totp_token":"` + totpToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleTOTPVerify(replay) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "code already used" {
		t.Errorf("error = %q, want %q", resp["error"], "code already used")
	}
}

func TestTOTPVerify_InvalidJSON(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/totp", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleTOTPVerify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleTOTPVerify(bad json) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Reauth TOTP tests (T-U10C2-02)
// =============================================================================

func TestReauth_TOTP_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, secret := setupTOTPUser(t, db, "reauth-totp-ok")
	token := createTestSession(t, db, user.ID)

	code := generateTOTPCode(t, secret)
	body := `{"method":"totp","code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	req.AddCookie(&http.Cookie{Name: auth.CookieNameHTTP, Value: token})
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleReauth(totp) status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestReauth_TOTP_InvalidCode(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user, _ := setupTOTPUser(t, db, "reauth-totp-bad")

	body := `{"method":"totp","code":"000000"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("handleReauth(totp invalid) status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestReauth_TOTP_NotEnabled(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "reauth-totp-off", "correct-horse-battery-staple")

	body := `{"method":"totp","code":"123456"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleReauth(totp not enabled) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["error"] != "TOTP not enabled" {
		t.Errorf("error = %q, want %q", resp["error"], "TOTP not enabled")
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
	s.handleDeletePasskey(rec, req)

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
	s.handleDeletePasskey(rec, req)

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
	s.handleListPasskeys(rec, req)

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
		_, hash, prefix, suffix, err := auth.GenerateAPIKey()
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
	s.handleListAPIKeys(rec, req)

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
	body := `{"label":"` + longLabel + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/apikeys", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleGenerateAPIKey(rec, req)

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
	s.handleRevokeAPIKey(rec, req)

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
	s.handleRevokeAPIKey(rec, req)

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
	s.handleSetupCreate(rec, req)

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
	s.handleAuthMe(rec, req)

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
	s.handleRenamePasskey(rec, req)

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
	s.handleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(long username) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// =============================================================================
// Recovery codes: status endpoint (GET /api/auth/recovery-codes)
// =============================================================================

func TestRecoveryCodesStatus_no_codes_returns_zero(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rc-status-empty", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/recovery-codes", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleRecoveryCodesStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Remaining int `json:"remaining"`
		Total     int `json:"total"`
	}
	decodeJSON(t, rec, &resp)
	if resp.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", resp.Remaining)
	}
	if resp.Total != auth.RecoveryCodeTotal {
		t.Errorf("total = %d, want %d", resp.Total, auth.RecoveryCodeTotal)
	}
}

func TestRecoveryCodesStatus_reports_remaining(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rc-status-remaining", "correct-horse-battery-staple")

	_, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetRecoveryCodes(context.Background(), user.ID, hashes); err != nil {
		t.Fatal(err)
	}
	// Consume one code.
	if _, err := db.UseRecoveryCode(context.Background(), user.ID, hashes[0]); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/recovery-codes", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleRecoveryCodesStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Remaining int `json:"remaining"`
		Total     int `json:"total"`
	}
	decodeJSON(t, rec, &resp)
	if resp.Remaining != auth.RecoveryCodeTotal-1 {
		t.Errorf("remaining = %d, want %d", resp.Remaining, auth.RecoveryCodeTotal-1)
	}
}

// =============================================================================
// Recovery codes: regenerate endpoint (POST /api/auth/recovery-codes)
// =============================================================================

func TestRegenerateRecoveryCodes_without_totp_returns_400(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rc-regen-no-totp", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/recovery-codes", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleRegenerateRecoveryCodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRegenerateRecoveryCodes_replaces_existing(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rc-regen-ok", "correct-horse-battery-staple")

	// Enable TOTP and seed recovery codes.
	user.TOTPEnabled = true
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	_, oldHashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetRecoveryCodes(context.Background(), user.ID, oldHashes); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/recovery-codes", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleRegenerateRecoveryCodes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Codes []string `json:"recovery_codes"`
	}
	decodeJSON(t, rec, &resp)
	if len(resp.Codes) != auth.RecoveryCodeTotal {
		t.Fatalf("returned %d codes, want %d", len(resp.Codes), auth.RecoveryCodeTotal)
	}

	// Count must be the new total.
	count, err := db.RecoveryCodeCount(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != auth.RecoveryCodeTotal {
		t.Errorf("RecoveryCodeCount after regenerate = %d, want %d", count, auth.RecoveryCodeTotal)
	}

	// Old codes must no longer match. Try to use one; it should fail.
	used, err := db.UseRecoveryCode(context.Background(), user.ID, oldHashes[0])
	if err != nil {
		t.Fatal(err)
	}
	if used {
		t.Error("old recovery code still works after regenerate")
	}

	// New plaintext codes must match the new hashes: hash each, try to use.
	for i, code := range resp.Codes {
		hashed := sha256.Sum256([]byte(code))
		codeHash := hex.EncodeToString(hashed[:])
		used, err := db.UseRecoveryCode(context.Background(), user.ID, codeHash)
		if err != nil {
			t.Fatalf("use new code[%d]: %v", i, err)
		}
		if !used {
			t.Errorf("new code[%d] did not verify", i)
		}
	}
}

// =============================================================================
// TOTP disable: recovery codes are cleared
// =============================================================================

func TestTOTPDisable_clears_recovery_codes(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	cfg, ok := s.state().cfg.(*authTestConfig)
	if !ok {
		t.Fatal("unexpected config type")
	}
	_ = cfg // no additional config needed; authTestConfig already supplies TOTPEncryptionKey
	ctx := context.Background()

	user := createTestUser(t, db, "totp-disable-clears", "correct-horse-battery-staple")

	// Enable TOTP with a real secret + stored codes.
	secret, err := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: user.Username})
	if err != nil {
		t.Fatal(err)
	}
	// Use the public helper to avoid reimplementing Encrypt in tests.
	totpKey, keyErr := s.state().cfg.TOTPEncryptionKey()
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	encSecret, err := auth.Encrypt([]byte(secret.Secret()), totpKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTOTPSecret(ctx, user.ID, encSecret); err != nil {
		t.Fatal(err)
	}
	user.TOTPEnabled = true
	if err := db.UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	_, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetRecoveryCodes(ctx, user.ID, hashes); err != nil {
		t.Fatal(err)
	}

	// Craft a valid TOTP code for the current step.
	code, err := totp.GenerateCode(secret.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	body := `{"code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/totp", strings.NewReader(body))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleTOTPDisable(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	count, err := db.RecoveryCodeCount(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("RecoveryCodeCount after disable = %d, want 0", count)
	}
}

// =============================================================================
// Passkey reauth: begin/finish ceremony
// =============================================================================

func TestReauthPasskeyBegin_webauthn_not_configured(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "pk-reauth-no-wa", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth/passkey/begin", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauthPasskeyBegin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestReauthPasskeyFinish_missing_session_token(t *testing.T) {
	t.Parallel()
	// Stand up a webauthn instance so we pass the nil check.
	wa, err := auth.NewWebAuthn("localhost", "Subflux Test", []string{"http://localhost"})
	if err != nil {
		t.Fatal(err)
	}

	s, db := testAuthServer(t)
	s.webauthn = wa
	user := createTestUser(t, db, "pk-reauth-missing-token", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/reauth/passkey/finish", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.handleReauthPasskeyFinish(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
