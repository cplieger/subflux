package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/account"
	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

const (
	adminPassword = "correct horse battery staple 42"
	issuerA       = "https://idp.example/a"
	oidcA         = "  oidc_enabled: true\n  oidc_issuer: " + issuerA + "\n  oidc_client_id: subflux\n  oidc_client_secret: s3cret\n  oidc_redirect_url: https://nas.example/api/auth/oidc/callback\n"
	stranded      = "would leave 1 user with no way to sign in"
	publicPeer    = "203.0.113.7"
)

// signInSite is a booted Run with its account routes mounted on its mux, so
// a test serves a password sign-in from a peer it names, and with the store,
// the account service and whether the media sources ever built a scanner.
type signInSite struct {
	*booted
	store     *statedb.Store
	accts     *accounts.Service
	mux       *http.ServeMux
	loginPath string
	routesErr error
	scanned   atomic.Bool
	ready     chan struct{}
}

// bootSite boots text over a store seed prepares (nothing when nil).
func bootSite(t *testing.T, text string, seed func(t *testing.T, storePath string)) *signInSite {
	t.Helper()
	s := &signInSite{ready: make(chan struct{})}
	s.booted = boot(t, text, func(cfg *Config) {
		if seed != nil {
			seed(t, cfg.Paths.Store)
		}
		cfg.Store = func(st *statedb.Store) { s.store = st }
		cfg.Accounts = func(a *accounts.Service) { s.accts = a }
		cfg.Scanner = func(*scan.Scanner) { s.scanned.Store(true) }
		cfg.Routes = func(mux *http.ServeMux) {
			defer close(s.ready)
			routes, err := s.accts.Routes(writeAuthError)
			s.mux, s.routesErr = mux, err
			for _, r := range routes {
				mux.Handle(r.Method+" "+r.Path, r.Handler)
				if r.Name == "authLoginPassword" {
					s.loginPath = r.Path
				}
			}
		}
	})
	<-s.ready
	if s.routesErr != nil || s.loginPath == "" {
		t.Fatalf("mounting the account routes: %v, password sign-in at %q", s.routesErr, s.loginPath)
	}
	return s
}

func writeAuthError(w http.ResponseWriter, _ *http.Request, e accesshttp.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{string(e.Code)})
}

// login signs name in with adminPassword from peer, answering the status
// and, on a refusal, its code.
func (s *signInSite) login(t *testing.T, peer, name string) (int, string) {
	t.Helper()
	body, err := json.Marshal(accesshttp.LoginRequest{Username: name, Password: adminPassword})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://nas.example"+s.loginPath, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://nas.example")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.RemoteAddr = net.JoinHostPort(peer, "40000")
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	var answer struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &answer)
	return w.Code, answer.Code
}

func (s *signInSite) record(t *testing.T) (statedb.AuthMethodState, bool) {
	t.Helper()
	rec, ok, err := statedb.MethodStateOf(t.Context(), s.store.Accounts())
	if err != nil {
		t.Fatal(err)
	}
	return rec, ok
}

// failedWith checks the boot is Failed with exactly want.
func (s *signInSite) failedWith(t *testing.T, want ...formschema.FieldError) {
	t.Helper()
	snap := s.svc.Snapshot()
	got, _ := snap.Failure()
	if snap.Mode() != settings.Failed || !slices.Equal(got, want) {
		t.Errorf("boot = %v %v, want Failed %v", snap.Mode(), got, want)
	}
}

// authOf is text's auth section, as Load parses it.
func authOf(t *testing.T, text string) accounts.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	c, ok := settings.Load(path, registries(providers.New(), filepath.Join(t.TempDir(), "subflux.bolt"))).Auth()
	if !ok {
		t.Fatalf("the auth section of %q does not parse", text)
	}
	return c
}

// recordOf is the method record of text's auth section.
func recordOf(t *testing.T, text string) statedb.AuthMethodState {
	t.Helper()
	return accounts.MethodRecord(authOf(t, text))
}

// seedStore seeds a store a configured run left: rec as its record and users.
func seedStore(rec *statedb.AuthMethodState, users ...*access.User) func(*testing.T, string) {
	return func(t *testing.T, path string) {
		t.Helper()
		db, err := statedb.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		if rec != nil {
			if err := db.ActivateAuth(t.Context(), *rec, func(context.Context, account.Tx, statedb.AuthMethodState, bool, statedb.AuthMethodState) error {
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, u := range users {
			if err := db.Accounts().CreateUser(t.Context(), u); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// admin is an enabled admin holding adminPassword when withPassword, and a
// link of issuer when it is set.
func admin(t *testing.T, name string, withPassword bool, issuer string) *access.User {
	t.Helper()
	now := time.Now()
	u := &access.User{Username: name, Role: access.RoleAdmin, Enabled: true, WebAuthnHandle: access.GenerateWebAuthnHandle(), CreatedAt: now, UpdatedAt: now}
	if withPassword {
		h, err := access.NewHasher(access.HasherConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if u.PasswordHash, err = h.Hash(t.Context(), adminPassword); err != nil {
			t.Fatal(err)
		}
	}
	if issuer != "" {
		u.OIDC = access.OIDCIdentity{Issuer: issuer, Subject: name}
	}
	return u
}

// countedGuard counts the user listings every method guard of the test
// binary's Run reads.
func countedGuard(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	shorten(t, &wrapGuard, func(g statedb.MethodGuard) statedb.MethodGuard {
		return func(ctx context.Context, tx account.Tx, before statedb.AuthMethodState, stored bool, next statedb.AuthMethodState) error {
			return g(ctx, countingTx{tx, &n}, before, stored, next)
		}
	})
	return &n
}

type countingTx struct {
	account.Tx
	n *atomic.Int64
}

func (c countingTx) ListUsers(ctx context.Context) ([]access.User, error) {
	c.n.Add(1)
	return c.Tx.ListUsers(ctx)
}

// TestMethodGuardRefusesStranding is the boot half of the method guard: a
// boot whose auth in force would strand a user boots Failed with the stored
// record kept, whatever mode the file alone gives.
func TestMethodGuardRefusesStranding(t *testing.T) {
	defaults := accounts.MethodRecord(accounts.Config{})
	t.Run("a restart over a refused document", func(t *testing.T) {
		s := bootSite(t, validLanguages+"auth:\n  password_login: false\n", seedStore(&defaults, admin(t, "ada", true, "")))
		s.failedWith(t, formschema.FieldError{Path: "auth.password_login", Message: stranded})
		if rec, _ := s.record(t); rec != defaults {
			t.Errorf("record = %+v, want the stored one kept", rec)
		}
		if _, ok := s.svc.Snapshot().Server(); !ok {
			t.Error("the server section was not kept")
		}
		if s.scanned.Load() {
			t.Error("a Failed boot built the media sources")
		}
		if code, answer := s.login(t, "127.0.0.1", "ada"); code != http.StatusOK {
			t.Errorf("loopback sign-in = %d %s, want 200", code, answer)
		}
		if code, answer := s.login(t, publicPeer, "ada"); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
			t.Errorf("public sign-in = %d %s, want 403 %s", code, answer, accesshttp.CodePasswordLoginDisabled)
		}
		saveText(t, s.svc, validLanguages+"auth:\n  password_login: true\n")
		if m := s.svc.Snapshot().Mode(); m != settings.ModeActive {
			t.Errorf("mode after the repair Save = %v, want Active", m)
		}
	})
	t.Run("a Failed boot refusing its parsed auth section", func(t *testing.T) {
		s := bootSite(t, "auth:\n  password_login: false\nlanguages: 7\n", seedStore(&defaults, admin(t, "bea", true, "")))
		got, _ := s.svc.Snapshot().Failure()
		if len(got) != 2 || got[1] != (formschema.FieldError{Path: "auth.password_login", Message: stranded}) || !strings.HasPrefix(got[0].Path, "languages") {
			t.Errorf("field errors = %v, want the languages one, then auth.password_login's count", got)
		}
		if _, ok := s.svc.Snapshot().Auth(); ok {
			t.Error("Auth() = true for a refused auth section")
		}
		if rec, _ := s.record(t); rec != defaults {
			t.Errorf("record = %+v, want the stored one kept", rec)
		}
		if code, answer := s.login(t, "127.0.0.1", "bea"); code != http.StatusOK {
			t.Errorf("loopback sign-in = %d %s, want 200", code, answer)
		}
	})
	t.Run("a Failed boot rewriting the record of a parsed auth section", func(t *testing.T) {
		reads := countedGuard(t)
		before := recordOf(t, validLanguages+"auth:\n  oidc_client_id: one\n")
		s := bootSite(t, "auth:\n  oidc_client_id: two\nlanguages: 7\n", seedStore(&before, admin(t, "cy", true, "")))
		if rec, _ := s.record(t); rec != recordOf(t, validLanguages+"auth:\n  oidc_client_id: two\n") || rec.SignIn.OIDCClientID != "two" {
			t.Errorf("record = %+v, want the parsed section's", rec)
		}
		if n := reads.Load(); n != 0 {
			t.Errorf("the guard listed users %d times for a change taking no method away", n)
		}
		if _, ok := s.svc.Snapshot().Auth(); !ok {
			t.Error("Auth() = false for an accepted auth section")
		}
	})
	t.Run("a Failed boot with the auth section broken writes no record", func(t *testing.T) {
		s := bootSite(t, "auth:\n  password_login: nope\nlanguages: 7\n", nil)
		if rec, ok := s.record(t); ok {
			t.Errorf("record = %+v, want none", rec)
		}
	})
	offWithOIDC := recordOf(t, validLanguages+"auth:\n  password_login: false\n"+oidcA)
	t.Run("an Unconfigured boot over a store a configured run left", func(t *testing.T) {
		s := bootSite(t, "", seedStore(&offWithOIDC, admin(t, "dee", true, "")))
		if m := s.svc.Snapshot().Mode(); m != settings.Unconfigured {
			t.Errorf("mode = %v, want Unconfigured", m)
		}
		if rec, _ := s.record(t); rec != defaults {
			t.Errorf("record = %+v, want the defaults'", rec)
		}
	})
	t.Run("an Unconfigured boot stranding an OIDC-only admin", func(t *testing.T) {
		s := bootSite(t, "", seedStore(&offWithOIDC, admin(t, "eve", false, issuerA)))
		s.failedWith(t, formschema.FieldError{Path: "auth.oidc_enabled", Message: stranded})
		server, ok := s.svc.Snapshot().Server()
		if !ok || len(server.AllowedHosts()) != 0 || len(server.TrustedProxies()) != 0 {
			t.Errorf("Server() = %+v, %v; want the defaults", server, ok)
		}
		if _, ok := s.svc.Snapshot().Auth(); ok {
			t.Error("Auth() = true for refused defaults")
		}
		if rec, _ := s.record(t); rec != offWithOIDC {
			t.Errorf("record = %+v, want the stored one kept", rec)
		}
		if code, answer := s.login(t, publicPeer, "eve"); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
			t.Errorf("public sign-in = %d %s, want 403 %s", code, answer, accesshttp.CodePasswordLoginDisabled)
		}
	})
}

// TestAccountsUseTheHostForwarderCheck: the account service Run builds reads
// the host's forwarder check, so a direct peer at a private address of one of
// the host's own interfaces, as a Docker bridge gateway is, is not local.
func TestAccountsUseTheHostForwarderCheck(t *testing.T) {
	own, found := hostPrivateAddr(t)
	if !found {
		t.Skip("the host has no private address on a non-loopback interface")
	}
	if !httpserver.NewForwarderCheck().IsForwarder(own) {
		t.Fatalf("NewForwarderCheck().IsForwarder(%s) = false for an address of the host's own interface", own)
	}
	defaults := accounts.MethodRecord(accounts.Config{})
	s := bootSite(t, validLanguages+"auth:\n  password_login: nope\n", seedStore(&defaults, admin(t, "ada", true, "")))
	if _, ok := s.svc.Snapshot().Auth(); ok {
		t.Fatal("Auth() = true for an auth section that failed to parse")
	}
	if code, answer := s.login(t, "127.0.0.1", "ada"); code != http.StatusOK {
		t.Errorf("loopback sign-in = %d %s, want 200", code, answer)
	}
	if code, answer := s.login(t, own.String(), "ada"); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
		t.Errorf("sign-in from the host's own %s = %d %s, want 403 %s", own, code, answer, accesshttp.CodePasswordLoginDisabled)
	}
}

// hostPrivateAddr is the first private address of a non-loopback interface.
func hostPrivateAddr(t *testing.T) (netip.Addr, bool) {
	t.Helper()
	ifcs, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, ifc := range ifcs {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip, ok := netip.AddrFromSlice(n.IP); ok && ip.Unmap().IsPrivate() {
				return ip.Unmap(), true
			}
		}
	}
	return netip.Addr{}, false
}

// TestBypassFollowsTheAuthSectionBootAccepted: disable_auth bypasses, and
// raises auth_disabled, only from the auth section Boot left in force; while
// config.yaml carries a true the booted bypass lacks, restart_pending names it.
func TestBypassFollowsTheAuthSectionBootAccepted(t *testing.T) {
	const turnsOff = "config.yaml's auth.disable_auth: true turns authentication off at a restart that accepts the auth section"
	defaults := accounts.MethodRecord(accounts.Config{})
	anonymous := func(t *testing.T, s *signInSite) (access.Principal, error) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "https://nas.example/", nil)
		r.RemoteAddr = publicPeer + ":40000"
		return s.accts.Authenticator().Authenticate(r)
	}
	pending := func(t *testing.T, s *signInSite, want string) {
		t.Helper()
		var got string
		if !eventually(func() bool { got, _ = alertText(s.status, status.AlertRestartPending); return got == want }) {
			t.Errorf("restart_pending %q, want %q", got, want)
		}
	}
	t.Run("a section the method guard refuses bypasses nothing", func(t *testing.T) {
		s := bootSite(t, validLanguages+"auth:\n  password_login: false\n  disable_auth: true\n", seedStore(&defaults, admin(t, "ada", true, "")))
		s.failedWith(t, formschema.FieldError{Path: "auth.password_login", Message: stranded})
		if p, err := anonymous(t, s); !errors.Is(err, access.ErrUnauthenticated) {
			t.Errorf("an anonymous public request = %v (%v), want ErrUnauthenticated", p.Method, err)
		}
		if hasAlert(s.status, status.AlertAuthDisabled) {
			t.Error("auth_disabled raised for a refused section")
		}
		pending(t, s, turnsOff)
		u, found, err := s.store.Accounts().UserByUsername(t.Context(), "ada")
		if err != nil || !found {
			t.Fatalf("UserByUsername(ada) = %v, %v", found, err)
		}
		cred := access.PasskeyCredential{UserID: u.ID, CredentialID: []byte("ada-key"), PublicKey: []byte("pk"), RPID: "nas.example", Name: "key", CreatedAt: time.Now()}
		if err := s.store.Accounts().CreatePasskey(t.Context(), &cred); err != nil {
			t.Fatal(err)
		}
		pending(t, s, turnsOff)
		saveText(t, s.svc, validLanguages+"auth:\n  disable_auth: true\n")
		if p, err := anonymous(t, s); !errors.Is(err, access.ErrUnauthenticated) {
			t.Errorf("after the repair Save, an anonymous public request = %v (%v), want ErrUnauthenticated", p.Method, err)
		}
		pending(t, s, turnsOff)
	})
	t.Run("a section that failed to parse bypasses nothing", func(t *testing.T) {
		s := bootSite(t, validLanguages+"auth:\n  password_login: nope\n  disable_auth: true\n", nil)
		if p, err := anonymous(t, s); !errors.Is(err, access.ErrUnauthenticated) {
			t.Errorf("an anonymous public request = %v (%v), want ErrUnauthenticated", p.Method, err)
		}
		if hasAlert(s.status, status.AlertAuthDisabled) {
			t.Error("auth_disabled raised for a section that failed to parse")
		}
		if hasAlert(s.status, status.AlertRestartPending) {
			t.Error("restart_pending raised for a section that failed to parse, which carries no disable_auth")
		}
		saveText(t, s.svc, validLanguages+"auth:\n  disable_auth: true\n")
		if p, err := anonymous(t, s); !errors.Is(err, access.ErrUnauthenticated) {
			t.Errorf("after the repair Save, an anonymous public request = %v (%v), want ErrUnauthenticated", p.Method, err)
		}
		pending(t, s, turnsOff)
	})
	t.Run("an accepted section bypasses", func(t *testing.T) {
		s := bootSite(t, validLanguages+"auth:\n  disable_auth: true\n", seedStore(&defaults, admin(t, "bea", true, "")))
		if p, err := anonymous(t, s); err != nil || p.Method != access.MethodBypass {
			t.Errorf("an anonymous public request = %v (%v), want the bypass", p.Method, err)
		}
		if !hasAlert(s.status, status.AlertAuthDisabled) {
			t.Error("no auth_disabled for an accepted disable_auth: true")
		}
		saveText(t, s.svc, validLanguages+"server:\n  log_format: text\nauth:\n  disable_auth: true\n")
		pending(t, s, "the log format changes at the next restart")
	})
}

// TestSaveCheckJoinsSourcesAndMethods: Save's check answers the media
// sources' refusals, then the method pre-check's, over next's auth section.
func TestSaveCheckJoinsSourcesAndMethods(t *testing.T) {
	next := settings.Active{
		Auth:    authOf(t, validLanguages+"auth:\n  password_login: false\n"),
		Sources: []mediasource.Config{{ID: "tv", Type: "nope"}},
	}
	method := formschema.FieldError{Path: "auth.password_login", Message: stranded}
	var asked int
	check := saveCheck(sourceDeps(slog.New(slog.DiscardHandler)), func(_ context.Context, c accounts.Config) []formschema.FieldError {
		asked++
		if accounts.MethodRecord(c).PasswordLogin {
			t.Error("the pre-check was not handed next's auth section")
		}
		return []formschema.FieldError{method}
	})
	got := check(t.Context(), settings.Active{}, next)
	if asked != 1 || len(got) != 2 || got[0].Path != "sources[0]" || got[1] != method {
		t.Errorf("check = %v after %d pre-checks, want the sources[0] refusal, then %v", got, asked, method)
	}
}
