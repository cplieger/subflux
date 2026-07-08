package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	authlib "github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
)

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
