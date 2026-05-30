package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/server/authhandlers"
)

// storeLink seeds a pending OIDC link and returns its token.
func storeLink(t *testing.T, s *Server, userID int64, sub string) string {
	t.Helper()
	token, err := authhandlers.GenerateCeremonyToken()
	if err != nil {
		t.Fatal(err)
	}
	s.authH.Ceremonies.Link.Store(token, &authhandlers.PendingLink{
		UserID:     userID,
		OIDCSub:    sub,
		OIDCIssuer: "https://idp.example.com",
		CreatedAt:  time.Now(),
	})
	return token
}

func postLink(s *Server, token, password string) *httptest.ResponseRecorder {
	body := `{"link_token":"` + token + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/link", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.authH.HandleOIDCLink(rec, req)
	return rec
}

// Link-on-login: an existing local account is linked to a new OIDC identity
// only after the user proves ownership with the account password.
func TestOIDCLink_requires_password_proof(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	user := createTestUser(t, db, "alex", "correct-horse-battery-staple")
	// Regular (non-admin) account: link-on-login migrates it to SSO-only.
	// (The last-local-admin guard only blocks admins.)
	user.Role = api.RoleUser
	if err := db.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	// Wrong password → 401, single-use token consumed, no link.
	if rec := postLink(s, storeLink(t, s, user.ID, "new-sub"), "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", rec.Code)
	}
	got, _ := db.GetUserByID(context.Background(), user.ID)
	if got.OIDCSub != "" {
		t.Fatalf("OIDCSub = %q, want empty after failed link", got.OIDCSub)
	}

	// Correct password → 200 and the OIDC identity is linked in place.
	if rec := postLink(s, storeLink(t, s, user.ID, "new-sub"), "correct-horse-battery-staple"); rec.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got, _ = db.GetUserByID(context.Background(), user.ID)
	if got.OIDCSub != "new-sub" {
		t.Fatalf("OIDCSub = %q, want new-sub after successful link", got.OIDCSub)
	}
	if got.PasswordHash != "" {
		t.Fatalf("PasswordHash not cleared; link-on-login must migrate to SSO-only")
	}
}

// The last local (password) admin cannot be migrated to SSO-only — that would
// remove the break-glass account.
func TestOIDCLink_refuses_last_local_admin(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)
	admin := createTestUser(t, db, "soleadmin", "correct-horse-battery-staple") // createTestUser makes admins
	rec := postLink(s, storeLink(t, s, admin.ID, "sub-z"), "correct-horse-battery-staple")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for last local admin; body %s", rec.Code, rec.Body.String())
	}
	got, _ := db.GetUserByID(context.Background(), admin.ID)
	if got.OIDCSub != "" || got.PasswordHash == "" {
		t.Fatal("account must be unchanged when migration is refused")
	}
}

// Invalid or expired link tokens are rejected.
func TestOIDCLink_invalid_token(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)
	if rec := postLink(s, "no-such-token", "whatever"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", rec.Code)
	}
}

// Unlink refuses to strand an account with no other login method, but
// succeeds when a password remains.
func TestOIDCUnlink_last_method_guard(t *testing.T) {
	t.Parallel()
	s, db := testAuthServer(t)

	// OIDC-only account: no password, no passkey → unlink refused.
	oidcOnly := &api.User{Username: "oidconly", Role: api.RoleUser, Enabled: true, OIDCSub: "sub-x", OIDCIssuer: "iss"}
	if err := db.CreateUser(context.Background(), oidcOnly); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/oidc/link", http.NoBody)
	req = req.WithContext(api.NewUserContext(req.Context(), oidcOnly))
	rec := httptest.NewRecorder()
	s.authH.HandleOIDCUnlink(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("unlink (no other method) status = %d, want 409", rec.Code)
	}

	// Account with a password → unlink allowed.
	withPass := createTestUser(t, db, "haspass", "correct-horse-battery-staple")
	withPass.OIDCSub = "sub-y"
	withPass.OIDCIssuer = "iss"
	if err := db.UpdateUser(context.Background(), withPass); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodDelete, "/api/auth/oidc/link", http.NoBody)
	req2 = req2.WithContext(api.NewUserContext(req2.Context(), withPass))
	rec2 := httptest.NewRecorder()
	s.authH.HandleOIDCUnlink(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unlink (has password) status = %d, want 200; body %s", rec2.Code, rec2.Body.String())
	}
	got, _ := db.GetUserByID(context.Background(), withPass.ID)
	if got.OIDCSub != "" {
		t.Fatalf("OIDCSub = %q, want empty after unlink", got.OIDCSub)
	}
}
