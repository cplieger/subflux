package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
