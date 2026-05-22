package api

import (
	"context"
	"testing"

	"pgregory.net/rapid"
)

func TestNewUserContext_round_trip(t *testing.T) {
	t.Parallel()

	user := &User{ID: 42, Username: "testuser", Role: "admin"}
	ctx := NewUserContext(context.Background(), user)

	got := UserFromContext(ctx)

	if got == nil {
		t.Fatal("UserFromContext() = nil, want non-nil")
	}
	if got.ID != 42 {
		t.Errorf("UserFromContext().ID = %d, want 42", got.ID)
	}
	if got.Username != "testuser" {
		t.Errorf("UserFromContext().Username = %q, want %q", got.Username, "testuser")
	}
}

func TestUserFromContext_returns_nil_for_empty_context(t *testing.T) {
	t.Parallel()

	got := UserFromContext(context.Background())

	if got != nil {
		t.Errorf("UserFromContext(empty) = %v, want nil", got)
	}
}

func TestUserFromContext_returns_nil_for_wrong_type(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), userContextKeyT{}, "not a user")

	got := UserFromContext(ctx)

	if got != nil {
		t.Errorf("UserFromContext(wrong type) = %v, want nil", got)
	}
}

func TestNewUserContext_nil_user_round_trip(t *testing.T) {
	t.Parallel()

	ctx := NewUserContext(context.Background(), nil)

	got := UserFromContext(ctx)

	if got != nil {
		t.Errorf("UserFromContext(nil user) = %v, want nil", got)
	}
}

func TestUserFromContext_round_trip_preserves_identity(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.Int64Range(1, 999999).Draw(t, "id")
		username := rapid.StringMatching(`[a-z]{3,20}`).Draw(t, "username")
		role := rapid.SampledFrom([]string{"admin", "user", "viewer"}).Draw(t, "role")

		user := &User{ID: id, Username: username, Role: Role(role)}
		ctx := NewUserContext(context.Background(), user)
		got := UserFromContext(ctx)

		if got == nil {
			t.Fatal("UserFromContext() = nil after NewUserContext")
			return
		}
		if got.ID != id {
			t.Errorf("ID = %d, want %d", got.ID, id)
		}
		if got.Username != username {
			t.Errorf("Username = %q, want %q", got.Username, username)
		}
		if got.Role != Role(role) {
			t.Errorf("Role = %q, want %q", got.Role, role)
		}
	})
}

func TestSessionHashFromContext_roundtrip(t *testing.T) {
	t.Parallel()

	const hash = "deadbeefcafe"
	ctx := NewSessionHashContext(context.Background(), hash)
	if got := SessionHashFromContext(ctx); got != hash {
		t.Errorf("SessionHashFromContext = %q, want %q", got, hash)
	}
}

func TestSessionHashFromContext_empty_when_absent(t *testing.T) {
	t.Parallel()

	if got := SessionHashFromContext(context.Background()); got != "" {
		t.Errorf("SessionHashFromContext(empty ctx) = %q, want \"\"", got)
	}
}

func TestSessionHashFromContext_distinct_from_user_key(t *testing.T) {
	t.Parallel()

	// A user-valued context must not leak into the session-hash key.
	ctx := NewUserContext(context.Background(), &User{ID: 1, Username: "u"})
	if got := SessionHashFromContext(ctx); got != "" {
		t.Errorf("SessionHashFromContext on user-only ctx = %q, want \"\"", got)
	}

	// And vice versa: a session-hash context must not expose a user.
	ctx = NewSessionHashContext(context.Background(), "hash")
	if got := UserFromContext(ctx); got != nil {
		t.Errorf("UserFromContext on sesshash-only ctx = %v, want nil", got)
	}
}
