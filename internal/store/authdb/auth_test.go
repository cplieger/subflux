package authdb

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/store/migrations"

	_ "modernc.org/sqlite"
)

// openTestAuthDB creates an in-memory SQLite database with the full schema
// applied, then initialises an *AuthDB on top of it. This lets auth tests
// run without the parent store.Open() and its subtitle/search schema setup.
func openTestAuthDB(t *testing.T) *AuthDB {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite",
		":memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_time_format=sqlite&_texttotime=1")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, migrations.Schema); err != nil {
		db.Close()
		t.Fatalf("apply schema: %v", err)
	}
	a, err := New(ctx, db)
	if err != nil {
		db.Close()
		t.Fatalf("authdb.New: %v", err)
	}
	t.Cleanup(func() {
		a.Close(ctx)
		db.Close()
	})
	return a
}

func TestCreateUser_sets_id_from_last_insert(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{
		Username: "alice",
		Email:    "alice@example.com",
		Role:     "admin",
		Enabled:  true,
	}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}
	if u.ID == 0 {
		t.Error("CreateUser() did not set ID on struct")
	}
}

func TestCreateUser_duplicate_username_returns_error(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u1 := &api.User{Username: "alice", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u1); err != nil {
		t.Fatalf("CreateUser(first) unexpected error: %v", err)
	}

	u2 := &api.User{Username: "alice", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u2); err == nil {
		t.Error("CreateUser(duplicate) expected error, got nil")
	}
}

func TestGetUser_lookup_methods(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)
	ctx := context.Background()

	// Create users for lookup tests.
	bob := &api.User{Username: "bob", Email: "bob@example.com", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, bob); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	carol := &api.User{Username: "carol", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, carol); err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	dave := &api.User{Username: "dave", Email: "dave@example.com", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, dave); err != nil {
		t.Fatalf("CreateUser(dave): %v", err)
	}
	oidcUser := &api.User{Username: "oidcuser", OIDCSub: "sub-123", OIDCIssuer: "https://issuer.example.com", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, oidcUser); err != nil {
		t.Fatalf("CreateUser(oidcuser): %v", err)
	}

	tests := []struct {
		lookup    func() (*api.User, error)
		checkUser func(t *testing.T, u *api.User)
		name      string
		wantNil   bool
	}{
		{
			name:   "ByID_returns_user",
			lookup: func() (*api.User, error) { return a.GetUserByID(ctx, bob.ID) },
			checkUser: func(t *testing.T, u *api.User) {
				if u.Username != "bob" {
					t.Errorf("Username = %q, want %q", u.Username, "bob")
				}
			},
		},
		{
			name:    "ByID_not_found_returns_nil",
			lookup:  func() (*api.User, error) { return a.GetUserByID(ctx, 99999) },
			wantNil: true,
		},
		{
			name:   "ByUsername_returns_user",
			lookup: func() (*api.User, error) { return a.GetUserByUsername(ctx, "carol") },
			checkUser: func(t *testing.T, u *api.User) {
				if u.ID != carol.ID {
					t.Errorf("ID = %d, want %d", u.ID, carol.ID)
				}
			},
		},
		{
			name:    "ByUsername_not_found_returns_nil",
			lookup:  func() (*api.User, error) { return a.GetUserByUsername(ctx, "nonexistent") },
			wantNil: true,
		},
		{
			name:   "ByEmail_returns_user",
			lookup: func() (*api.User, error) { return a.GetUserByEmail(ctx, "dave@example.com") },
			checkUser: func(t *testing.T, u *api.User) {
				if u.Username != "dave" {
					t.Errorf("Username = %q, want %q", u.Username, "dave")
				}
			},
		},
		{
			name:    "ByEmail_not_found_returns_nil",
			lookup:  func() (*api.User, error) { return a.GetUserByEmail(ctx, "nobody@example.com") },
			wantNil: true,
		},
		{
			name:   "ByOIDCSub_returns_user",
			lookup: func() (*api.User, error) { return a.GetUserByOIDCSub(ctx, "sub-123") },
			checkUser: func(t *testing.T, u *api.User) {
				if u.Username != "oidcuser" {
					t.Errorf("Username = %q, want %q", u.Username, "oidcuser")
				}
			},
		},
		{
			name:    "ByOIDCSub_not_found_returns_nil",
			lookup:  func() (*api.User, error) { return a.GetUserByOIDCSub(ctx, "nonexistent-sub") },
			wantNil: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.lookup()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("got nil, want user")
			}
			if tc.checkUser != nil {
				tc.checkUser(t, got)
			}
		})
	}
}

func TestListUsers_returns_all_ordered_by_username(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	for _, name := range []string{"charlie", "alice", "bob"} {
		u := &api.User{Username: name, Role: "user", Enabled: true}
		if err := a.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser(%q) unexpected error: %v", name, err)
		}
	}

	users, err := a.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() unexpected error: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("ListUsers() returned %d users, want 3", len(users))
	}
	if users[0].Username != "alice" {
		t.Errorf("ListUsers()[0].Username = %q, want %q", users[0].Username, "alice")
	}
	if users[1].Username != "bob" {
		t.Errorf("ListUsers()[1].Username = %q, want %q", users[1].Username, "bob")
	}
	if users[2].Username != "charlie" {
		t.Errorf("ListUsers()[2].Username = %q, want %q", users[2].Username, "charlie")
	}
}

func TestListUsers_empty_returns_nil(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	users, err := a.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("ListUsers() returned %d users, want 0", len(users))
	}
}

func TestUpdateUser_modifies_fields(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "eve", Email: "eve@old.com", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	u.Email = "eve@new.com"
	u.DisplayName = "Eve Updated"
	u.Role = "admin"
	if err := a.UpdateUser(context.Background(), u); err != nil {
		t.Fatalf("UpdateUser() unexpected error: %v", err)
	}

	got, err := a.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID(%d) unexpected error: %v", u.ID, err)
	}
	if got.Email != "eve@new.com" {
		t.Errorf("UpdateUser().Email = %q, want %q", got.Email, "eve@new.com")
	}
	if got.DisplayName != "Eve Updated" {
		t.Errorf("UpdateUser().DisplayName = %q, want %q", got.DisplayName, "Eve Updated")
	}
	if got.Role != "admin" {
		t.Errorf("UpdateUser().Role = %q, want %q", got.Role, "admin")
	}
}

func TestDeleteUser_removes_user(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "frank", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	if err := a.DeleteUser(context.Background(), u.ID); err != nil {
		t.Fatalf("DeleteUser(%d) unexpected error: %v", u.ID, err)
	}

	got, err := a.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID(%d) unexpected error: %v", u.ID, err)
	}
	if got != nil {
		t.Errorf("GetUserByID(%d) after delete = %v, want nil", u.ID, got)
	}
}

func TestUserCount_returns_total(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	count, err := a.UserCount(context.Background())
	if err != nil {
		t.Fatalf("UserCount() unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("UserCount() = %d, want 0", count)
	}

	for _, name := range []string{"a", "b", "c"} {
		u := &api.User{Username: name, Role: "user", Enabled: true}
		if err := a.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser(%q) unexpected error: %v", name, err)
		}
	}

	count, err = a.UserCount(context.Background())
	if err != nil {
		t.Fatalf("UserCount() unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("UserCount() = %d, want 3", count)
	}
}

func TestCreateSession_and_get_by_hash(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "sessuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	sess := &api.Session{
		TokenHash:    "hash-abc-123",
		UserID:       u.ID,
		AuthMethod:   "password",
		IPAddress:    "192.168.1.1",
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := a.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	got, err := a.GetSessionByHash(context.Background(), "hash-abc-123")
	if err != nil {
		t.Fatalf("GetSessionByHash(%q) unexpected error: %v", "hash-abc-123", err)
	}
	if got == nil {
		t.Fatal("GetSessionByHash(hash-abc-123) = nil, want session")
	}
	if got.UserID != u.ID {
		t.Errorf("GetSessionByHash().UserID = %d, want %d", got.UserID, u.ID)
	}
	if got.AuthMethod != "password" {
		t.Errorf("GetSessionByHash().AuthMethod = %q, want %q", got.AuthMethod, "password")
	}
	if got.IPAddress != "192.168.1.1" {
		t.Errorf("GetSessionByHash().IPAddress = %q, want %q", got.IPAddress, "192.168.1.1")
	}
}

func TestGetSessionByHash_not_found_returns_nil(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	got, err := a.GetSessionByHash(context.Background(), "nonexistent-hash")
	if err != nil {
		t.Fatalf("GetSessionByHash(%q) unexpected error: %v", "nonexistent-hash", err)
	}
	if got != nil {
		t.Errorf("GetSessionByHash(nonexistent) = %v, want nil", got)
	}
}

func TestCreateSession_with_nullable_times(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "nulluser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	reauthAt := now.Add(time.Hour)
	oidcExpiry := now.Add(24 * time.Hour)
	sess := &api.Session{
		TokenHash:    "hash-nullable",
		UserID:       u.ID,
		AuthMethod:   "oidc",
		CreatedAt:    now,
		LastActivity: now,
		ReauthAt:     &reauthAt,
		OIDCExpiry:   &oidcExpiry,
	}
	if err := a.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	got, err := a.GetSessionByHash(context.Background(), "hash-nullable")
	if err != nil {
		t.Fatalf("GetSessionByHash() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionByHash() = nil, want session")
	}
	if got.ReauthAt == nil {
		t.Fatal("GetSessionByHash().ReauthAt = nil, want non-nil")
	}
	if got.OIDCExpiry == nil {
		t.Fatal("GetSessionByHash().OIDCExpiry = nil, want non-nil")
	}
}

func TestUpdateSessionActivity_updates_timestamp(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "actuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	sess := &api.Session{
		TokenHash: "hash-activity", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now, LastActivity: now,
	}
	if err := a.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	later := now.Add(5 * time.Minute)
	if err := a.UpdateSessionActivity(context.Background(), "hash-activity", later); err != nil {
		t.Fatalf("UpdateSessionActivity() unexpected error: %v", err)
	}

	got, err := a.GetSessionByHash(context.Background(), "hash-activity")
	if err != nil {
		t.Fatalf("GetSessionByHash() unexpected error: %v", err)
	}
	if got.LastActivity.Before(now) {
		t.Errorf("UpdateSessionActivity() did not update timestamp")
	}
}

func TestDeleteSession_removes_session(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "deluser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	sess := &api.Session{
		TokenHash: "hash-del", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now, LastActivity: now,
	}
	if err := a.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	if err := a.DeleteSession(context.Background(), "hash-del"); err != nil {
		t.Fatalf("DeleteSession() unexpected error: %v", err)
	}

	got, err := a.GetSessionByHash(context.Background(), "hash-del")
	if err != nil {
		t.Fatalf("GetSessionByHash() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetSessionByHash() after delete = %v, want nil", got)
	}
}

func TestDeleteUserSessions_keeps_current_session(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "multiuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	for _, hash := range []string{"keep-this", "delete-me-1", "delete-me-2"} {
		sess := &api.Session{
			TokenHash: hash, UserID: u.ID, AuthMethod: "password",
			CreatedAt: now, LastActivity: now,
		}
		if err := a.CreateSession(context.Background(), sess); err != nil {
			t.Fatalf("CreateSession(%q) unexpected error: %v", hash, err)
		}
	}

	if err := a.DeleteUserSessions(context.Background(), u.ID, "keep-this"); err != nil {
		t.Fatalf("DeleteUserSessions() unexpected error: %v", err)
	}

	kept, err := a.GetSessionByHash(context.Background(), "keep-this")
	if err != nil {
		t.Fatalf("GetSessionByHash(keep-this) unexpected error: %v", err)
	}
	if kept == nil {
		t.Error("DeleteUserSessions() deleted the excepted session")
	}

	for _, hash := range []string{"delete-me-1", "delete-me-2"} {
		got, err := a.GetSessionByHash(context.Background(), hash)
		if err != nil {
			t.Fatalf("GetSessionByHash(%q) unexpected error: %v", hash, err)
		}
		if got != nil {
			t.Errorf("GetSessionByHash(%q) after DeleteUserSessions = %v, want nil", hash, got)
		}
	}
}

func TestCleanupExpiredSessions_removes_idle_and_absolute(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "cleanuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)

	active := &api.Session{
		TokenHash: "active", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now.Add(-time.Hour), LastActivity: now,
	}

	idle := &api.Session{
		TokenHash: "idle", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now.Add(-time.Hour), LastActivity: now.Add(-25 * time.Hour),
	}

	absolute := &api.Session{
		TokenHash: "absolute", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now.Add(-8 * 24 * time.Hour), LastActivity: now,
	}

	for _, s := range []*api.Session{active, idle, absolute} {
		if err := a.CreateSession(context.Background(), s); err != nil {
			t.Fatalf("CreateSession(%q) unexpected error: %v", s.TokenHash, err)
		}
	}

	deleted, err := a.CleanupExpiredSessions(context.Background(), now,
		24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions() unexpected error: %v", err)
	}
	if deleted != 2 {
		t.Errorf("CleanupExpiredSessions() deleted %d, want 2", deleted)
	}

	got, err := a.GetSessionByHash(context.Background(), "active")
	if err != nil {
		t.Fatalf("GetSessionByHash(active) unexpected error: %v", err)
	}
	if got == nil {
		t.Error("CleanupExpiredSessions() deleted the active session")
	}
}

func TestCreateAPIKey_sets_id(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "keyuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	key := &api.Key{
		UserID:    u.ID,
		KeyHash:   "sha256-abc",
		KeyPrefix: "sf_",
		KeySuffix: "xyz",
		Label:     "test key",
	}
	if err := a.CreateAPIKey(context.Background(), key); err != nil {
		t.Fatalf("CreateAPIKey() unexpected error: %v", err)
	}
	if key.ID == 0 {
		t.Error("CreateAPIKey() did not set ID on struct")
	}
}

func TestGetAPIKeyByHash_returns_key(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "hashuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	key := &api.Key{
		UserID: u.ID, KeyHash: "sha256-lookup",
		KeyPrefix: "sf_", KeySuffix: "end", Label: "lookup key",
	}
	if err := a.CreateAPIKey(context.Background(), key); err != nil {
		t.Fatalf("CreateAPIKey() unexpected error: %v", err)
	}

	got, err := a.GetAPIKeyByHash(context.Background(), "sha256-lookup")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash(%q) unexpected error: %v", "sha256-lookup", err)
	}
	if got == nil {
		t.Fatal("GetAPIKeyByHash() = nil, want key")
	}
	if got.Label != "lookup key" {
		t.Errorf("GetAPIKeyByHash().Label = %q, want %q", got.Label, "lookup key")
	}
}

func TestGetAPIKeyByHash_not_found_returns_nil(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	got, err := a.GetAPIKeyByHash(context.Background(), "nonexistent-hash")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash(%q) unexpected error: %v", "nonexistent-hash", err)
	}
	if got != nil {
		t.Errorf("GetAPIKeyByHash(nonexistent) = %v, want nil", got)
	}
}

func TestListAPIKeysByUserID_ordered_newest_first(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "listuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	for _, label := range []string{"first", "second", "third"} {
		key := &api.Key{
			UserID: u.ID, KeyHash: "hash-" + label,
			KeyPrefix: "sf_", KeySuffix: label[:3], Label: label,
		}
		if err := a.CreateAPIKey(context.Background(), key); err != nil {
			t.Fatalf("CreateAPIKey(%q) unexpected error: %v", label, err)
		}
	}

	keys, err := a.ListAPIKeysByUserID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUserID() unexpected error: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("ListAPIKeysByUserID() returned %d keys, want 3", len(keys))
	}

	labels := map[string]bool{}
	for _, k := range keys {
		labels[k.Label] = true
	}
	for _, want := range []string{"first", "second", "third"} {
		if !labels[want] {
			t.Errorf("ListAPIKeysByUserID() missing key %q", want)
		}
	}
}

func TestDeleteAPIKey_removes_key(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "delkeyuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	key := &api.Key{
		UserID: u.ID, KeyHash: "sha256-del",
		KeyPrefix: "sf_", KeySuffix: "del", Label: "delete me",
	}
	if err := a.CreateAPIKey(context.Background(), key); err != nil {
		t.Fatalf("CreateAPIKey() unexpected error: %v", err)
	}

	if err := a.DeleteAPIKey(context.Background(), key.ID, key.UserID); err != nil {
		t.Fatalf("DeleteAPIKey(%d) unexpected error: %v", key.ID, err)
	}

	got, err := a.GetAPIKeyByHash(context.Background(), "sha256-del")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetAPIKeyByHash() after delete = %v, want nil", got)
	}
}

func TestTOTPSecret_set_get_clear_roundtrip(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "totpuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	secret, err := a.GetTOTPSecret(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetTOTPSecret() unexpected error: %v", err)
	}
	if secret != nil {
		t.Errorf("GetTOTPSecret() = %v, want nil (no secret set)", secret)
	}

	encrypted := []byte("encrypted-totp-secret-data")
	if err := a.SetTOTPSecret(context.Background(), u.ID, encrypted); err != nil {
		t.Fatalf("SetTOTPSecret() unexpected error: %v", err)
	}

	secret, err = a.GetTOTPSecret(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetTOTPSecret() unexpected error: %v", err)
	}
	if string(secret) != string(encrypted) {
		t.Errorf("GetTOTPSecret() = %q, want %q", secret, encrypted)
	}

	got, err := a.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID() unexpected error: %v", err)
	}
	if !got.TOTPEnabled {
		t.Error("SetTOTPSecret() did not enable TOTP on user")
	}

	if err := a.ClearTOTPSecret(context.Background(), u.ID); err != nil {
		t.Fatalf("ClearTOTPSecret() unexpected error: %v", err)
	}

	secret, err = a.GetTOTPSecret(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetTOTPSecret() after clear unexpected error: %v", err)
	}
	if secret != nil {
		t.Errorf("GetTOTPSecret() after clear = %v, want nil", secret)
	}

	got, err = a.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID() unexpected error: %v", err)
	}
	if got.TOTPEnabled {
		t.Error("ClearTOTPSecret() did not disable TOTP on user")
	}
}

func TestGetTOTPSecret_nonexistent_user_returns_nil(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	secret, err := a.GetTOTPSecret(context.Background(), 99999)
	if err != nil {
		t.Fatalf("GetTOTPSecret(99999) unexpected error: %v", err)
	}
	if secret != nil {
		t.Errorf("GetTOTPSecret(99999) = %v, want nil", secret)
	}
}

func TestSetRecoveryCodes_replaces_existing(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "recuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	hashes1 := []string{"hash-a", "hash-b", "hash-c"}
	if err := a.SetRecoveryCodes(context.Background(), u.ID, hashes1); err != nil {
		t.Fatalf("SetRecoveryCodes(first) unexpected error: %v", err)
	}

	count, err := a.RecoveryCodeCount(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("RecoveryCodeCount() unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("RecoveryCodeCount() = %d, want 3", count)
	}

	hashes2 := []string{"hash-x", "hash-y"}
	if err := a.SetRecoveryCodes(context.Background(), u.ID, hashes2); err != nil {
		t.Fatalf("SetRecoveryCodes(second) unexpected error: %v", err)
	}

	count, err = a.RecoveryCodeCount(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("RecoveryCodeCount() unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("RecoveryCodeCount() after replace = %d, want 2", count)
	}
}

func TestUseRecoveryCode_consumes_matching_code(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "userecuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	hashes := []string{"code-1", "code-2", "code-3"}
	if err := a.SetRecoveryCodes(context.Background(), u.ID, hashes); err != nil {
		t.Fatalf("SetRecoveryCodes() unexpected error: %v", err)
	}

	used, err := a.UseRecoveryCode(context.Background(), u.ID, "code-2")
	if err != nil {
		t.Fatalf("UseRecoveryCode(code-2) unexpected error: %v", err)
	}
	if !used {
		t.Error("UseRecoveryCode(code-2) = false, want true")
	}

	count, err := a.RecoveryCodeCount(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("RecoveryCodeCount() unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("RecoveryCodeCount() after use = %d, want 2", count)
	}

	used, err = a.UseRecoveryCode(context.Background(), u.ID, "code-2")
	if err != nil {
		t.Fatalf("UseRecoveryCode(code-2 again) unexpected error: %v", err)
	}
	if used {
		t.Error("UseRecoveryCode(code-2 again) = true, want false (already used)")
	}
}

func TestUseRecoveryCode_nonexistent_code_returns_false(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "norecuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	used, err := a.UseRecoveryCode(context.Background(), u.ID, "nonexistent")
	if err != nil {
		t.Fatalf("UseRecoveryCode(nonexistent) unexpected error: %v", err)
	}
	if used {
		t.Error("UseRecoveryCode(nonexistent) = true, want false")
	}
}

func TestRecoveryCodeCount_no_codes_returns_zero(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "nocodeuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	count, err := a.RecoveryCodeCount(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("RecoveryCodeCount() unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("RecoveryCodeCount() = %d, want 0", count)
	}
}

func TestOIDCState_create_and_consume(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	if err := a.CreateOIDCState(context.Background(),
		"state-abc", "nonce-xyz", "verifier-123", "/callback"); err != nil {
		t.Fatalf("CreateOIDCState() unexpected error: %v", err)
	}

	nonce, verifier, redirect, err := a.ConsumeOIDCState(context.Background(), "state-abc")
	if err != nil {
		t.Fatalf("ConsumeOIDCState() unexpected error: %v", err)
	}
	if nonce != "nonce-xyz" {
		t.Errorf("ConsumeOIDCState().nonce = %q, want %q", nonce, "nonce-xyz")
	}
	if verifier != "verifier-123" {
		t.Errorf("ConsumeOIDCState().verifier = %q, want %q", verifier, "verifier-123")
	}
	if redirect != "/callback" {
		t.Errorf("ConsumeOIDCState().redirect = %q, want %q", redirect, "/callback")
	}

	_, _, _, err = a.ConsumeOIDCState(context.Background(), "state-abc")
	if err == nil {
		t.Error("ConsumeOIDCState(consumed) expected error, got nil")
	}
}

func TestConsumeOIDCState_nonexistent_returns_error(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	_, _, _, err := a.ConsumeOIDCState(context.Background(), "nonexistent-state")
	if err == nil {
		t.Error("ConsumeOIDCState(nonexistent) expected error, got nil")
	}
}

func TestCleanupExpiredOIDCStates_removes_old_entries(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	now := time.Now().Truncate(time.Second)

	if err := a.CreateOIDCState(context.Background(),
		"old-state", "n", "v", "/"); err != nil {
		t.Fatalf("CreateOIDCState() unexpected error: %v", err)
	}
	if err := a.CreateOIDCState(context.Background(),
		"fresh-state", "n2", "v2", "/fresh"); err != nil {
		t.Fatalf("CreateOIDCState() unexpected error: %v", err)
	}

	oldTime := now.Add(-2 * time.Hour)
	_, err := a.db.ExecContext(context.Background(),
		`UPDATE auth_oidc_states SET created_at = ? WHERE state = 'old-state'`,
		oldTime)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	_, err = a.db.ExecContext(context.Background(),
		`UPDATE auth_oidc_states SET created_at = ? WHERE state = 'fresh-state'`,
		now)
	if err != nil {
		t.Fatalf("set fresh time: %v", err)
	}

	deleted, err := a.CleanupExpiredOIDCStates(context.Background(), now, time.Hour)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates() unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("CleanupExpiredOIDCStates() deleted %d, want 1", deleted)
	}

	nonce, _, _, err := a.ConsumeOIDCState(context.Background(), "fresh-state")
	if err != nil {
		t.Fatalf("ConsumeOIDCState(fresh) unexpected error: %v", err)
	}
	if nonce != "n2" {
		t.Errorf("ConsumeOIDCState(fresh).nonce = %q, want %q", nonce, "n2")
	}
}

func TestCreatePasskey_sets_id(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "pkuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	cred := &api.PasskeyCredential{
		UserID:       u.ID,
		CredentialID: []byte("cred-id-1"),
		PublicKey:    []byte("pub-key-1"),
		AAGUID:       make([]byte, 16),
		Name:         "My YubiKey",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}
	if cred.ID == 0 {
		t.Error("CreatePasskey() did not set ID on struct")
	}
}

func TestGetPasskeysByUserID_returns_ordered_by_creation(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "multipk", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	for _, name := range []string{"first", "second"} {
		cred := &api.PasskeyCredential{
			UserID:       u.ID,
			CredentialID: []byte("cred-" + name),
			PublicKey:    []byte("pub-" + name),
			AAGUID:       make([]byte, 16),
			Name:         name,
		}
		if err := a.CreatePasskey(context.Background(), cred); err != nil {
			t.Fatalf("CreatePasskey(%q) unexpected error: %v", name, err)
		}
	}

	creds, err := a.GetPasskeysByUserID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetPasskeysByUserID() unexpected error: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("GetPasskeysByUserID() returned %d creds, want 2", len(creds))
	}
	if creds[0].Name != "first" {
		t.Errorf("GetPasskeysByUserID()[0].Name = %q, want %q", creds[0].Name, "first")
	}
	if creds[1].Name != "second" {
		t.Errorf("GetPasskeysByUserID()[1].Name = %q, want %q", creds[1].Name, "second")
	}
}

func TestGetPasskeyByCredentialID_returns_passkey(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "crediduser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	credID := []byte("unique-cred-id")
	cred := &api.PasskeyCredential{
		UserID: u.ID, CredentialID: credID,
		PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "test passkey",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}

	got, err := a.GetPasskeyByCredentialID(context.Background(), credID)
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("GetPasskeyByCredentialID() = nil, want passkey")
	}
	if got.Name != "test passkey" {
		t.Errorf("GetPasskeyByCredentialID().Name = %q, want %q", got.Name, "test passkey")
	}
}

func TestGetPasskeyByCredentialID_not_found_returns_nil(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	got, err := a.GetPasskeyByCredentialID(context.Background(), []byte("nonexistent"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetPasskeyByCredentialID(nonexistent) = %v, want nil", got)
	}
}

func TestUpdatePasskeyAfterLogin_updates_sign_count_and_flags(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "loginuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	credID := []byte("login-cred")
	cred := &api.PasskeyCredential{
		UserID: u.ID, CredentialID: credID,
		PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "login key",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}

	flags := api.PasskeyFlags{
		BackupEligible: true,
		BackupState:    true,
		UserPresent:    true,
		UserVerified:   true,
	}
	if err := a.UpdatePasskeyAfterLogin(context.Background(), credID, 42, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin() unexpected error: %v", err)
	}

	got, err := a.GetPasskeyByCredentialID(context.Background(), credID)
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID() unexpected error: %v", err)
	}
	if got.SignCount != 42 {
		t.Errorf("SignCount = %d, want 42", got.SignCount)
	}
	if !got.BackupEligible {
		t.Error("BackupEligible = false, want true")
	}
	if !got.UserVerified {
		t.Error("UserVerified = false, want true")
	}
}

func TestRenamePasskey_updates_name(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "renameuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	cred := &api.PasskeyCredential{
		UserID: u.ID, CredentialID: []byte("rename-cred"),
		PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "old name",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}

	if err := a.RenamePasskey(context.Background(), cred.ID, u.ID, "new name"); err != nil {
		t.Fatalf("RenamePasskey() unexpected error: %v", err)
	}

	got, err := a.GetPasskeyByCredentialID(context.Background(), []byte("rename-cred"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID() unexpected error: %v", err)
	}
	if got.Name != "new name" {
		t.Errorf("RenamePasskey().Name = %q, want %q", got.Name, "new name")
	}
}

func TestDeletePasskey_removes_passkey(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "delpkuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	cred := &api.PasskeyCredential{
		UserID: u.ID, CredentialID: []byte("del-cred"),
		PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "delete me",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}

	if err := a.DeletePasskey(context.Background(), cred.ID, u.ID); err != nil {
		t.Fatalf("DeletePasskey(%d) unexpected error: %v", cred.ID, err)
	}

	got, err := a.GetPasskeyByCredentialID(context.Background(), []byte("del-cred"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetPasskeyByCredentialID() after delete = %v, want nil", got)
	}
}

func TestPasskeyCountForUser_returns_count(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "countpkuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	count, err := a.PasskeyCountForUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("PasskeyCountForUser() unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("PasskeyCountForUser() = %d, want 0", count)
	}

	for i := range 2 {
		cred := &api.PasskeyCredential{
			UserID: u.ID, CredentialID: []byte{byte(i)},
			PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "key",
		}
		if err := a.CreatePasskey(context.Background(), cred); err != nil {
			t.Fatalf("CreatePasskey() unexpected error: %v", err)
		}
	}

	count, err = a.PasskeyCountForUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("PasskeyCountForUser() unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("PasskeyCountForUser() = %d, want 2", count)
	}
}

func TestDeleteUser_cascades_to_sessions_and_passkeys(t *testing.T) {
	t.Parallel()
	a := openTestAuthDB(t)

	u := &api.User{Username: "cascadeuser", Role: "user", Enabled: true}
	if err := a.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser() unexpected error: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	sess := &api.Session{
		TokenHash: "cascade-hash", UserID: u.ID, AuthMethod: "password",
		CreatedAt: now, LastActivity: now,
	}
	if err := a.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession() unexpected error: %v", err)
	}

	cred := &api.PasskeyCredential{
		UserID: u.ID, CredentialID: []byte("cascade-cred"),
		PublicKey: []byte("pub"), AAGUID: make([]byte, 16), Name: "cascade key",
	}
	if err := a.CreatePasskey(context.Background(), cred); err != nil {
		t.Fatalf("CreatePasskey() unexpected error: %v", err)
	}

	key := &api.Key{
		UserID: u.ID, KeyHash: "cascade-key-hash",
		KeyPrefix: "sf_", KeySuffix: "cas", Label: "cascade",
	}
	if err := a.CreateAPIKey(context.Background(), key); err != nil {
		t.Fatalf("CreateAPIKey() unexpected error: %v", err)
	}

	if err := a.DeleteUser(context.Background(), u.ID); err != nil {
		t.Fatalf("DeleteUser() unexpected error: %v", err)
	}

	gotSess, _ := a.GetSessionByHash(context.Background(), "cascade-hash")
	if gotSess != nil {
		t.Error("session not cascaded on user delete")
	}

	gotPK, _ := a.GetPasskeyByCredentialID(context.Background(), []byte("cascade-cred"))
	if gotPK != nil {
		t.Error("passkey not cascaded on user delete")
	}

	gotKey, _ := a.GetAPIKeyByHash(context.Background(), "cascade-key-hash")
	if gotKey != nil {
		t.Error("API key not cascaded on user delete")
	}
}

// --- Benchmarks for per-request hot paths ---

func BenchmarkGetSessionByHash(b *testing.B) {
	ctx := context.Background()
	db, err := sql.Open("sqlite",
		":memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_time_format=sqlite&_texttotime=1")
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, migrations.Schema); err != nil {
		db.Close()
		b.Fatalf("apply schema: %v", err)
	}
	a, err := New(ctx, db)
	if err != nil {
		db.Close()
		b.Fatalf("authdb.New: %v", err)
	}
	defer func() { a.Close(ctx); db.Close() }()

	u := &api.User{Username: "bench", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, u); err != nil {
		b.Fatalf("CreateUser: %v", err)
	}

	// Insert sessions to benchmark against.
	for i := range 100 {
		sess := &api.Session{
			TokenHash:    fmt.Sprintf("hash-%d", i),
			UserID:       u.ID,
			AuthMethod:   "password",
			IPAddress:    "127.0.0.1",
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
		}
		if err := a.CreateSession(ctx, sess); err != nil {
			b.Fatalf("CreateSession: %v", err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = a.GetSessionByHash(ctx, "hash-50")
	}
}

func BenchmarkGetAPIKeyByHash(b *testing.B) {
	ctx := context.Background()
	db, err := sql.Open("sqlite",
		":memory:?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_time_format=sqlite&_texttotime=1")
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, migrations.Schema); err != nil {
		db.Close()
		b.Fatalf("apply schema: %v", err)
	}
	a, err := New(ctx, db)
	if err != nil {
		db.Close()
		b.Fatalf("authdb.New: %v", err)
	}
	defer func() { a.Close(ctx); db.Close() }()

	u := &api.User{Username: "bench-api", Role: "user", Enabled: true}
	if err := a.CreateUser(ctx, u); err != nil {
		b.Fatalf("CreateUser: %v", err)
	}

	// Insert API keys to benchmark against.
	for i := range 100 {
		key := &api.Key{
			UserID:    u.ID,
			KeyHash:   fmt.Sprintf("api-hash-%d", i),
			KeyPrefix: "sf_",
			KeySuffix: fmt.Sprintf("s%d", i),
			Label:     fmt.Sprintf("key-%d", i),
		}
		if err := a.CreateAPIKey(ctx, key); err != nil {
			b.Fatalf("CreateAPIKey: %v", err)
		}
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = a.GetAPIKeyByHash(ctx, "api-hash-50")
	}
}
