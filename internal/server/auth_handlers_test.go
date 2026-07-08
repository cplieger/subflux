package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authlib "github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

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
