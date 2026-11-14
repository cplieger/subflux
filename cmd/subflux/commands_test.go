package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/access/account"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/statedb"
)

// socketFake answers the admin socket's commands and records each call.
type socketFake struct {
	err     error
	login   adminsock.PasswordLogin
	calls   []string
	options []statedb.InspectOptions
	mu      sync.Mutex
}

func (f *socketFake) record(format string, a ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
}

func (f *socketFake) take() ([]string, []statedb.InspectOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls, opts := f.calls, f.options
	f.calls, f.options = nil, nil
	return calls, opts
}

func (f *socketFake) ResetPassword(_ context.Context, u, p string) error {
	return f.record("reset %s %q", u, p)
}

func (f *socketFake) RecoveryLink(_ context.Context, u string) (string, error) {
	return "/recover#tok", f.record("link %s", u)
}

func (f *socketFake) CreateUser(_ context.Context, u, p, r string) error {
	return f.record("create %s %q %s", u, p, r)
}

func (f *socketFake) CreateKey(_ context.Context, u, l string, s []string) (string, error) {
	return "sfx_key", f.record("key %s %s %v", u, l, s)
}

func (f *socketFake) SetupToken(context.Context) (string, error) {
	return "setup-tok", f.record("setup")
}

func (f *socketFake) EnablePasswordLogin(context.Context) (adminsock.PasswordLogin, error) {
	return f.login, f.record("enable")
}

func (f *socketFake) Inspect(_ context.Context, o statedb.InspectOptions) (statedb.Inspection, error) {
	f.mu.Lock()
	f.options = append(f.options, o)
	f.mu.Unlock()
	return statedb.Inspection{Rev: 9, Commits: 3, FileBytes: 8192, Mismatches: []statedb.Mismatch{{Name: "media.items", Missing: 1}}}, f.err
}

// serveSocket serves d on a socket the CLI's paths point at.
func serveSocket(t *testing.T, d adminsock.Deps) {
	t.Helper()
	root, err := os.MkdirTemp("", "cl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "d")
	sock := filepath.Join(dir, "s.sock")
	ln, err := adminsock.Listen(t.Context(), dir, sock)
	if err != nil {
		t.Fatal(err)
	}
	d.Logger = slog.New(slog.DiscardHandler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adminsock.New(d).Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	prev := paths
	paths.Socket = sock
	t.Cleanup(func() { paths = prev })
}

func fakeSocket(t *testing.T, f *socketFake) {
	t.Helper()
	serveSocket(t, adminsock.Deps{Accounts: f, Settings: f, Store: f})
}

// cli runs args with stdin and answers the exit code, stdout and stderr.
func cli(t *testing.T, stdin io.Reader, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(args, stdin, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// jsonError is the CLI's one-line stderr refusal.
func jsonError(t *testing.T, stderr string) adminsock.Error {
	t.Helper()
	var e adminsock.Error
	if err := json.Unmarshal([]byte(stderr), &e); err != nil || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr %q is not one JSON error line", stderr)
	}
	return e
}

func terminal(t *testing.T, typed ...string) *int {
	t.Helper()
	reads := 0
	prevT, prevR := isTerminal, readPassword
	isTerminal = func(io.Reader) bool { return true }
	readPassword = func(io.Reader) ([]byte, error) {
		reads++
		return []byte(typed[reads-1]), nil
	}
	t.Cleanup(func() { isTerminal, readPassword = prevT, prevR })
	return &reads
}

func TestResetPasswordPrompts(t *testing.T) {
	t.Run("twice on a terminal", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		reads := terminal(t, "a long new passphrase", "a long new passphrase")
		code, out, errOut := cli(t, nil, "reset-password", "alice")
		if code != 0 || *reads != 2 {
			t.Fatalf("exit %d after %d reads, stderr %q", code, *reads, errOut)
		}
		if calls, _ := f.take(); !slices.Equal(calls, []string{`reset alice "a long new passphrase"`}) {
			t.Errorf("calls = %q", calls)
		}
		if strings.Count(errOut, "password") != 1 || strings.Contains(errOut+out, "passphrase") {
			t.Errorf("prompts %q, out %q: want one prompt per read and no echo", errOut, out)
		}
	})
	t.Run("two different passwords send nothing", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		terminal(t, "first passphrase", "second passphrase")
		code, _, errOut := cli(t, nil, "reset-password", "alice")
		if code != 1 || !strings.Contains(errOut, "differ") {
			t.Errorf("exit %d, stderr %q; want 1 naming the mismatch", code, errOut)
		}
		if calls, _ := f.take(); len(calls) != 0 {
			t.Errorf("a mismatch reached the socket: %q", calls)
		}
	})
	t.Run("once from piped stdin", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		code, _, errOut := cli(t, strings.NewReader("piped passphrase\r\nignored\n"), "create-user", "bob", "--role", "admin")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		if calls, _ := f.take(); !slices.Equal(calls, []string{`create bob "piped passphrase" admin`}) {
			t.Errorf("calls = %q", calls)
		}
		if errOut != "" {
			t.Errorf("piped stdin prompted: %q", errOut)
		}
	})
	t.Run("a pipe at the bound is sent whole", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		pw := strings.Repeat("p", maxPasswordBytes)
		if code, _, errOut := cli(t, strings.NewReader(pw+"\r\n"), "reset-password", "alice"); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		if calls, _ := f.take(); !slices.Equal(calls, []string{fmt.Sprintf("reset alice %q", pw)}) {
			t.Errorf("calls = %q, want the %d-byte password whole", calls, maxPasswordBytes)
		}
	})
	t.Run("a pipe over the bound sends nothing", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		code, _, errOut := cli(t, strings.NewReader(strings.Repeat("p", maxPasswordBytes+1)+"\n"), "reset-password", "alice")
		if code != 1 || !strings.Contains(errOut, fmt.Sprintf("over %d bytes", maxPasswordBytes)) {
			t.Errorf("exit %d, stderr %q; want 1 naming the bound", code, errOut)
		}
		if calls, _ := f.take(); len(calls) != 0 {
			t.Errorf("a password over the bound reached the socket: %q", calls)
		}
	})
	t.Run("a pipe filling the read with CR bytes sends nothing", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		in := strings.Repeat("a", 20) + strings.Repeat("\r", 2000) + "SECRET\n"
		code, _, errOut := cli(t, strings.NewReader(in), "reset-password", "alice")
		if code != 1 || !strings.Contains(errOut, fmt.Sprintf("over %d bytes", maxPasswordBytes)) {
			t.Errorf("exit %d, stderr %q; want 1 naming the bound", code, errOut)
		}
		if calls, _ := f.take(); len(calls) != 0 {
			t.Errorf("a truncated password reached the socket: %q", calls)
		}
	})
	t.Run("an empty pipe sends nothing", func(t *testing.T) {
		f := &socketFake{}
		fakeSocket(t, f)
		if code, _, _ := cli(t, strings.NewReader(""), "reset-password", "alice"); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if calls, _ := f.take(); len(calls) != 0 {
			t.Errorf("an empty password reached the socket: %q", calls)
		}
	})
}

func TestCommandsReachTheSocket(t *testing.T) {
	f := &socketFake{login: adminsock.PasswordLogin{Fields: []formschema.FieldError{{Path: "languages", Message: "must be a mapping"}}}}
	fakeSocket(t, f)
	for _, c := range []struct {
		args       []string
		call, out  string
		code       int
		errContain string
	}{
		{[]string{"recovery-link", "alice"}, "link alice", "/recover#tok\n", 0, ""},
		{[]string{"create-key", "alice", "--label", "ci", "--scope", "read", "--scope", "operate"}, "key alice ci [read operate]", "sfx_key\n", 0, ""},
		{[]string{"create-user", "carol"}, `create carol "pw" user`, "created carol\n", 0, ""},
		{[]string{"setup-token"}, "setup", "setup-tok\n", 0, ""},
		{[]string{"enable-password-login"}, "enable", "", 1, "  languages: must be a mapping\n"},
	} {
		t.Run(c.args[0], func(t *testing.T) {
			code, out, errOut := cli(t, strings.NewReader("pw\n"), c.args...)
			if code != c.code || out != c.out || !strings.Contains(errOut, c.errContain) {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d, %q, %q", code, out, errOut, c.code, c.out, c.errContain)
			}
			if calls, _ := f.take(); !slices.Equal(calls, []string{c.call}) {
				t.Errorf("calls = %q, want %q", calls, c.call)
			}
		})
	}
	t.Run("enable-password-login with no file", func(t *testing.T) {
		f.login = adminsock.PasswordLogin{AlreadyOn: true}
		code, out, _ := cli(t, nil, "enable-password-login")
		if code != 0 || !strings.Contains(out, "already on") {
			t.Errorf("exit %d, stdout %q", code, out)
		}
		f.take()
	})
	t.Run("an unknown user is a JSON error and exit 1", func(t *testing.T) {
		f.err = account.ErrNotFound
		defer func() { f.err = nil }()
		code, out, errOut := cli(t, nil, "recovery-link", "nobody")
		if e := jsonError(t, errOut); code != 1 || e.Code != "not_found" || out != "" {
			t.Errorf("exit %d, stdout %q, error %+v", code, out, e)
		}
		f.take()
	})
}

func TestCommandsWithoutSocketFail(t *testing.T) {
	prev := paths
	paths.Socket = filepath.Join(t.TempDir(), "absent.sock")
	t.Cleanup(func() { paths = prev })
	code, _, errOut := cli(t, nil, "setup-token")
	if e := jsonError(t, errOut); code != 1 || !strings.Contains(e.Message, "is subflux running") {
		t.Errorf("exit %d, error %+v", code, e)
	}
}

func TestEachWordDispatches(t *testing.T) {
	for _, w := range adminsock.Words() {
		code, out, errOut := cli(t, nil, w, "--no-such-flag")
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: subflux "+w) {
			t.Errorf("%s --no-such-flag: exit %d, stdout %q, stderr %q; want 2 with its own usage", w, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"reset-password"}, {"create-key"}, {"recovery-link", "a", "b"}, {"setup-token", "x"}} {
		if code, _, _ := cli(t, nil, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}

func TestStoreCommandPassesFlags(t *testing.T) {
	f := &socketFake{}
	fakeSocket(t, f)
	for _, c := range []struct {
		args []string
		want statedb.InspectOptions
	}{
		{[]string{"store"}, statedb.InspectOptions{}},
		{[]string{"store", "--verify"}, statedb.InspectOptions{Verify: true}},
		{[]string{"store", "--verify", "--repair"}, statedb.InspectOptions{Verify: true, Repair: true}},
	} {
		code, out, errOut := cli(t, nil, c.args...)
		if code != 0 {
			t.Fatalf("%q: exit %d, stderr %q", c.args, code, errOut)
		}
		if _, opts := f.take(); !slices.Equal(opts, []statedb.InspectOptions{c.want}) {
			t.Errorf("%q passed %+v, want %+v", c.args, opts, c.want)
		}
		var got statedb.Inspection
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q printed %q: %v", c.args, out, err)
		}
		if got.Rev != 9 || got.Commits != 3 || got.FileBytes != 8192 || len(got.Mismatches) != 1 || got.Mismatches[0].Missing != 1 {
			t.Errorf("%q printed %+v, want the fake's inspection", c.args, got)
		}
		var fields map[string]any
		_ = json.Unmarshal([]byte(out), &fields)
		for k := range fields {
			if strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
				t.Errorf("%q printed the field %s", c.args, k)
			}
		}
	}
	code, _, errOut := cli(t, nil, "store", "--repair")
	if code != 2 || !strings.Contains(errOut, "usage: subflux store") {
		t.Errorf("store --repair: exit %d, stderr %q; want a usage error", code, errOut)
	}
	if _, opts := f.take(); len(opts) != 0 {
		t.Errorf("store --repair reached the socket with %+v", opts)
	}
}

func TestStoreCommandPrintsInspection(t *testing.T) {
	st, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	serveSocket(t, adminsock.Deps{Store: st})
	code, out, errOut := cli(t, nil, "store")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var got statedb.Inspection
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("printed %q: %v", out, err)
	}
	if len(got.Namespaces) == 0 || len(got.Namespaces[0].Tables) == 0 || got.FileBytes == 0 || got.Verified {
		t.Errorf("printed %+v, want the store's namespaces, tables and size, unverified", got)
	}
}
