package accounts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/access/ratelimit"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/statedb"
)

const testPassword = "correct horse battery staple 42"

// env is the settings a test service reads through its Deps getters.
type env struct {
	auth       Config
	hosts      []string
	trusted    []netip.Prefix
	forwarders []netip.Addr
	mu         sync.Mutex
	authOK     bool
	serverOK   bool
}

func newEnv() *env { return &env{authOK: true, serverOK: true} }

func (e *env) set(f func(*env)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f(e)
}

type droppedCount struct{ n atomic.Int64 }

func (d *droppedCount) IncAuthEventsDropped() { d.n.Add(1) }

// deps is a complete Deps over e, Store left nil.
func (e *env) deps() Deps {
	return Deps{
		Secrets: statedb.AuthSecrets{FailureKey: [32]byte{1}, DeviceKey: [32]byte{2}},
		Config: func() (Config, bool) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.auth, e.authOK
		},
		ServerParsed: func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.serverOK
		},
		AllowedHosts: func() []string {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.hosts
		},
		Resolver: func() clientaddr.Resolver {
			e.mu.Lock()
			defer e.mu.Unlock()
			return clientaddr.New(e.trusted)
		},
		Forwarder: func(a netip.Addr) bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return slices.Contains(e.forwarders, a)
		},
		Boot:    func() Config { return Config{} },
		Metrics: &droppedCount{},
		Breach:  &http.Client{Timeout: time.Second},
		OIDC:    &http.Client{Timeout: time.Second},
		Logger:  slog.New(slog.DiscardHandler),
	}
}

// memService composes a service over a fresh MemStore, shut down at cleanup.
func memService(t *testing.T, e *env) (*Service, *accesstest.MemStore) {
	t.Helper()
	store := accesstest.NewMemStore()
	d := e.deps()
	s, err := compose(t.Context(), &d, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(context.Background()) })
	return s, store
}

// openStore opens a state store in a temporary directory, closed at cleanup.
func openStore(t *testing.T) *statedb.Store {
	t.Helper()
	st, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// storeService builds a service through New over a fresh state store.
func storeService(t *testing.T, e *env) (*Service, *statedb.Store) {
	t.Helper()
	st := openStore(t)
	d := e.deps()
	d.Store = st.Accounts()
	s, err := New(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(context.Background()) })
	return s, st
}

func createUser(t *testing.T, s *Service, name string, role access.Role, password string) *access.User {
	t.Helper()
	u, err := s.account.CreateUser(t.Context(), account.OutOfBand("test"), account.NewUser{Username: name, Password: access.NewSecret(password), Role: role})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return u
}

func clientAt(ip string) account.Client {
	a := netip.MustParseAddr(ip)
	return account.Client{Peer: a, Via: access.ViaDirect}
}

// signIn signs name in with a password from loopback and answers the
// session's principal.
func signIn(t *testing.T, s *Service, name string) access.Principal {
	t.Helper()
	in, err := s.account.PasswordLogin(t.Context(), clientAt("127.0.0.1"), account.Credentials{Username: name, Password: access.NewSecret(testPassword)})
	if err != nil {
		t.Fatalf("sign in %s: %v", name, err)
	}
	return authenticate(t, s, func(r *http.Request) {
		cookie := s.authn.Cookie()
		r.AddCookie(&http.Cookie{Name: cookie.CookieName(r), Value: in.Token.Reveal()})
	})
}

func bearer(t *testing.T, s *Service, key access.Secret) access.Principal {
	t.Helper()
	return authenticate(t, s, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key.Reveal()) })
}

func authenticate(t *testing.T, s *Service, with func(*http.Request)) access.Principal {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8374/api/session", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	with(r)
	p, err := s.authn.Authenticate(r)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	return p
}

// failureCounting counts every failure record written, inside an Update or
// not.
type failureCounting struct {
	*accesstest.MemStore
	puts *atomic.Int64
}

func (s failureCounting) PutAuthFailure(ctx context.Context, f account.FailureCount) error {
	s.puts.Add(1)
	return s.MemStore.PutAuthFailure(ctx, f)
}

func (s failureCounting) Update(ctx context.Context, fn func(account.Tx) error) error {
	return s.MemStore.Update(ctx, func(tx account.Tx) error { return fn(countingTx{Tx: tx, puts: s.puts}) })
}

type countingTx struct {
	account.Tx
	puts *atomic.Int64
}

func (t countingTx) PutAuthFailure(ctx context.Context, f account.FailureCount) error {
	t.puts.Add(1)
	return t.Tx.PutAuthFailure(ctx, f)
}

func TestSharedHashBudget(t *testing.T) {
	e := newEnv()
	puts := &atomic.Int64{}
	store := failureCounting{MemStore: accesstest.NewMemStore(), puts: puts}
	d := e.deps()
	s, err := compose(t.Context(), &d, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(context.Background()) })
	createUser(t, s, "alice", access.RoleAdmin, testPassword)
	wrong := account.Credentials{Username: "alice", Password: access.NewSecret("not the password at all")}

	if _, err := s.account.PasswordLogin(t.Context(), clientAt("192.0.2.1"), wrong); !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("a wrong password with the budget free = %v, want ErrInvalidCredentials", err)
	}
	counted := puts.Load()
	if counted == 0 {
		t.Fatal("a wrong password wrote no failure record, so the busy case below proves nothing")
	}

	var permits []*access.HashPermit
	for range 4 {
		p, err := s.hasher.h.Reserve(t.Context())
		if err != nil {
			t.Fatalf("reserve: %v", err)
		}
		permits = append(permits, p)
	}
	t.Run("password sign-in", func(t *testing.T) {
		if _, err := s.account.PasswordLogin(t.Context(), clientAt("192.0.2.1"), wrong); !errors.Is(err, access.ErrHashBusy) {
			t.Errorf("sign-in with every permit held = %v, want ErrHashBusy", err)
		}
		if got := puts.Load(); got != counted {
			t.Errorf("failure records written = %d, want %d: a busy sign-in counted a failure", got, counted)
		}
	})
	t.Run("funnel hasher", func(t *testing.T) {
		if _, err := s.Hasher().Verify(t.Context(), "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNo", "x"); !errors.Is(err, access.ErrHashBusy) {
			t.Errorf("Hasher().Verify with every permit held = %v, want ErrHashBusy", err)
		}
	})
	for _, p := range permits {
		p.Release()
	}
}

func TestHasherRoundTrip(t *testing.T) {
	s, _ := memService(t, newEnv())
	h := s.Hasher()
	fp, err := h.Hash(t.Context(), "api-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(t.Context(), fp, "api-key-1"); err != nil || !ok {
		t.Errorf("Verify(own fingerprint) = %v, %v; want true", ok, err)
	}
	if ok, err := h.Verify(t.Context(), fp, "api-key-2"); err != nil || ok {
		t.Errorf("Verify(another credential) = %v, %v; want false", ok, err)
	}
}

type ctxKey struct{}

func TestLongLivedAuthContextsDetached(t *testing.T) {
	var limiterCtx, accountCtx context.Context
	swap(t, &newLimiter, func(ctx context.Context, c ratelimit.Config) (*ratelimit.Limiter, error) {
		limiterCtx = ctx
		return ratelimit.New(ctx, c)
	})
	swap(t, &newAccount, func(ctx context.Context, c account.Config) (*account.Service, error) {
		accountCtx = ctx
		return account.New(ctx, c)
	})
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "boot"))
	defer cancel()
	d := newEnv().deps()
	s, err := compose(ctx, &d, accesstest.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(context.Background()) })
	for name, c := range map[string]context.Context{"ratelimit.New": limiterCtx, "account.New": accountCtx} {
		if c == nil {
			t.Errorf("%s was not called through its seam", name)
			continue
		}
		if c.Done() != nil {
			t.Errorf("%s got a cancellable context: SIGTERM would stop it before Shutdown", name)
		}
		if c.Value(ctxKey{}) != "boot" {
			t.Errorf("%s's context lost New's values", name)
		}
	}
}

func swap[T any](t *testing.T, v *T, to T) {
	t.Helper()
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

func TestServiceOutlivesBootContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		store := accesstest.NewMemStore()
		d := newEnv().deps()
		s, err := compose(ctx, &d, store)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		u := createUser(t, s, "alice", access.RoleAdmin, testPassword)
		if _, err := s.account.PasswordLogin(context.Background(), clientAt("192.0.2.9"), account.Credentials{Username: "alice", Password: access.NewSecret(testPassword)}); err != nil {
			t.Fatalf("sign-in: %v", err)
		}
		wrong := account.Credentials{Username: "alice", Password: access.NewSecret("not the password at all")}
		for i := range 10 {
			if _, err := s.account.PasswordLogin(context.Background(), clientAt("192.0.2.1"), wrong); !errors.Is(err, account.ErrInvalidCredentials) {
				t.Fatalf("wrong password %d = %v, want a failed sign-in", i+1, err)
			}
		}
		var refused *ratelimit.RefusedError
		if _, err := s.account.PasswordLogin(context.Background(), clientAt("192.0.2.1"), wrong); !errors.As(err, &refused) {
			t.Errorf("the eleventh wrong password = %v, want the limiter's refusal", err)
		}

		done := make(chan error, 1)
		runCtx, stop := context.WithCancel(context.Background())
		go func() { done <- s.Run(runCtx) }()
		time.Sleep(24*time.Hour + 10*time.Minute)
		synctest.Wait()
		if sessions, err := store.SessionsByUserID(context.Background(), u.ID); err != nil || len(sessions) != 0 {
			t.Errorf("sessions after the idle timeout = %d, %v; want the sweep to have removed them", len(sessions), err)
		}
		stop()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
	})
}

func TestSetupTokenRequired(t *testing.T) {
	e := newEnv()
	s, _ := memService(t, e)
	required := s.rules().SetupTokenRequired
	ask := func(peer, host, xff string) bool {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/auth/setup", nil)
		r.Host, r.RemoteAddr = host, netip.AddrPortFrom(netip.MustParseAddr(peer), 40000).String()
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return required(r.Context(), account.Client{Host: r.Host, ClientAddr: s.clientAddr(r)})
	}
	listed := []netip.Prefix{netip.MustParsePrefix("192.168.1.2/32"), netip.MustParsePrefix("192.168.1.3/32")}
	cases := []struct {
		name, peer, host, xff string
		hosts                 []string
		trusted               []netip.Prefix
		forwarders            []netip.Addr
		serverFailed          bool
		want                  bool
	}{
		{name: "loopback at an IP literal", peer: "127.0.0.1", host: "127.0.0.1:8374"},
		{name: "loopback at the IPv6 loopback", peer: "::1", host: "[::1]"},
		{name: "private at its own address", peer: "192.168.1.10", host: "192.168.1.5"},
		{name: "loopback at localhost", peer: "127.0.0.1", host: "localhost:8374"},
		{name: "private at a listed name", peer: "192.168.1.10", host: "nas.lan:8374", hosts: []string{"nas.lan"}},
		{name: "private at a name with no list", peer: "192.168.1.10", host: "nas.lan:8374", want: true},
		{name: "private at an unlisted name", peer: "192.168.1.10", host: "evil.example", hosts: []string{"nas.lan"}, want: true},
		{name: "public at an IP literal", peer: "203.0.113.9", host: "127.0.0.1:8374", want: true},
		{name: "public at a listed name", peer: "203.0.113.9", host: "nas.lan", hosts: []string{"nas.lan"}, want: true},
		{name: "loopback while the server section failed", peer: "127.0.0.1", host: "127.0.0.1:8374", serverFailed: true, want: true},
		{name: "a listed proxy forwarding a private client", peer: "192.168.1.2", host: "192.168.1.5", xff: "192.168.1.10", trusted: listed, want: true},
		{name: "a listed proxy forwarding a loopback client", peer: "192.168.1.2", host: "127.0.0.1:8374", xff: "127.0.0.1", trusted: listed, want: true},
		{name: "a direct private peer that is a host forwarder", peer: "172.17.0.1", host: "127.0.0.1:8374", forwarders: []netip.Addr{netip.MustParseAddr("172.17.0.1")}, want: true},
		{name: "a direct private peer beside a host forwarder", peer: "172.17.0.5", host: "127.0.0.1:8374", forwarders: []netip.Addr{netip.MustParseAddr("172.17.0.1")}},
		{name: "a listed proxy forwarding no client", peer: "192.168.1.2", host: "192.168.1.5", trusted: listed, want: true},
		{name: "a listed proxy forwarding only listed hops", peer: "192.168.1.2", host: "192.168.1.5", xff: "192.168.1.3", trusted: listed, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e.set(func(e *env) {
				e.hosts, e.trusted, e.forwarders, e.serverOK = c.hosts, c.trusted, c.forwarders, !c.serverFailed
			})
			if got := ask(c.peer, c.host, c.xff); got != c.want {
				t.Errorf("token required = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("an allowed_hosts change reaches the next call", func(t *testing.T) {
		e.set(func(e *env) { e.hosts, e.trusted, e.serverOK = nil, nil, true })
		if !ask("192.168.1.10", "nas.lan:8374", "") {
			t.Fatal("an unlisted name needs no token")
		}
		e.set(func(e *env) { e.hosts = []string{"nas.lan"} })
		if ask("192.168.1.10", "nas.lan:8374", "") {
			t.Error("the newly listed name still needs a token")
		}
	})
}

func TestNewNamesMissingDependency(t *testing.T) {
	st := openStore(t)
	full := func() Deps {
		d := newEnv().deps()
		d.Store = st.Accounts()
		return d
	}
	cases := map[string]func(*Deps){
		"Store":        func(d *Deps) { d.Store = nil },
		"Secrets":      func(d *Deps) { d.Secrets = statedb.AuthSecrets{} },
		"Config":       func(d *Deps) { d.Config = nil },
		"Boot":         func(d *Deps) { d.Boot = nil },
		"ServerParsed": func(d *Deps) { d.ServerParsed = nil },
		"AllowedHosts": func(d *Deps) { d.AllowedHosts = nil },
		"Resolver":     func(d *Deps) { d.Resolver = nil },
		"Forwarder":    func(d *Deps) { d.Forwarder = nil },
		"Metrics":      func(d *Deps) { d.Metrics = nil },
		"Breach":       func(d *Deps) { d.Breach = nil },
		"OIDC":         func(d *Deps) { d.OIDC = nil },
		"Logger":       func(d *Deps) { d.Logger = nil },
	}
	for field, drop := range cases {
		t.Run(field, func(t *testing.T) {
			d := full()
			drop(&d)
			s, err := New(t.Context(), d)
			if err == nil {
				_ = s.shutdown(context.Background())
				t.Fatal("New accepted the missing dependency")
			}
			if !strings.Contains(err.Error(), "Deps."+field) {
				t.Errorf("New = %v, want it to name Deps.%s", err, field)
			}
		})
	}
	t.Run("complete", func(t *testing.T) {
		s, err := New(t.Context(), full())
		if err != nil {
			t.Fatalf("New with every dependency = %v", err)
		}
		_ = s.shutdown(context.Background())
	})
}

func TestKeyPermissionScopes(t *testing.T) {
	s, _ := memService(t, newEnv())
	ctx := t.Context()
	for got, want := range map[string]string{s.account.AdminScope(): scopeAdmin, s.account.ManageUsersScope(): scopeManageUsers, s.account.CreateKeysScope(): scopeCreateKeys} {
		if got != want {
			t.Errorf("composed scope %q, want %q", got, want)
		}
	}
	for _, scope := range []string{scopeAdmin, scopeManageUsers, scopeCreateKeys} {
		if !strings.Contains(strings.Join(s.rules().Scopes, " "), scope) {
			t.Errorf("Rules.Scopes lacks %s", scope)
		}
	}
	root := createUser(t, s, "root", access.RoleAdmin, testPassword)
	mint := func(scopes ...string) account.CreatedKey {
		k, err := s.account.CreateAPIKey(ctx, account.OutOfBand("test"), root.ID, account.NewKey{Label: strings.Join(scopes, "+"), Scopes: scopes})
		if err != nil {
			t.Fatalf("mint %v: %v", scopes, err)
		}
		return k
	}
	users := mint(scopeManageUsers)
	byUsers := bearer(t, s, users.Plaintext)
	admin := bearer(t, s, mint(scopeAdmin).Plaintext)

	t.Run("manage_users creates a regular user", func(t *testing.T) {
		if _, err := s.account.CreateUser(ctx, account.As(byUsers), account.NewUser{Username: "bob", Password: access.NewSecret(testPassword)}); err != nil {
			t.Errorf("CreateUser = %v", err)
		}
	})
	t.Run("manage_users cannot create an admin", func(t *testing.T) {
		_, err := s.account.CreateUser(ctx, account.As(byUsers), account.NewUser{Username: "eve", Password: access.NewSecret(testPassword), Role: access.RoleAdmin})
		var pe *account.PolicyError
		if !errors.As(err, &pe) || pe.Reason != account.ReasonAdminTarget {
			t.Errorf("CreateUser(admin) = %v, want the %s refusal", err, account.ReasonAdminTarget)
		}
	})
	t.Run("admin alone mints no key and creates no user", func(t *testing.T) {
		if _, err := s.account.CreateAPIKey(ctx, account.As(admin), root.ID, account.NewKey{Label: "x", Scopes: []string{scopeRead}}); !errors.Is(err, account.ErrScopeRequired) {
			t.Errorf("CreateAPIKey = %v, want ErrScopeRequired", err)
		}
		if _, err := s.account.CreateUser(ctx, account.As(admin), account.NewUser{Username: "carol", Password: access.NewSecret(testPassword)}); !errors.Is(err, account.ErrScopeRequired) {
			t.Errorf("CreateUser = %v, want ErrScopeRequired", err)
		}
	})
	t.Run("revoking the key keeps the user it created", func(t *testing.T) {
		if err := s.account.RevokeAPIKey(ctx, signIn(t, s, "root"), users.Key.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		listed, err := s.account.Users(ctx, admin, account.UserFilter{AttributedToKey: users.Key.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].User.Username != "bob" {
			t.Errorf("users attributed to the revoked key = %v, want bob alone", listed)
		}
	})
}

func TestBypassReadsBootOnly(t *testing.T) {
	section := func(on bool) Config {
		return parsed(t, map[string]any{keyDisableAuth: map[bool]string{true: yes, false: no}[on]})
	}
	bypassing := func(boot *atomic.Bool, live bool) bool {
		e := newEnv()
		e.auth = section(live)
		d := e.deps()
		d.Boot = func() Config { return section(boot.Load()) }
		s, err := compose(t.Context(), &d, accesstest.NewMemStore())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.shutdown(context.Background()) }()
		boot.Store(!boot.Load())
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		p, err := s.authn.Authenticate(r)
		return err == nil && p.Method == access.MethodBypass
	}
	var off, on atomic.Bool
	on.Store(true)
	if !bypassing(&off, false) {
		t.Error("disable_auth in Boot's section once composed did not bypass with the live section off")
	}
	if bypassing(&on, true) {
		t.Error("disable_auth set only in the live section, or in Boot's before compose, bypassed")
	}
}

func TestSetupTokenLoggedWhenNoUser(t *testing.T) {
	t.Run("no user", func(t *testing.T) {
		logger, rec := capture.New()
		e := newEnv()
		st := openStore(t)
		d := e.deps()
		d.Store, d.Logger = st.Accounts(), logger
		s, err := New(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.shutdown(context.Background()) }()
		var found bool
		for _, r := range rec.Records() {
			if r.Level != slog.LevelWarn || !strings.Contains(r.Message, "server.allowed_hosts") {
				continue
			}
			found = true
			attrs := map[string]string{}
			r.Attrs(func(a slog.Attr) bool {
				attrs[a.Key] = a.Value.String()
				return true
			})
			if attrs["setup_token"] == "" {
				t.Error("the WARN line carries no setup token")
			}
			if _, err := time.Parse(time.RFC3339, attrs["expires"]); err != nil {
				t.Errorf("expires = %q, want an RFC 3339 time", attrs["expires"])
			}
			state, err := s.account.State(t.Context())
			if err != nil || !state.SetupRequired {
				t.Errorf("State = %+v, %v; want setup required", state, err)
			}
		}
		if !found {
			t.Errorf("no WARN line names server.allowed_hosts beside a token: %v", rec.Records())
		}
	})
	t.Run("a user exists", func(t *testing.T) {
		st := openStore(t)
		e := newEnv()
		d := e.deps()
		d.Store = st.Accounts()
		first, err := New(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		createUser(t, first, "alice", access.RoleAdmin, testPassword)
		_ = first.shutdown(context.Background())
		logger, rec := capture.New()
		d.Logger = logger
		s, err := New(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.shutdown(context.Background()) }()
		for _, r := range rec.Records() {
			if r.Level == slog.LevelWarn {
				t.Errorf("a WARN line with a user present: %s", r.Message)
			}
		}
	})
}

func TestCookieSecureBehindUnlistedTLSProxy(t *testing.T) {
	s, _ := memService(t, newEnv())
	r := httptest.NewRequest(http.MethodPost, "http://nas.example/api/auth/login", nil)
	r.RemoteAddr = "172.18.0.2:40000"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	cookie := s.authn.Cookie()
	cookie.SetCookie(w, r, "token", 60)
	got := w.Result().Cookies()
	if len(got) != 1 || !got[0].Secure || got[0].Name != "__Host-sfx_session" {
		t.Errorf("cookie behind an unlisted TLS proxy = %+v, want one Secure __Host-sfx_session", got)
	}
}
