package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"go.yaml.in/yaml/v4"
)

// recordingStore records, per Update, the writes its transaction made.
type recordingStore struct {
	*accesstest.MemStore
	mu      sync.Mutex
	updates [][]string
}

func (s *recordingStore) Update(ctx context.Context, fn func(account.Tx) error) error {
	s.mu.Lock()
	s.updates = append(s.updates, nil)
	n := len(s.updates) - 1
	s.mu.Unlock()
	return s.MemStore.Update(ctx, func(tx account.Tx) error { return fn(&recordingTx{Tx: tx, s: s, n: n}) })
}

func (s *recordingStore) record(n int, op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates[n] = append(s.updates[n], op)
}

func (s *recordingStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = nil
}

type recordingTx struct {
	account.Tx
	s *recordingStore
	n int
}

func (t *recordingTx) UpdateUser(ctx context.Context, u *access.User) error {
	t.s.record(t.n, "UpdateUser")
	return t.Tx.UpdateUser(ctx, u)
}

func (t *recordingTx) DeleteUserSessions(ctx context.Context, id int64, except string) ([]string, error) {
	t.s.record(t.n, "DeleteUserSessions")
	return t.Tx.DeleteUserSessions(ctx, id, except)
}

func (t *recordingTx) DeleteAuthFailures(ctx context.Context, name string, methods []access.Method) error {
	t.s.record(t.n, "DeleteAuthFailures")
	return t.Tx.DeleteAuthFailures(ctx, name, methods)
}

// eventService composes a service over a recording MemStore whose account
// events the returned func answers.
func eventService(t *testing.T) (*Service, *recordingStore, func() []account.Event) {
	t.Helper()
	var (
		mu     sync.Mutex
		events []account.Event
	)
	swap(t, &newAccount, func(ctx context.Context, c account.Config) (*account.Service, error) {
		c.OnEvent = func(e account.Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		}
		return account.New(ctx, c)
	})
	store := &recordingStore{MemStore: accesstest.NewMemStore()}
	d := newEnv().deps()
	s, err := compose(t.Context(), &d, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(context.Background()) })
	return s, store, func() []account.Event {
		mu.Lock()
		defer mu.Unlock()
		out := slices.Clone(events)
		events = nil
		return out
	}
}

// actedAsSocket fails unless events holds kind with the admin socket as its
// actor.
func actedAsSocket(t *testing.T, events []account.Event, kind account.EventKind) {
	t.Helper()
	for _, e := range events {
		if e.Kind == kind {
			if e.Actor.OutOfBand != adminSocket {
				t.Errorf("%s actor = %+v, want the %s channel", kind, e.Actor, adminSocket)
			}
			return
		}
	}
	t.Errorf("no %s event; got %v", kind, events)
}

func authenticates(s *Service, with func(*http.Request)) bool {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8374/api/session", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	with(r)
	_, err := s.authn.Authenticate(r)
	return err == nil
}

func TestCommandsActAsAdminSocket(t *testing.T) {
	const newPassword = "an entirely different passphrase 7"
	t.Run("reset-password revokes sessions and keys and clears locks in one transaction", func(t *testing.T) {
		s, store, events := eventService(t)
		createUser(t, s, "alice", access.RoleAdmin, testPassword)
		in, err := s.account.PasswordLogin(t.Context(), clientAt("127.0.0.1"), account.Credentials{Username: "alice", Password: access.NewSecret(testPassword)})
		if err != nil {
			t.Fatal(err)
		}
		key, err := s.CreateKey(t.Context(), "alice", "ci", nil)
		if err != nil {
			t.Fatal(err)
		}
		cfg := s.authn.Cookie()
		cookie := func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cfg.CookieName(r), Value: in.Token.Reveal()})
		}
		keyed := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }
		if !authenticates(s, cookie) || !authenticates(s, keyed) {
			t.Fatal("the session or key does not authenticate before the reset")
		}
		store.reset()
		events()
		if err := s.ResetPassword(t.Context(), "Alice", newPassword); err != nil {
			t.Fatal(err)
		}
		if len(store.updates) != 1 {
			t.Fatalf("ResetPassword ran %d Updates, want 1", len(store.updates))
		}
		for _, op := range []string{"UpdateUser", "DeleteUserSessions", "DeleteAuthFailures"} {
			if !slices.Contains(store.updates[0], op) {
				t.Errorf("the reset's Update made no %s; it made %v", op, store.updates[0])
			}
		}
		if authenticates(s, cookie) {
			t.Error("the session still authenticates after the reset")
		}
		if authenticates(s, keyed) {
			t.Error("the key still authenticates after the reset")
		}
		ev := events()
		actedAsSocket(t, ev, account.EventRecoveryCompleted)
		actedAsSocket(t, ev, account.EventKeyRevoked)
		if _, err := s.account.PasswordLogin(t.Context(), clientAt("127.0.0.1"), account.Credentials{Username: "alice", Password: access.NewSecret(newPassword)}); err != nil {
			t.Errorf("the new password does not sign in: %v", err)
		}
	})
	t.Run("recovery-link prints a fragment link", func(t *testing.T) {
		s, _, events := eventService(t)
		createUser(t, s, "bob", access.RoleUser, testPassword)
		link, err := s.RecoveryLink(t.Context(), "BOB")
		if err != nil {
			t.Fatal(err)
		}
		tok, ok := strings.CutPrefix(link, "/recover#")
		if !ok || tok == "" {
			t.Fatalf("link = %q, want /recover#<token>", link)
		}
		actedAsSocket(t, events(), account.EventRecoveryIssued)
		if _, err := s.account.StartRecovery(t.Context(), clientAt("127.0.0.1"), access.NewSecret(tok)); err != nil {
			t.Errorf("the link's token does not start a recovery: %v", err)
		}
	})
	t.Run("create-user normalises the name and takes the role", func(t *testing.T) {
		s, store, events := eventService(t)
		if err := s.CreateUser(t.Context(), "Carol", testPassword, "admin"); err != nil {
			t.Fatal(err)
		}
		actedAsSocket(t, events(), account.EventUserCreated)
		u, ok, err := store.UserByUsername(t.Context(), "carol")
		if err != nil || !ok {
			t.Fatalf("carol not stored: %v", err)
		}
		if u.Role != access.RoleAdmin {
			t.Errorf("role = %q, want admin", u.Role)
		}
		if err := s.CreateUser(t.Context(), "dave", testPassword, ""); err != nil {
			t.Fatal(err)
		}
		if d, _, _ := store.UserByUsername(t.Context(), "dave"); d == nil || d.Role != access.RoleUser {
			t.Errorf("an empty role did not create a regular user: %+v", d)
		}
		if err := s.CreateUser(t.Context(), "erin", testPassword, "owner"); !errors.Is(err, ErrUnknownRole) {
			t.Errorf("role owner: %v, want ErrUnknownRole", err)
		}
		if err := s.CreateUser(t.Context(), "frank", "short", ""); err == nil {
			t.Error("a password the HTTP plane refuses was accepted")
		}
	})
	t.Run("create-key answers the key once, under the configured scopes", func(t *testing.T) {
		s, _, events := eventService(t)
		createUser(t, s, "alice", access.RoleAdmin, testPassword)
		key, err := s.CreateKey(t.Context(), "alice", "backup", []string{scopeRead})
		if err != nil {
			t.Fatal(err)
		}
		actedAsSocket(t, events(), account.EventKeyMinted)
		if p := bearer(t, s, access.NewSecret(key)); !slices.Equal(p.Scopes, []string{scopeRead}) {
			t.Errorf("key scopes = %v, want [read]", p.Scopes)
		}
		if _, err := s.CreateKey(t.Context(), "alice", "x", []string{"everything"}); !errors.Is(err, account.ErrUnknownScope) {
			t.Errorf("unknown scope: %v, want ErrUnknownScope", err)
		}
	})
	t.Run("an unknown user is not found", func(t *testing.T) {
		s, _, _ := eventService(t)
		for name, call := range map[string]func() error{
			"reset-password": func() error { return s.ResetPassword(t.Context(), "nobody", newPassword) },
			"recovery-link":  func() error { _, err := s.RecoveryLink(t.Context(), "nobody"); return err },
			"create-key":     func() error { _, err := s.CreateKey(t.Context(), "nobody", "", nil); return err },
		} {
			if err := call(); !errors.Is(err, account.ErrNotFound) {
				t.Errorf("%s: %v, want account.ErrNotFound", name, err)
			}
		}
		if err := s.ResetPassword(t.Context(), " \x00", newPassword); !errors.Is(err, access.ErrUsernameInvalid) && !errors.Is(err, access.ErrUsernameEmpty) {
			t.Errorf("an invalid username: %v, want the username rule's error", err)
		}
	})
}

func TestSetupTokenRefusedWithUsers(t *testing.T) {
	s, _, events := eventService(t)
	tok, err := s.SetupToken(t.Context())
	if err != nil || tok == "" {
		t.Fatalf("SetupToken with no user = %q, %v", tok, err)
	}
	actedAsSocket(t, events(), account.EventSetupTokenIssued)
	createUser(t, s, "alice", access.RoleAdmin, testPassword)
	if _, err := s.SetupToken(t.Context()); !errors.Is(err, account.ErrSetupComplete) {
		t.Errorf("SetupToken with a user: %v, want ErrSetupComplete", err)
	}
}

func TestWithPasswordLogin(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no auth section", "server:\n  log_level: info\n", "server:\n    log_level: info\nauth:\n    password_login: true\n"},
		{"auth without the key", "auth:\n  oidc_enabled: true\n", "auth:\n    oidc_enabled: true\n    password_login: true\n"},
		{"the key off", "auth:\n  password_login: false\n  oidc_enabled: true\n", "auth:\n    password_login: true\n    oidc_enabled: true\n"},
		{"the key unchanged-tagged", "auth:\n  password_login: !unchanged\n", "auth:\n    password_login: true\n"},
		{"auth not a mapping", "auth: [1]\n", "auth: [1]\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(c.in), &doc); err != nil {
				t.Fatal(err)
			}
			out, err := yaml.Marshal(WithPasswordLogin(doc.Content[0]))
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != c.want {
				t.Errorf("got\n%s\nwant\n%s", out, c.want)
			}
		})
	}
}
