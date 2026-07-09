package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

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
