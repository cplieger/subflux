package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

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
	s.authH.HandleListUsers(rec, req)

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
	s.authH.HandleCreateUser(rec, req)

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
	s.authH.HandleCreateUser(rec, req)

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
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(empty username) status = %d, want %d", rec.Code, http.StatusBadRequest)
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
	s.authH.HandleCreateUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateUser(long username) status = %d, want %d", rec.Code, http.StatusBadRequest)
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
	s.authH.HandleDeleteUser(rec, req)

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
	s.authH.HandleDeleteUser(rec, req)

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
	s.authH.HandleDeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeleteUser(invalid id) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
