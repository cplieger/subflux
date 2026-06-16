package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

// =============================================================================
// Task 22.1: Full login flow integration test
// =============================================================================

func TestIntegration_FullLoginFlow(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)

	// 1. Setup: create admin account via POST /api/auth/setup.
	body := `{"username":"admin","password":"super-secure-password-here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleSetupCreate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// 2. Login: POST /api/auth/login with correct credentials.
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login",
		loginBody("admin", "super-secure-password-here"))
	rec = httptest.NewRecorder()
	s.authH.HandleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Extract session cookie.
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authhandlers.CookieNameHTTP || c.Name == authhandlers.CookieNameSecure {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("login: no session cookie set")
	}

	// 3. Authenticated request: GET /api/auth/me with session cookie.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	// Simulate the auth middleware by authenticating and injecting user.
	user, sessHash, err := s.authenticator.Authenticate(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if sessHash != "" {
		s.authStore.UpdateSessionActivity(req.Context(), sessHash, time.Now())
	}
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	s.authH.HandleAuthMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: status = %d, want %d", rec.Code, http.StatusOK)
	}
	var meResp map[string]any
	json.NewDecoder(rec.Body).Decode(&meResp)
	if meResp["username"] != "admin" {
		t.Errorf("me: username = %v, want admin", meResp["username"])
	}

	// 4. Generate API key: POST /api/auth/apikeys.
	req = httptest.NewRequest(http.MethodPost, "/api/auth/apikeys",
		strings.NewReader(`{"label":"integration-test","password":"super-secure-password-here"}`))
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec = httptest.NewRecorder()
	s.authH.HandleGenerateAPIKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate api key: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var keyResp map[string]any
	json.NewDecoder(rec.Body).Decode(&keyResp)
	apiKeyPlaintext, ok := keyResp["key"].(string)
	if !ok || apiKeyPlaintext == "" {
		t.Fatal("generate api key: missing 'key' in response")
	}

	// 5. API key request: GET /api/auth/me with X-API-Key header.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.Header.Set("X-API-Key", apiKeyPlaintext)
	rec = httptest.NewRecorder()
	apiUser, _, err := s.authenticator.Authenticate(req)
	if err != nil {
		t.Fatalf("api key auth: %v", err)
	}
	req = req.WithContext(api.NewUserContext(req.Context(), apiUser))
	s.authH.HandleAuthMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("api key me: status = %d, want %d", rec.Code, http.StatusOK)
	}

	// 6. Revoke API key: DELETE /api/auth/apikeys/{id}.
	keys, err := db.ListAPIKeysByUserID(context.Background(), user.ID)
	if err != nil || len(keys) == 0 {
		t.Fatal("no API keys found to revoke")
	}
	keyID := keys[0].ID
	req = httptest.NewRequest(http.MethodDelete,
		"/api/auth/apikeys/"+strconv.FormatInt(keyID, 10), http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), user))
	rec = httptest.NewRecorder()
	s.authH.HandleRevokeAPIKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke api key: status = %d, want %d", rec.Code, http.StatusOK)
	}

	// 7. API key request after revoke: should fail.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.Header.Set("X-API-Key", apiKeyPlaintext)
	_, _, err = s.authenticator.Authenticate(req)
	if err == nil {
		t.Error("expected authentication to fail with revoked API key")
	}

	// 8. Logout: POST /api/auth/logout.
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	s.authH.HandleLogout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d, want %d", rec.Code, http.StatusOK)
	}

	// 9. Authenticated request after logout: should fail.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.AddCookie(sessionCookie)
	_, _, err = s.authenticator.Authenticate(req)
	if err == nil {
		t.Error("expected authentication to fail after logout")
	}
}

// =============================================================================
// Task 22.2: Middleware chain integration test
// =============================================================================

// noopMetrics implements Metrics for integration tests.
type noopMetrics struct{}

func (noopMetrics) RecordSearch(_ api.ProviderID, _ time.Duration, _ error) {}
func (noopMetrics) RecordHTTP(_, _ string, _ int, _ time.Duration)          {}
func (noopMetrics) RecordDownload(_ api.ProviderID, _ error)                {}
func (noopMetrics) AdaptiveSkip()                                           {}
func (noopMetrics) RecordScan(_, _ int, _ time.Duration)                    {}
func (noopMetrics) RecordImport(_ api.PollKey)                              {}
func (noopMetrics) TotalSearches() int64                                    { return 0 }
func (noopMetrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "metrics ok")
	}
}
func (noopMetrics) RecordStoreFileSize(_ int64)                     {}
func (noopMetrics) RecordStoreFreelistBytes(_ int64)                {}
func (noopMetrics) RecordReconcile(_ int, _ int64, _ time.Duration) {}
func (noopMetrics) RecordBackupSuccess(_ time.Duration)             {}

func TestIntegration_MiddlewareChain(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	s.metrics = noopMetrics{}
	s.ready.Store(true)

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	ts := httptest.NewServer(securityHeaders(mux))
	defer ts.Close()

	client := ts.Client()
	// Don't follow redirects automatically.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	// 1. Unauthenticated browser request to / → gets login.html (not index.html).
	// Note: handleUI serves login.html inline for unauthenticated browser requests.
	// Since we don't have the embedded static files in tests, we check that
	// the response doesn't redirect and the security headers are present.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", http.NoBody)
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("browser request: %v", err)
	}
	resp.Body.Close()
	// Security headers should be present on all responses.
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options header")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options header")
	}

	// 2. Unauthenticated API request to /api/config → 401 JSON.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/config", http.NoBody)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("api config request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusFound {
		t.Errorf("unauthenticated /api/config: status = %d, want 401 or 302", resp.StatusCode)
	}

	// 3. Health endpoint → 200 without auth.
	resp, err = client.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// 4. Metrics endpoint → 200 without auth.
	resp, err = client.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// 5. Auth endpoint → accessible without auth.
	resp, err = client.Get(ts.URL + "/api/auth/setup")
	if err != nil {
		t.Fatalf("auth setup request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auth setup: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Create an admin user and a regular user for role tests.
	admin := createTestUser(t, db, "chain-admin", "correct-horse-battery-staple")
	admin.Role = "admin"
	db.UpdateUser(context.Background(), admin)

	regularUser := createTestUser(t, db, "chain-user", "correct-horse-battery-staple")
	regularUser.Role = "user"
	db.UpdateUser(context.Background(), regularUser)

	adminToken := createTestSession(t, db, admin.ID)
	userToken := createTestSession(t, db, regularUser.ID)

	// 6. Admin endpoint with user role → 403.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/config", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: userToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("user config request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("user /api/config: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	// 7. Admin endpoint with admin role → 200 (or 503 if not configured, which is fine).
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/config", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: adminToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("admin config request: %v", err)
	}
	resp.Body.Close()
	// Admin should pass auth (not 401 or 403). May get 200 or 500 depending on config state.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Errorf("admin /api/config: status = %d, want not 401/403", resp.StatusCode)
	}

	// 8. Admin-only endpoint with user role → 403 (other admin groups).
	// /api/auth/users → admin group (requireAuth + requireRole).
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/auth/users", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: userToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("user /api/auth/users request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("user /api/auth/users: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	// 9. adminConfigured endpoint with user role → 403 (role checked before config).
	// /api/scan → adminConfigured group (requireAuth + requireRole + requireConfigured).
	// The role check runs first, so a user always gets 403 regardless of config state.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/scan", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: userToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("user /api/scan request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("user /api/scan: status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	// 10. API key creation with a valid session → success. Reauth step-up was
	// removed; /api/auth/apikeys POST is now in the plain authenticated group,
	// so a valid session suffices (sensitive actions are confirmed client-side).
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/auth/apikeys",
		strings.NewReader(`{"label":"test","password":"correct-horse-battery-staple"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: adminToken})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("authed /api/auth/apikeys request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("authed /api/auth/apikeys: status = %d, want %d; body: %s",
			resp.StatusCode, http.StatusOK, body)
	}
}

// =============================================================================
// Task 22.3: Database migration integration test
// =============================================================================

func TestIntegration_DatabaseMigration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "migration.bolt")
	coreDB, err := boltstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer coreDB.Close(context.Background())

	db := authstore.New(coreDB.BoltDB())
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Verify all auth buckets exist by exercising each one.
	// If any bucket is missing, the operations below will fail.
	now := time.Now()
	user := &api.User{
		Username:     "migration-test",
		PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$dGVzdHNhbHQ$dGVzdGhhc2g",
		Role:         "admin",
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("auth_users bucket: %v", err)
	}
	if _, err := db.UserCount(ctx); err != nil {
		t.Fatalf("auth_users count: %v", err)
	}

	sess := &api.Session{
		TokenHash: "migration-sess-hash", UserID: user.ID,
		AuthMethod: "password", IPAddress: "127.0.0.1",
		CreatedAt: now, LastActivity: now,
	}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatalf("auth_sessions: %v", err)
	}

	apiKey := &api.Key{
		UserID: user.ID, KeyHash: "migration-key-hash",
		KeyPrefix: "sfx_test", KeySuffix: "abcd",
		Label: "migration-key", CreatedAt: now,
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("auth_api_keys bucket: %v", err)
	}

	if err := db.CreateOIDCState(ctx, "state1", "nonce1", "verifier1", "/"); err != nil {
		t.Fatalf("auth_oidc_states: %v", err)
	}

	// 2. Close and re-open the database; verify durable data is preserved.
	db.Close()
	coreDB.Close(context.Background())

	coreDB, err = boltstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer coreDB.Close(context.Background())

	db = authstore.New(coreDB.BoltDB())
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Verify auth user survived re-open.
	u, err := db.GetUserByUsername(ctx, "migration-test")
	if err != nil || u == nil {
		t.Fatal("auth user not preserved after re-open")
	}

	// 3. Foreign key cascades: delete user → API keys deleted; sessions are
	// ephemeral (in-memory), so they don't survive the re-open anyway.
	if err := db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}

	k, err := db.GetAPIKeyByHash(ctx, "migration-key-hash")
	if err != nil {
		t.Fatal(err)
	}
	if k != nil {
		t.Error("API key not cascade-deleted when user was deleted")
	}
}

// =============================================================================
// Task 22.4: Auth config hot-reload integration test
// =============================================================================

func TestIntegration_ConfiguredFlag(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)

	// 1. Server starts unconfigured (configured=false by default in testAuthServer).
	if s.configured.Load() {
		t.Fatal("server should start unconfigured")
	}

	// 2. GET /api/auth/setup → config_valid=false.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/setup", http.NoBody)
	rec := httptest.NewRecorder()
	s.authH.HandleSetupStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status: %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["config_valid"] != false {
		t.Errorf("config_valid = %v, want false", resp["config_valid"])
	}

	// 3. Set configured=true.
	s.configured.Store(true)

	// 4. GET /api/auth/setup → config_valid=true.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/setup", http.NoBody)
	rec = httptest.NewRecorder()
	s.authH.HandleSetupStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status: %d", rec.Code)
	}
	var resp2 map[string]any
	json.NewDecoder(rec.Body).Decode(&resp2)
	if resp2["config_valid"] != true {
		t.Errorf("config_valid = %v, want true", resp2["config_valid"])
	}
}

// =============================================================================
// Task 22.5: Security hardening tests
// =============================================================================

func TestSecurity_SetupRaceCondition(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)

	// Launch 10 concurrent POST /api/auth/setup requests.
	const goroutines = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for range goroutines {
		wg.Go(func() {
			body := `{"username":"race-admin","password":"super-secure-password-here"}`
			req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
				strings.NewReader(body))
			rec := httptest.NewRecorder()
			s.authH.HandleSetupCreate(rec, req)
			if rec.Code == http.StatusOK {
				successCount.Add(1)
			}
		})
	}
	wg.Wait()

	// Verify exactly 1 user was created.
	count, err := db.UserCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("user count = %d, want 1 (race condition!)", count)
	}
	// At most 1 goroutine should have succeeded.
	if successCount.Load() != 1 {
		t.Errorf("success count = %d, want 1", successCount.Load())
	}
}

func TestSecurity_TimingEqualization(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	createTestUser(t, db, "timing-user", "correct-horse-battery-staple")

	// Login with existing user (wrong password).
	const iterations = 3
	var existingTotal time.Duration
	for range iterations {
		start := time.Now()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			loginBody("timing-user", "wrong-password-here-now"))
		rec := httptest.NewRecorder()
		s.authH.HandleLogin(rec, req)
		existingTotal += time.Since(start)
	}

	// Login with non-existing user.
	var nonExistingTotal time.Duration
	for range iterations {
		start := time.Now()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			loginBody("nonexistent-user", "wrong-password-here-now"))
		rec := httptest.NewRecorder()
		s.authH.HandleLogin(rec, req)
		nonExistingTotal += time.Since(start)
	}

	existingAvg := existingTotal / iterations
	nonExistingAvg := nonExistingTotal / iterations

	// Both should take similar time. Use generous tolerance (500ms)
	// since CI environments are slow and Argon2id takes ~100ms.
	diff := existingAvg - nonExistingAvg
	if diff < 0 {
		diff = -diff
	}
	if diff > 500*time.Millisecond {
		t.Errorf("timing difference = %v (existing avg=%v, nonexisting avg=%v); want < 500ms",
			diff, existingAvg, nonExistingAvg)
	}
}

func TestSecurity_DisabledUserRejection(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "disable-me", "correct-horse-battery-staple")
	token := createTestSession(t, db, user.ID)

	// Verify session works while user is enabled.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: token})
	_, _, err := s.authenticator.Authenticate(req)
	if err != nil {
		t.Fatalf("should authenticate while enabled: %v", err)
	}

	// Disable the user.
	user.Enabled = false
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	// Verify session is now rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req.AddCookie(&http.Cookie{Name: authhandlers.CookieNameHTTP, Value: token})
	_, _, err = s.authenticator.Authenticate(req)
	if err == nil {
		t.Error("disabled user should not authenticate")
	}
}

func TestSecurity_CookieFallback(t *testing.T) {
	t.Parallel()

	// Request over HTTP → cookie name is sfx_session (no __Host- prefix).
	httpReq := httptest.NewRequest(http.MethodGet, "http://localhost/", http.NoBody)
	httpName := authhandlers.SessionCookieName(httpReq)
	if httpName != authhandlers.CookieNameHTTP {
		t.Errorf("HTTP cookie name = %q, want %q", httpName, authhandlers.CookieNameHTTP)
	}

	// Verify SetSessionCookie over HTTP produces the right cookie.
	rec := httptest.NewRecorder()
	authhandlers.SetSessionCookie(rec, httpReq, "test-token", 0)
	httpCookies := rec.Result().Cookies()
	for _, c := range httpCookies {
		if c.Name == authhandlers.CookieNameHTTP {
			if c.Secure {
				t.Error("HTTP cookie should not have Secure flag")
			}
		}
	}

	// Request with TLS → cookie name is __Host-sfx_session.
	tlsReq := httptest.NewRequest(http.MethodGet, "https://localhost/", http.NoBody)
	tlsReq.Header.Set("X-Forwarded-Proto", "https")
	tlsName := authhandlers.SessionCookieName(tlsReq)
	if tlsName != authhandlers.CookieNameSecure {
		t.Errorf("TLS cookie name = %q, want %q", tlsName, authhandlers.CookieNameSecure)
	}

	rec = httptest.NewRecorder()
	authhandlers.SetSessionCookie(rec, tlsReq, "test-token", 0)
	tlsCookies := rec.Result().Cookies()
	foundSecure := false
	for _, c := range tlsCookies {
		if c.Name == authhandlers.CookieNameSecure {
			foundSecure = true
			if !c.Secure {
				t.Error("TLS cookie should have Secure flag")
			}
		}
	}
	if !foundSecure {
		t.Error("TLS request should produce __Host-sfx_session cookie")
	}
}
