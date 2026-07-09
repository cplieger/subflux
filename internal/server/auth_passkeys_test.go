package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

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
	user := &auth.User{
		Username:  "delpk-lastmethod",
		Role:      "admin",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.CreateUser(context.Background(), user); err != nil {
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
