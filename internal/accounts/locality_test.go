package accounts

import (
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/accesstest"
)

// TestClientFollowsTheResolverGetter serves the routes behind
// clientaddr.Middleware as app mounts them, the resolver read per request
// from a getter a trusted_proxies Save swaps (platform-auth section 11).
func TestClientFollowsTheResolverGetter(t *testing.T) {
	e := newEnv()
	store := accesstest.NewMemStore()
	st := newSite(t, e, store, nil)
	// An IP literal Host, so the client's locality alone decides setup.
	const origin = "http://127.0.0.1:8374"
	setup := accesshttp.SetupPasswordRequest{Username: "ada", Password: testPassword}

	if w := newBrowser(t, origin, "203.0.113.9").call(t, st, "authSetupPassword", setup); codeOf(t, w) != string(accesshttp.CodeSetupTokenRequired) {
		t.Fatalf("setup from a public peer = %d %s, want %s", w.Code, w.Body, accesshttp.CodeSetupTokenRequired)
	}
	if w := newBrowser(t, origin, "127.0.0.1").call(t, st, "authSetupPassword", setup); w.Code != http.StatusOK {
		t.Fatalf("setup from loopback with no token = %d %s, want 200", w.Code, w.Body)
	}

	const proxy, client, other = "10.0.0.2", "198.51.100.7", "198.51.100.8"
	e.set(func(e *env) { e.trusted = []netip.Prefix{netip.MustParsePrefix(proxy + "/32")} })
	via := func(forwarded, password string) int {
		b := newBrowser(t, origin, proxy)
		w := b.sendWith(st, http.MethodPost, st.path(t, routeLoginPassword), `{"username":"ada","password":"`+password+`"}`,
			http.Header{"X-Forwarded-For": {forwarded}})
		return w.Code
	}
	if code := via(client, testPassword); code != http.StatusOK {
		t.Fatalf("sign-in through the listed proxy = %d, want 200", code)
	}
	ada, ok, err := store.UserByUsername(t.Context(), "ada")
	if err != nil || !ok {
		t.Fatalf("UserByUsername(ada) = %v, %v", ok, err)
	}
	sessions, err := store.SessionsByUserID(t.Context(), ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sessions, func(s access.Session) bool { return s.LastIP == client }) {
		t.Errorf("sessions %+v: none records the forwarded client %s as LastIP", sessions, client)
	}

	limited := false
	for range 20 {
		if via(client, "wrong horse battery staple") == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("20 failed sign-ins from %s through the proxy were never limited", client)
	}
	if code := via(other, "wrong horse battery staple"); code != http.StatusUnauthorized {
		t.Errorf("another client through the same proxy = %d, want 401: the limiter keys on the forwarded client", code)
	}
}
