package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v6"
	authwebauthn "github.com/cplieger/auth/v6/webauthn"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/webhttp/v3"
)

// liveCeremony returns a real in-flight WebAuthn ceremony. Only the library can
// mint one (Ceremony's state is unexported), and its own deadline is what the
// ceremony store evicts on.
func liveCeremony(t *testing.T) authwebauthn.Ceremony {
	t.Helper()
	rp, err := authwebauthn.New(authwebauthn.RPConfig{
		ID:          "example.com",
		DisplayName: "Test RP",
	})
	if err != nil {
		t.Fatalf("webauthn.New: %v", err)
	}
	origin, err := authwebauthn.ParseOrigin("https://example.com")
	if err != nil {
		t.Fatalf("ParseOrigin: %v", err)
	}
	_, ceremony, err := authwebauthn.BeginLogin(rp, origin)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	return ceremony
}

func TestListPasskeys_Empty(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "heidi", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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

func TestListPasskeys_WithData(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "listpk-data", "correct-horse-battery-staple")

	for i := range 2 {
		pk := &auth.PasskeyCredential{
			UserID:       user.ID,
			CredentialID: []byte("cred-" + strconv.Itoa(i)),
			PublicKey:    []byte("pub-" + strconv.Itoa(i)),
			AAGUID:       make([]byte, 16),
			Name:         "Key " + strconv.Itoa(i),
			CreatedAt:    time.Now(),
		}
		if err := db.CreatePasskey(t.Context(), pk); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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

func TestRenamePasskey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-pk", "correct-horse-battery-staple")

	passkey := &auth.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("test-cred-id"),
		PublicKey:    []byte("test-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "Old Name",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(t.Context(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.PasskeysByUserID(t.Context(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	body := `{"name":"New Name"}`
	req := httptest.NewRequest(http.MethodPut,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), strings.NewReader(body))
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleRenamePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleRenamePasskey(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRenamePasskey_NameTooLong(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "rename-long", "correct-horse-battery-staple")

	longName := strings.Repeat("x", 129) // exceeds maxPasskeyNameLen=128
	body := `{"name":"` + longName + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/passkeys/1", strings.NewReader(body))
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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

func TestDeletePasskey_InvalidID(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "delpk-inv", "correct-horse-battery-staple")

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/passkeys/xyz", http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeletePasskey(missing id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDeletePasskey_Success(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "delpk-ok", "correct-horse-battery-staple")

	passkey := &auth.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("del-cred-id"),
		PublicKey:    []byte("del-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "To Delete",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(t.Context(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.PasskeysByUserID(t.Context(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
	rec := httptest.NewRecorder()
	s.authH.HandleDeletePasskey(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleDeletePasskey status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify passkey is gone.
	remaining, err := db.PasskeysByUserID(t.Context(), user.ID)
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
	user := &auth.User{
		Username:  "delpk-lastmethod",
		Role:      "admin",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}

	passkey := &auth.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("last-cred-id"),
		PublicKey:    []byte("last-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "Only Passkey",
		CreatedAt:    now,
	}
	if err := db.CreatePasskey(t.Context(), passkey); err != nil {
		t.Fatal(err)
	}

	creds, err := db.PasskeysByUserID(t.Context(), user.ID)
	if err != nil || len(creds) == 0 {
		t.Fatal("no passkeys found")
	}
	pkID := creds[0].ID

	req := httptest.NewRequest(http.MethodDelete,
		"/api/auth/passkeys/"+strconv.FormatInt(pkID, 10), http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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

func TestAuthMe_WithPasskeys(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "me-passkeys", "correct-horse-battery-staple")

	pk := &auth.PasskeyCredential{
		UserID:       user.ID,
		CredentialID: []byte("me-cred-id"),
		PublicKey:    []byte("me-pub-key"),
		AAGUID:       make([]byte, 16),
		Name:         "My Passkey",
		CreatedAt:    time.Now(),
	}
	if err := db.CreatePasskey(t.Context(), pk); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", http.NoBody)
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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

func TestCleanupCeremonies_removes_expired(t *testing.T) {
	t.Parallel()

	cs := authhandlers.NewCeremonyStore()

	expiredWAToken := "expired-wa-cleanup"
	freshWAToken := "fresh-wa-cleanup"

	// The zero Ceremony reports a zero deadline, so it is always past it.
	cs.WebAuthn.Store(expiredWAToken, authwebauthn.Ceremony{})
	cs.WebAuthn.Store(freshWAToken, liveCeremony(t))

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
	cs.WebAuthn.Store(token, authwebauthn.Ceremony{})

	if _, found := cs.ConsumeWebAuthnSession(token); found {
		t.Error("ConsumeWebAuthnSession(past deadline) found = true, want false")
	}

	// Consuming it must also evict it, expired or not.
	if _, exists := cs.WebAuthn.LoadAndDelete(token); exists {
		t.Error("an expired ceremony was left in the map after being consumed")
	}
}

func TestConsumeWebAuthnSession_missing(t *testing.T) {
	t.Parallel()
	cs := authhandlers.NewCeremonyStore()
	if _, found := cs.ConsumeWebAuthnSession("nonexistent-token"); found {
		t.Error("ConsumeWebAuthnSession(unknown token) found = true, want false")
	}
}

func loginBegin(t *testing.T, s *Server, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(webhttp.WithRequestID(t.Context(), "req-42"),
		http.MethodPost, "/api/auth/webauthn/login/begin", http.NoBody)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.authH.HandleWebAuthnLoginBegin(rec, req)
	return rec
}

func TestWebAuthnLoginBegin_originDecidesBeforeAnyChallenge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		origin   string
		wantCode int
		wantErr  string
	}{
		{name: "covered_subdomain_proceeds", origin: "https://subflux.example.com", wantCode: http.StatusOK},
		{
			name: "localhost_is_refused_as_not_covered", origin: "http://localhost:8374", wantCode: http.StatusBadRequest,
			wantErr: `this page's origin "http://localhost:8374" is not covered by the configured relying-party ID "example.com". Set auth.webauthn_rp_id to "localhost" in the Authentication section of Settings`,
		},
		{
			name: "dot_guard", origin: "https://evilexample.com", wantCode: http.StatusBadRequest,
			wantErr: `this page's origin "https://evilexample.com" is not covered by the configured relying-party ID "example.com". Set auth.webauthn_rp_id to "evilexample.com" in the Authentication section of Settings`,
		},
		{
			name: "no_origin_header", origin: "", wantCode: http.StatusBadRequest,
			wantErr: "this request carries no Origin header, so no passkey ceremony can be bound to it",
		},
		{
			name: "trailing_dot_names_the_dotless_host", origin: "https://subflux.example.com.", wantCode: http.StatusBadRequest,
			wantErr: `this page's address ends in a dot. Reach subflux at "https://subflux.example.com"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := testAuthServer(t)
			rp := testRelyingParty(t)
			s.authH.WebAuthnResolver = func() *authwebauthn.RelyingParty { return rp }

			rec := loginBegin(t, s, tt.origin)
			if rec.Code != tt.wantCode {
				t.Fatalf("login begin from %q status = %d, want %d; body %s", tt.origin, rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode == http.StatusOK {
				return
			}
			var resp map[string]string
			decodeJSON(t, rec, &resp)
			if resp["code"] != "webauthn_unsupported_origin" {
				t.Errorf("code = %q, want webauthn_unsupported_origin", resp["code"])
			}
			if resp["error"] != tt.wantErr {
				t.Errorf("error = %q, want %q", resp["error"], tt.wantErr)
			}
		})
	}
}

func TestRequireWebAuthn_unconfiguredEnvelopeCarriesTheRequestIDAndLogs(t *testing.T) {
	logs := capture.Default(t)
	s, _ := testAuthServer(t)

	rec := loginBegin(t, s, "https://subflux.example.com")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("login begin with no relying party status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	decodeJSON(t, rec, &resp)
	if resp["code"] != "webauthn_unconfigured" {
		t.Errorf("code = %q, want webauthn_unconfigured", resp["code"])
	}
	if resp["request_id"] != "req-42" {
		t.Errorf("request_id = %q, want %q: the envelope dropped the correlation id", resp["request_id"], "req-42")
	}
	if logs.CountLevel(slog.LevelWarn, "webauthn: ceremony requested but no relying party is configured") != 1 {
		t.Errorf("Warn records = %d, want 1; messages: %q",
			logs.CountLevel(slog.LevelWarn, "webauthn: ceremony requested but no relying party is configured"), logs.Messages())
	}
}
