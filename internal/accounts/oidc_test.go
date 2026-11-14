package accounts

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesstest"
)

const siteCallback = siteOrigin + callbackPath

// fakeClock is the OIDC getter's clock, moved only by the test.
type fakeClock struct {
	at time.Time
	mu sync.Mutex
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// oidcSite is a site signing in through a fresh IdP whose discovery the
// returned discovery counts and can fail.
func oidcSite(t *testing.T, o accesstest.IdPOptions) (*site, *env, *accesstest.IdP, *discovery) {
	t.Helper()
	if o.URL == "" {
		o.URL = "https://idp.example.com"
	}
	o.Groups = []string{"media"}
	idp := accesstest.NewIdP(t, o)
	disc := &discovery{next: idp.Client().Transport}
	e := newEnv()
	e.auth = oidcSection(t, idp, siteCallback, nil)
	st := newSite(t, e, accesstest.NewMemStore(), func(d *Deps) {
		d.OIDC = &http.Client{Transport: disc, Timeout: 5 * time.Second}
	})
	createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
	return st, e, idp, disc
}

// startSignIn serves the OIDC sign-in start from b.
func (b *browser) startSignIn(t *testing.T, st *site) (code int, location string) {
	t.Helper()
	w := b.send(st, http.MethodGet, st.path(t, "authOIDCStartSignIn"), "")
	return w.Code, w.Header().Get("Location")
}

// signInOIDC runs a whole OIDC sign-in from b and answers the callback's
// redirect, failing the test unless the start reached idp.
func (b *browser) signInOIDC(t *testing.T, st *site, idp *accesstest.IdP) (code int, location string) {
	t.Helper()
	code, loc := b.startSignIn(t, st)
	if code != http.StatusFound || !strings.HasPrefix(loc, idp.Issuer()) {
		t.Fatalf("sign-in start = %d %q, want a redirect to the IdP", code, loc)
	}
	w := b.send(st, http.MethodGet, authorize(t, idp, loc), "")
	return w.Code, w.Header().Get("Location")
}

func hasSession(b *browser) bool {
	for _, c := range b.jar.Cookies(b.origin) {
		if strings.HasSuffix(c.Name, "sfx_session") && c.Value != "" {
			return true
		}
	}
	return false
}

func TestOIDCProviderRetriesWithoutCaching(t *testing.T) {
	st, e, idp, disc := oidcSite(t, accesstest.IdPOptions{})
	clock := &fakeClock{at: time.Unix(1_000_000, 0)}
	st.s.oidc.now = clock.now
	unavailable := func(t *testing.T, peer string) {
		t.Helper()
		b := newBrowser(t, siteOrigin, peer)
		w := b.send(st, http.MethodGet, st.path(t, "authOIDCStartSignIn"), "")
		if w.Code != http.StatusBadRequest || codeOf(t, w) != "auth_oidc_unavailable" {
			t.Errorf("start from %s = %d %s, want 400 auth_oidc_unavailable", peer, w.Code, w.Body)
		}
	}
	disc.set(true)
	unavailable(t, "192.0.2.10")
	if got := disc.count(); got != 1 {
		t.Fatalf("discovery calls after a failed start = %d, want 1", got)
	}

	clock.advance(oidcRetry - time.Second)
	unavailable(t, "192.0.2.11")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { unavailable(t, "192.0.2."+strconv.Itoa(20+i)) })
	}
	wg.Wait()
	disc.set(false)
	unavailable(t, "192.0.2.12")
	if got := disc.count(); got != 1 {
		t.Errorf("discovery calls within %s of a failure = %d, want 1", oidcRetry, got)
	}

	clock.advance(time.Second)
	b := newBrowser(t, siteOrigin, "192.0.2.13")
	if code, loc := b.signInOIDC(t, st, idp); code != http.StatusFound || loc != "/" || !hasSession(b) {
		t.Errorf("sign-in after recovery = %d %q, session %v; want a signed-in redirect to /", code, loc, hasSession(b))
	}
	if got := disc.count(); got != 2 {
		t.Errorf("discovery calls after recovery = %d, want 2", got)
	}

	t.Run("an unchanged configuration reuses the provider", func(t *testing.T) {
		label := map[string]any{keyOIDCLabel: "Sign in with the IdP"}
		e.set(func(e *env) { e.auth = oidcSection(t, idp, siteCallback, label) })
		if code, _ := newBrowser(t, siteOrigin, "192.0.2.14").startSignIn(t, st); code != http.StatusFound {
			t.Fatalf("start = %d, want 302", code)
		}
		if got := disc.count(); got != 2 {
			t.Errorf("discovery calls = %d, want 2: a label change rebuilt the provider", got)
		}
	})
	t.Run("a changed OIDC field rebuilds it", func(t *testing.T) {
		e.set(func(e *env) { e.auth = oidcSection(t, idp, "https://nas.example:8443"+callbackPath, nil) })
		code, loc := newBrowser(t, siteOrigin, "192.0.2.15").startSignIn(t, st)
		if code != http.StatusFound || !strings.Contains(loc, url.QueryEscape("https://nas.example:8443"+callbackPath)) {
			t.Fatalf("start = %d %q, want a redirect naming the new redirect URL", code, loc)
		}
		if got := disc.count(); got != 3 {
			t.Errorf("discovery calls = %d, want 3", got)
		}
	})
}

func TestOIDCBuildOutlivesBootContext(t *testing.T) {
	t.Run("a start after boot builds on a detached context", func(t *testing.T) {
		idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: "https://idp.example.com"})
		e := newEnv()
		e.auth = oidcSection(t, idp, siteCallback, nil)
		d := e.deps()
		d.OIDC = idp.Client()
		ctx, cancel := context.WithCancel(t.Context())
		s, err := compose(ctx, &d, accesstest.NewMemStore())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.shutdown(context.Background()) })
		cancel()
		code, loc := newBrowser(t, siteOrigin, "192.0.2.10").startSignIn(t, mount(t, s))
		if code != http.StatusFound || !strings.HasPrefix(loc, idp.Issuer()) {
			t.Errorf("start after boot's context ended = %d %q, want a redirect to the IdP", code, loc)
		}
	})
	t.Run("a callback verifies after the IdP rotates its signing key", func(t *testing.T) {
		idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: "https://idp.example.com", Groups: []string{"media"}})
		e := newEnv()
		e.auth = oidcSection(t, idp, siteCallback, nil)
		d := e.deps()
		d.OIDC = idp.Client()
		ctx, cancel := context.WithCancel(t.Context())
		s, err := compose(ctx, &d, accesstest.NewMemStore())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.shutdown(context.Background()) })
		cancel()
		st := mount(t, s)
		createUser(t, s, "admin", access.RoleAdmin, testPassword)
		signIn := func(when, peer string) {
			t.Helper()
			b := newBrowser(t, siteOrigin, peer)
			if code, loc := b.signInOIDC(t, st, idp); code != http.StatusFound || loc != "/" || !hasSession(b) {
				t.Errorf("sign-in %s = %d %q, session %v; want a signed-in redirect to /", when, code, loc, hasSession(b))
			}
		}
		signIn("before the rotation", "192.0.2.10")
		idp.RotateKey()
		signIn("after RotateKey", "192.0.2.11")
		idp.RetireOldKeys()
		signIn("after RetireOldKeys", "192.0.2.12")
	})
	t.Run("an IdP that never answers discovery fails within the client timeout", func(t *testing.T) {
		idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: "https://idp.example.com", Discovery: accesstest.IdPEndpoint{Delay: time.Minute}})
		e := newEnv()
		e.auth = oidcSection(t, idp, siteCallback, nil)
		st := newSite(t, e, accesstest.NewMemStore(), func(d *Deps) {
			d.OIDC = &http.Client{Transport: idp.Client().Transport, Timeout: 200 * time.Millisecond}
		})
		start := time.Now()
		w := newBrowser(t, siteOrigin, "192.0.2.10").send(st, http.MethodGet, st.path(t, "authOIDCStartSignIn"), "")
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("a start waited %s on a silent IdP, want about the 200ms client timeout", took)
		}
		if codeOf(t, w) != "auth_oidc_unavailable" {
			t.Errorf("start = %d %s, want auth_oidc_unavailable", w.Code, w.Body)
		}
	})
}
