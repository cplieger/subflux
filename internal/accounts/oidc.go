package accounts

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/oidc"
)

// oidcRetry is the shortest interval between two failed provider builds of
// one configuration.
const oidcRetry = 30 * time.Second

// oidcProviders is account's OIDC getter: a provider built lazily, on the
// first sign-in start after boot, and reused until an OIDC field changes. A
// failed build is never kept: the getter answers nil (auth_oidc_unavailable)
// and the same configuration builds again only after oidcRetry, so a late
// identity provider works without a restart.
type oidcProviders struct {
	failedAt time.Time
	provider *oidc.Provider
	// flows outlives every provider, so a rebuild keeps in-flight flows.
	flows  *access.Sealer
	client *http.Client
	logger *slog.Logger
	now    func() time.Time
	built  oidcKey
	failed oidcKey
	mu     sync.Mutex
}

type oidcKey struct{ issuer, clientID, secret, redirect string }

// get answers c's provider, nil while OIDC is off or unavailable. The lock
// is held across discovery, so no request waits on more than one; ctx is
// detached from boot, and Deps.OIDC's timeout bounds discovery.
func (o *oidcProviders) get(ctx context.Context, c Config) *oidc.Provider {
	cfg := c.get()
	if !cfg.oidcEnabled {
		return nil
	}
	k := oidcKey{issuer: cfg.issuer, clientID: cfg.clientID, secret: cfg.secret, redirect: cfg.redirect}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil && o.built == k {
		return o.provider
	}
	if !o.failedAt.IsZero() && o.failed == k && o.now().Sub(o.failedAt) < oidcRetry {
		return nil
	}
	p, err := oidc.NewProvider(ctx, oidc.Config{
		HTTPClient: o.client, FlowSealer: o.flows,
		IssuerURL: cfg.issuer, ClientID: cfg.clientID, ClientSecret: access.NewSecret(cfg.secret), RedirectURL: cfg.redirect,
	})
	if err != nil {
		o.failedAt, o.failed = o.now(), k
		o.logger.Warn("accounts: the OIDC provider is unavailable; single sign-on is off until a later build succeeds",
			"issuer", cfg.issuer, "retry_after", oidcRetry, "error", err)
		return nil
	}
	o.provider, o.built, o.failedAt = p, k, time.Time{}
	return p
}
