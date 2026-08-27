package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/auth/v4"
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
	// Add session cookie so the handler can identify the current session.
	req.AddCookie(&http.Cookie{
		Name:  authhandlers.CookieNameHTTP,
		Value: token,
	})
	rec := httptest.NewRecorder()
	s.authH.HandleChangePassword(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify new password works.
	updated, _, err := db.UserByUsername(t.Context(), "frank")
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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
	req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
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
	hash := auth.HashPassword(newPassword)
	user.PasswordHash = hash
	if err := db.UpdateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}

	// Invalidate all sessions.
	if err := db.DeleteUserSessions(t.Context(), user.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Verify new password works.
	updated, _, err := db.UserByUsername(t.Context(), "mallory")
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

// TestUpdateProfile_DisplayName covers what the handler accepts as a display
// name and what it refuses. The refusals are the load-bearing half: this value
// is rendered by every surface that lists the account and is handed to the
// user's password manager as the passkey label, so a bidi control here
// reorders what a human reads. A silently-sanitized value would be worse than
// a refusal, and neither failure shows up at runtime.
func TestUpdateProfile_DisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStored string
		wantStatus int
	}{
		{
			name:       "plain name is stored",
			body:       `{"display_name":"Ada Lovelace"}`,
			wantStatus: http.StatusOK,
			wantStored: "Ada Lovelace",
		},
		{
			name:       "surrounding whitespace is trimmed",
			body:       `{"display_name":"  Ada Lovelace  "}`,
			wantStatus: http.StatusOK,
			wantStored: "Ada Lovelace",
		},
		{
			name:       "non-ASCII name is kept intact",
			body:       `{"display_name":"Ada Löwe 好"}`,
			wantStatus: http.StatusOK,
			wantStored: "Ada Löwe 好",
		},
		{
			name:       "empty value clears the display name",
			body:       `{"display_name":""}`,
			wantStatus: http.StatusOK,
			wantStored: "",
		},
		{
			name:       "bidi override is refused",
			body:       `{"display_name":"Ada\u202eevoL"}`,
			wantStatus: http.StatusBadRequest,
			wantStored: "",
		},
		{
			name:       "newline is refused",
			body:       `{"display_name":"Ada\nLovelace"}`,
			wantStatus: http.StatusBadRequest,
			wantStored: "",
		},
		{
			name:       "NUL is refused",
			body:       `{"display_name":"Ada\u0000"}`,
			wantStatus: http.StatusBadRequest,
			wantStored: "",
		},
		{
			name:       "over the length cap is refused",
			body:       `{"display_name":"` + strings.Repeat("a", 129) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantStored: "",
		},
		{
			name:       "at the length cap is stored",
			body:       `{"display_name":"` + strings.Repeat("a", 128) + `"}`,
			wantStatus: http.StatusOK,
			wantStored: strings.Repeat("a", 128),
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s, db := testAuthServer(t)
			username := "profile" + strconv.Itoa(i)
			user := createTestUser(t, db, username, "correct-horse-battery-staple")

			req := httptest.NewRequest(http.MethodPut, "/api/auth/profile", strings.NewReader(test.body))
			req = req.WithContext(authhandlers.NewUserContext(req.Context(), user))
			rec := httptest.NewRecorder()
			s.authH.HandleUpdateProfile(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("HandleUpdateProfile(%s) status = %d, want %d; body: %s",
					test.body, rec.Code, test.wantStatus, rec.Body.String())
			}

			stored, found, err := db.UserByUsername(t.Context(), username)
			if err != nil || !found {
				t.Fatalf("UserByUsername(%q) = found %v, err %v", username, found, err)
			}
			if stored.DisplayName != test.wantStored {
				t.Errorf("HandleUpdateProfile(%s) stored DisplayName = %q, want %q",
					test.body, stored.DisplayName, test.wantStored)
			}
		})
	}
}
