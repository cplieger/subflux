package accounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/access/oidc"
	"github.com/cplieger/access/ratelimit"
	"github.com/cplieger/access/webauthn"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/statedb"
)

// droppedCounter is the one metric accounts writes; *obs.Metrics satisfies
// it.
type droppedCounter interface{ IncAuthEventsDropped() }

// Deps is what New composes over. Every field is required.
type Deps struct {
	Store *statedb.AccountStore
	// Config is the auth section in force; false means it failed to parse,
	// and password sign-in is then refused to every client that is not
	// local.
	Config func() (Config, bool)
	// ServerParsed is false while the server section failed to parse; setup
	// then needs the token from every address.
	ServerParsed func() bool
	// AllowedHosts is the server section's allowed_hosts in force: the names
	// a token-free setup may send as its Host.
	AllowedHosts func() []string
	// Resolver is the client resolver in force.
	Resolver func() clientaddr.Resolver
	// Forwarder reports whether a direct peer is one of this host's own
	// forwarders (httpserver.ForwarderCheck's IsForwarder); such a peer is
	// never a local client.
	Forwarder func(netip.Addr) bool
	Metrics   droppedCounter
	Breach    *http.Client
	// OIDC is the client for discovery, JWKS and token calls; its Timeout
	// bounds discovery (10 s when zero).
	OIDC   *http.Client
	Logger *slog.Logger
	// Boot is the auth section settings' Boot left in force, the defaults
	// when none was or before it returns: disable_auth's only source, read
	// per request.
	Boot    func() Config
	Secrets statedb.AuthSecrets
}

// Service is subflux's composition of the access library.
type Service struct {
	account  *account.Service
	users    account.UserStore
	limiter  *ratelimit.Limiter
	authn    *access.Authenticator
	hasher   *Hasher
	passkeys *relyingParties
	oidc     *oidcProviders
	audit    *auditSink
	deps     Deps
}

// setupTokenTTL is how long the setup token New logs stays valid.
const setupTokenTTL = 24 * time.Hour

// newLimiter and newAccount are the constructors of the two long-lived
// goroutine owners, seams for the test that captures their contexts.
var (
	newLimiter = ratelimit.New
	newAccount = account.New
)

// New builds the hash budget, authenticator, limiter and account service,
// and mints and logs a setup token when no user exists. Their goroutines run
// on context.WithoutCancel(ctx), so only Run's shutdown stops them.
func New(ctx context.Context, d Deps) (*Service, error) { //nolint:gocritic // hugeParam: built once at boot
	if err := d.check(); err != nil {
		return nil, err
	}
	s, err := compose(ctx, &d, d.Store)
	if err != nil {
		return nil, err
	}
	if err := s.logSetupToken(ctx, d.Store); err != nil {
		return nil, errors.Join(err, s.shutdown(context.WithoutCancel(ctx)))
	}
	return s, nil
}

// check names the first required dependency d lacks.
func (d *Deps) check() error {
	var missing string
	switch {
	case d.Store == nil:
		missing = "Store"
	case d.Secrets.FailureKey == [32]byte{} || d.Secrets.DeviceKey == [32]byte{}:
		missing = "Secrets"
	case d.Config == nil:
		missing = "Config"
	case d.Boot == nil:
		missing = "Boot"
	case d.ServerParsed == nil:
		missing = "ServerParsed"
	case d.AllowedHosts == nil:
		missing = "AllowedHosts"
	case d.Resolver == nil:
		missing = "Resolver"
	case d.Forwarder == nil:
		missing = "Forwarder"
	case d.Metrics == nil:
		missing = "Metrics"
	case d.Breach == nil:
		missing = "Breach"
	case d.OIDC == nil:
		missing = "OIDC"
	case d.Logger == nil:
		missing = "Logger"
	default:
		return nil
	}
	return fmt.Errorf("accounts: New needs Deps.%s", missing)
}

// compose is New's wiring over store, every field the library's
// constructors take named here.
func compose(ctx context.Context, d *Deps, store account.Store) (*Service, error) {
	budget, err := access.NewHashBudget(76 << 20)
	if err != nil {
		return nil, err
	}
	h, err := access.NewHasher(access.HasherConfig{Budget: budget})
	if err != nil {
		return nil, err
	}
	s := &Service{
		hasher:   &Hasher{h},
		users:    store,
		passkeys: &relyingParties{logger: d.Logger},
		oidc:     &oidcProviders{flows: access.NewEphemeralSealer(), client: d.OIDC, logger: d.Logger, now: time.Now},
		audit:    newAuditSink(d.Metrics, d.Logger),
		deps:     *d,
	}
	if s.authn, err = access.New(store, access.Config{
		Cookie:           access.CookieConfig{Posture: access.PosturePerRequest, Name: "sfx_session", HTTPS: access.RequestIsHTTPS},
		TimeoutSource:    func() access.SessionTimeouts { return s.config().timeouts() },
		ActivityThrottle: time.Minute,
		ClientAddr:       s.clientAddr,
		Bypass:           func() bool { return BypassEnabled(s.deps.Boot()) },
		Logger:           d.Logger,
	}); err != nil {
		return nil, err
	}
	devices, err := access.NewSealer(d.Secrets.DeviceKey[:])
	if err != nil {
		return nil, err
	}
	detached := context.WithoutCancel(ctx)
	lc := ratelimit.DefaultConfig()
	lc.Devices, lc.Logger = devices, d.Logger
	if s.limiter, err = newLimiter(detached, lc); err != nil {
		return nil, err
	}
	s.account, err = newAccount(detached, account.Config{
		Store:            store,
		Policy:           account.RolePolicy{KeepLocalAdmin: true, KeysAdminOnly: true},
		Authenticator:    s.authn,
		Hasher:           h,
		Limiter:          s.limiter,
		RP:               func() *webauthn.RelyingParty { return s.passkeys.get(s.config()) },
		OIDC:             func() *oidc.Provider { return s.oidc.get(detached, s.config()) },
		Rules:            s.rules,
		Breach:           d.Breach,
		Logger:           d.Logger,
		FailureKey:       d.Secrets.FailureKey[:],
		AdminScope:       scopeAdmin,
		ManageUsersScope: scopeManageUsers,
		CreateKeysScope:  scopeCreateKeys,
	})
	if err != nil {
		return nil, errors.Join(err, s.limiter.Shutdown(detached))
	}
	return s, nil
}

// config is the auth section in force, the defaults while it failed.
func (s *Service) config() Config {
	if c, ok := s.deps.Config(); ok {
		return c
	}
	return Config{}
}

// rules is account's Rules getter, read once per operation.
func (s *Service) rules() account.Rules {
	c := s.config()
	return rulesOf(c, s.setupTokenRequired, func() string { return s.passkeys.id(s.config()) })
}

// clientAddr is the address clientaddr.Middleware stored for r, or one from
// the resolver in force off the middleware.
func (s *Service) clientAddr(r *http.Request) access.ClientAddr {
	if a, ok := clientaddr.From(r.Context()); ok {
		return a
	}
	return s.deps.Resolver().Client(r)
}

// logSetupToken mints a setup token and logs it at WARN when no user exists.
func (s *Service) logSetupToken(ctx context.Context, users account.UserStore) error {
	n, err := users.UserCount(ctx)
	if err != nil || n > 0 {
		return err
	}
	tok, err := s.account.IssueSetupToken(ctx, account.OutOfBand("startup"), setupTokenTTL)
	if err != nil {
		return err
	}
	s.deps.Logger.Warn("accounts: no account exists yet; open subflux to set one up. Setting up from a hostname? add it to server.allowed_hosts, or paste this token",
		"setup_token", tok.Reveal(), "expires", time.Now().Add(setupTokenTTL).UTC().Format(time.RFC3339))
	return nil
}

// Run logs the audit queue until ctx ends, then the events already queued,
// then shuts down the account service and the limiter on
// context.WithoutCancel(ctx); app bounds that by waiting no longer than its
// drain budget.
func (s *Service) Run(ctx context.Context) error {
	s.audit.run(ctx)
	return s.shutdown(context.WithoutCancel(ctx))
}

func (s *Service) shutdown(ctx context.Context) error {
	return errors.Join(s.account.Shutdown(ctx), s.limiter.Shutdown(ctx))
}

// Authenticator resolves a request to its principal.
func (s *Service) Authenticator() *access.Authenticator { return s.authn }

// Hasher is the provider funnel's hasher, sharing the account service's hash
// budget.
func (s *Service) Hasher() *Hasher { return s.hasher }
