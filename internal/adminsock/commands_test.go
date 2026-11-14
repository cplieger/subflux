package adminsock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/access/account"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

func TestCommandWords(t *testing.T) {
	want := []string{"reset-password", "recovery-link", "create-user", "create-key", "setup-token", "enable-password-login", "store"}
	if got := Words(); !slices.Equal(got, want) {
		t.Errorf("Words() = %v, want %v", got, want)
	}
	for _, w := range want {
		var c Command
		if err := c.UnmarshalText([]byte(w)); err != nil {
			t.Errorf("UnmarshalText(%q): %v", w, err)
			continue
		}
		b, err := json.Marshal(c)
		if err != nil || string(b) != `"`+w+`"` {
			t.Errorf("%q marshals as %s, %v", w, b, err)
		}
	}
	for _, c := range []Command{0, Store + 1} {
		if _, err := c.MarshalText(); !errors.Is(err, errUnknownCommand) {
			t.Errorf("Command(%d).MarshalText: %v, want errUnknownCommand", c, err)
		}
	}
	var c Command
	if err := c.UnmarshalText([]byte("drop-tables")); !errors.Is(err, errUnknownCommand) {
		t.Errorf("an unknown word: %v, want errUnknownCommand", err)
	}
	if err := c.UnmarshalText(nil); !errors.Is(err, errUnknownCommand) {
		t.Errorf("an empty word: %v, want errUnknownCommand", err)
	}
}

// fakes records each call the socket makes and answers what it is set to.
type fakes struct {
	err     error
	login   PasswordLogin
	calls   []string
	options []statedb.InspectOptions
	mu      sync.Mutex
}

func (f *fakes) record(format string, a ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
}

// take answers the calls and options recorded since the last take.
func (f *fakes) take() ([]string, []statedb.InspectOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls, opts := f.calls, f.options
	f.calls, f.options = nil, nil
	return calls, opts
}

func (f *fakes) ResetPassword(_ context.Context, u, p string) error {
	return f.record("reset %s %s", u, p)
}

func (f *fakes) RecoveryLink(_ context.Context, u string) (string, error) {
	return "/recover#tok", f.record("link %s", u)
}

func (f *fakes) CreateUser(_ context.Context, u, p, r string) error {
	return f.record("create %s %s %s", u, p, r)
}

func (f *fakes) CreateKey(_ context.Context, u, l string, s []string) (string, error) {
	return "sfx_key", f.record("key %s %s %v", u, l, s)
}

func (f *fakes) SetupToken(context.Context) (string, error) { return "setup", f.record("setup") }

func (f *fakes) EnablePasswordLogin(context.Context) (PasswordLogin, error) {
	return f.login, f.record("enable")
}

func (f *fakes) Inspect(_ context.Context, o statedb.InspectOptions) (statedb.Inspection, error) {
	f.mu.Lock()
	f.options = append(f.options, o)
	f.mu.Unlock()
	return statedb.Inspection{Rev: 7, FileBytes: 4096}, f.err
}

// served serves a Server over f on a socket, answering its path and the
// log it writes.
func served(t *testing.T, f *fakes) (string, *capture.Recorder) {
	t.Helper()
	logger, rec := capture.New()
	dir, path := sockPaths(t)
	ln := listens(t, dir, path)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(Deps{Accounts: f, Settings: f, Store: f, Logger: logger}).Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path, rec
}

// logged is every captured record rendered as text.
func logged(rec *capture.Recorder) string {
	var b strings.Builder
	for _, r := range rec.Records() {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

func TestServerRunsEachCommand(t *testing.T) {
	const password = "hunter2 is not a passphrase"
	f := &fakes{login: PasswordLogin{Fields: []formschema.FieldError{{Path: "auth.oidc_enabled", Message: "would leave 1 user without a method"}}}}
	path, rec := served(t, f)
	cases := []struct {
		req       Request
		call      string
		secret    string
		wantField bool
	}{
		{Request{Command: ResetPassword, Username: "alice", Password: password}, "reset alice " + password, "", false},
		{Request{Command: RecoveryLink, Username: "alice"}, "link alice", "/recover#tok", false},
		{Request{Command: CreateUser, Username: "bob", Password: password, Role: "admin"}, "create bob " + password + " admin", "", false},
		{Request{Command: CreateKey, Username: "bob", Label: "ci", Scopes: []string{"read"}}, "key bob ci [read]", "sfx_key", false},
		{Request{Command: SetupToken}, "setup", "setup", false},
		{Request{Command: EnablePasswordLogin}, "enable", "", true},
	}
	for _, c := range cases {
		t.Run(c.req.Command.String(), func(t *testing.T) {
			reply, err := Call(t.Context(), path, &c.req)
			if err != nil {
				t.Fatal(err)
			}
			if calls, _ := f.take(); !slices.Equal(calls, []string{c.call}) {
				t.Errorf("calls = %q, want %q", calls, c.call)
			}
			if reply.Secret != c.secret {
				t.Errorf("secret = %q, want %q", reply.Secret, c.secret)
			}
			if got := len(reply.Fields) == 1; got != c.wantField {
				t.Errorf("fields = %v", reply.Fields)
			}
		})
	}
	if strings.Contains(logged(rec), password) {
		t.Errorf("a password reached the log:\n%s", logged(rec))
	}
}

func TestServerStorePassesFlags(t *testing.T) {
	f := &fakes{}
	path, _ := served(t, f)
	for _, o := range []statedb.InspectOptions{{}, {Verify: true}, {Verify: true, Repair: true}} {
		reply, err := Call(t.Context(), path, &Request{Command: Store, Verify: o.Verify, Repair: o.Repair})
		if err != nil {
			t.Fatal(err)
		}
		if reply.Inspection == nil || reply.Inspection.Rev != 7 {
			t.Errorf("%+v: inspection = %+v", o, reply.Inspection)
		}
	}
	if _, opts := f.take(); !slices.Equal(opts, []statedb.InspectOptions{{}, {Verify: true}, {Verify: true, Repair: true}}) {
		t.Errorf("options = %+v, want none, verify, verify and repair", opts)
	}
	_, err := Call(t.Context(), path, &Request{Command: Store, Repair: true})
	if e, ok := errors.AsType[*Error](err); !ok || e.Code != "bad_request" {
		t.Errorf("repair without verify: %v, want bad_request", err)
	}
	if _, opts := f.take(); len(opts) != 0 {
		t.Errorf("repair without verify reached Inspect with %+v", opts)
	}
}

func TestServerRefusals(t *testing.T) {
	t.Run("an unknown user is a JSON not_found error", func(t *testing.T) {
		path, _ := served(t, &fakes{err: account.ErrNotFound})
		_, err := Call(t.Context(), path, &Request{Command: RecoveryLink, Username: "nobody"})
		if e, ok := errors.AsType[*Error](err); !ok || e.Code != "not_found" || e.Message == "" {
			t.Errorf("Call: %v, want a not_found Error", err)
		}
	})
	t.Run("a backend failure is internal and logged", func(t *testing.T) {
		path, rec := served(t, &fakes{err: errors.New("disk on fire")})
		_, err := Call(t.Context(), path, &Request{Command: SetupToken})
		if e, ok := errors.AsType[*Error](err); !ok || e.Code != "internal" || strings.Contains(e.Message, "disk") {
			t.Errorf("Call: %v, want an internal Error naming no cause", err)
		}
		if !rec.AttrContains("adminsock: command failed", "error", "disk on fire") {
			t.Error("the cause was not logged")
		}
	})
	t.Run("a store refusal is answered with its text", func(t *testing.T) {
		path, rec := served(t, &fakes{err: errors.New("statedb: inspect: no activation in force")})
		_, err := Call(t.Context(), path, &Request{Command: Store, Verify: true})
		if e, ok := errors.AsType[*Error](err); !ok || e.Code != "inspect_failed" || !strings.Contains(e.Message, "no activation") {
			t.Errorf("Call: %v, want inspect_failed naming the cause", err)
		}
		if rec.CountLevel(slog.LevelError, "adminsock: command refused") != 1 {
			t.Error("the store's refusal was not logged at ERROR")
		}
	})
	h := New(Deps{Accounts: &fakes{}, Settings: &fakes{}, Store: &fakes{}, Logger: slog.New(slog.DiscardHandler)}).handler()
	for _, c := range []struct{ name, body, code string }{
		{"an unknown command", `{"command":"drop-tables"}`, "bad_request"},
		{"no command", `{"username":"alice"}`, "unknown_command"},
		{"an unknown field", `{"command":"setup-token","admin":true}`, "bad_request"},
		{"two objects", `{"command":"setup-token"}{"command":"setup-token"}`, "bad_request"},
		{"a body over 4 KiB", `{"command":"create-key","username":"alice","label":"` + strings.Repeat("x", 4<<10) + `"}`, "body_too_large"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body)))
			var e Error
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Code != c.code {
				t.Errorf("status %d body %s, want code %s", w.Code, w.Body, c.code)
			}
		})
	}
	t.Run("a body at 4 KiB is read", func(t *testing.T) {
		f := &fakes{}
		h := New(Deps{Accounts: f, Settings: f, Store: f, Logger: slog.New(slog.DiscardHandler)}).handler()
		head := `{"command":"create-key","username":"alice","label":"`
		body := head + strings.Repeat("x", 4<<10-len(head)-2) + `"}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if w.Code != http.StatusOK || len(f.calls) != 1 {
			t.Errorf("status %d calls %d, want 200 and one call", w.Code, len(f.calls))
		}
	})
}
