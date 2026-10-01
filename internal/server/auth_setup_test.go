package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/auth/v6"
	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

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
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify user was created with admin role.
	user, _, err := db.UserByUsername(t.Context(), "admin")
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

// testRelyingParty builds a relying party for example.com with no origin list,
// the shape buildWebAuthn produces.
func testRelyingParty(t *testing.T) *authwebauthn.RelyingParty {
	t.Helper()
	rp, err := authwebauthn.New(authwebauthn.RPConfig{ID: "example.com", DisplayName: "Test RP"})
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	return rp
}

func setupStatus(t *testing.T, s *Server) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/setup", http.NoBody)
	rec := httptest.NewRecorder()
	s.authH.HandleSetupStatus(rec, req)
	var resp map[string]any
	if rec.Code == http.StatusOK {
		decodeJSON(t, rec, &resp)
	}
	return rec.Code, resp
}

func TestSetupStatus_passkeyLogin_hiddenWithoutARelyingParty(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "pk-norp", "correct-horse-battery-staple")
	if err := db.CreatePasskey(t.Context(), &auth.PasskeyCredential{
		UserID: user.ID, CredentialID: []byte("norp-cred"), PublicKey: []byte("pub"), RPID: "example.com",
	}); err != nil {
		t.Fatal(err)
	}

	code, resp := setupStatus(t, s)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp["passkey_login_available"] != false {
		t.Errorf("passkey_login_available with no relying party = %v, want false", resp["passkey_login_available"])
	}
}

func TestSetupStatus_passkeyLogin_hiddenWithNoCredentials(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)
	rp := testRelyingParty(t)
	s.authH.WebAuthnResolver = func() *authwebauthn.RelyingParty { return rp }

	code, resp := setupStatus(t, s)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp["passkey_login_available"] != false {
		t.Errorf("passkey_login_available with no credentials = %v, want false", resp["passkey_login_available"])
	}
}

func TestSetupStatus_passkeyLogin_shownWithAnUnreportedDiscoverability(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	rp := testRelyingParty(t)
	s.authH.WebAuthnResolver = func() *authwebauthn.RelyingParty { return rp }
	user := createTestUser(t, db, "pk-shown", "correct-horse-battery-staple")
	if err := db.CreatePasskey(t.Context(), &auth.PasskeyCredential{
		UserID: user.ID, CredentialID: []byte("shown-cred"), PublicKey: []byte("pub"), RPID: "example.com",
	}); err != nil {
		t.Fatal(err)
	}

	code, resp := setupStatus(t, s)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp["passkey_login_available"] != true {
		t.Errorf("passkey_login_available with a pre-credProps credential = %v, want true", resp["passkey_login_available"])
	}
}

// failingPasskeyProbeStore is the real store with the discoverable-login
// predicate failing, so the setup payload's advisory arm can be observed.
type failingPasskeyProbeStore struct{ *authstore.Store }

func (failingPasskeyProbeStore) AnyPasskeyForDiscoverableLogin(context.Context, string) (bool, error) {
	return false, errors.New("bucket unreadable")
}

// failingUserCountStore is the real store with UserCount failing.
type failingUserCountStore struct{ *authstore.Store }

func (failingUserCountStore) UserCount(context.Context) (int, error) {
	return 0, errors.New("bucket unreadable")
}

func TestSetupStatus_passkeyLogin_storeErrorFailsOpenAndLogs(t *testing.T) {
	logs := capture.Default(t)
	s, db := testAuthServer(t)
	rp := testRelyingParty(t)
	s.authH.WebAuthnResolver = func() *authwebauthn.RelyingParty { return rp }
	s.authH.Store = failingPasskeyProbeStore{db}

	code, resp := setupStatus(t, s)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a predicate failure must not take the login page down", code)
	}
	if resp["passkey_login_available"] != true {
		t.Errorf("passkey_login_available on a store error = %v, want true (advisory, fail open)", resp["passkey_login_available"])
	}
	if logs.CountLevel(slog.LevelError, "setup: passkey login probe") != 1 {
		t.Errorf("Error records for the probe failure = %d, want 1; messages: %q",
			logs.CountLevel(slog.LevelError, "setup: passkey login probe"), logs.Messages())
	}
}

func TestSetupStatus_userCountErrorStillAnswers500(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	s.authH.Store = failingUserCountStore{db}

	code, _ := setupStatus(t, s)
	if code != http.StatusInternalServerError {
		t.Fatalf("status on a UserCount error = %d, want 500: setup_required is what the page cannot proceed without", code)
	}
}
