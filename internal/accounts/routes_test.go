package accounts

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/access"
	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/subflux/internal/statedb"
)

func names(ds []accesshttp.Descriptor) []string {
	out := make([]string, len(ds))
	for i := range ds {
		out[i] = ds[i].Name
	}
	return out
}

func routeDescriptors(t *testing.T, s *Service) []accesshttp.Descriptor {
	t.Helper()
	routes, err := s.Routes(writeError)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]accesshttp.Descriptor, len(routes))
	for i := range routes {
		out[i] = routes[i].Descriptor
	}
	return out
}

func TestRoutesDropsUnmountedDescriptors(t *testing.T) {
	s, _ := memService(t, newEnv())
	all := names(accesshttp.Descriptors(descriptorConfig()))
	for _, n := range unmounted {
		if !slices.Contains(all, n) {
			t.Errorf("the drop table names %s, which access does not serve", n)
		}
	}
	want := slices.DeleteFunc(slices.Clone(all), func(n string) bool { return slices.Contains(unmounted, n) })
	if got := names(routeDescriptors(t, s)); !slices.Equal(got, want) {
		t.Errorf("Routes names\n%v\nwant access's routes less the six unmounted\n%v", got, want)
	}
	if len(all)-len(want) != 6 {
		t.Errorf("Routes drops %d routes, want 6", len(all)-len(want))
	}
}

func TestDescriptorsMatchRoutes(t *testing.T) {
	s, _ := memService(t, newEnv())
	type shape struct{ name, method, path, class, scope string }
	of := func(ds []accesshttp.Descriptor) []shape {
		out := make([]shape, len(ds))
		for i, d := range ds {
			out[i] = shape{d.Name, d.Method, d.Path, string(d.Class), d.Scope}
		}
		return out
	}
	if got, want := of(Descriptors()), of(routeDescriptors(t, s)); !slices.Equal(got, want) {
		t.Errorf("Descriptors()\n%v\nwant the routes Routes mounts\n%v", got, want)
	}
	t.Run("the binary links no accesstest", func(t *testing.T) {
		out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "github.com/cplieger/subflux/cmd/subflux").Output()
		if err != nil {
			t.Fatalf("go list -deps: %v", err)
		}
		if !strings.Contains(string(out), "github.com/cplieger/subflux/internal/accounts\n") {
			t.Fatal("cmd/subflux does not link accounts, so this check proves nothing")
		}
		if strings.Contains(string(out), "github.com/cplieger/access/accesstest") {
			t.Error("cmd/subflux links accesstest")
		}
	})
}

func TestOIDCLabel(t *testing.T) {
	if got := (Config{}).OIDCLabel(); got != "Single sign-on" {
		t.Errorf("default OIDCLabel = %q, want Single sign-on", got)
	}
	if got := parsed(t, map[string]any{keyOIDCLabel: "Sign in with Authelia"}).OIDCLabel(); got != "Sign in with Authelia" {
		t.Errorf("OIDCLabel = %q, want the parsed oidc_label", got)
	}
}

func TestPasswordLoginGateWhileAuthUnparsed(t *testing.T) {
	login := func(t *testing.T, st *site, peer string) (int, string) {
		t.Helper()
		w := newBrowser(t, siteOrigin, peer).call(t, st, routeLoginPassword, accesshttp.LoginRequest{Username: "admin", Password: testPassword})
		return w.Code, codeOf(t, w)
	}
	t.Run("a broken auth section", func(t *testing.T) {
		e := newEnv()
		st := newSite(t, e, accesstest.NewMemStore(), nil)
		createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
		e.set(func(e *env) { e.authOK = false })
		if code, answer := login(t, st, "203.0.113.7"); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
			t.Errorf("public sign-in = %d %s, want 403 %s", code, answer, accesshttp.CodePasswordLoginDisabled)
		}
		select {
		case ev := <-st.s.audit.events:
			if ev.Kind != "login.failure" || ev.IP != "203.0.113.7" || ev.Reason != "password_login_disabled" {
				t.Errorf("audit event = %+v, want a login.failure from 203.0.113.7", ev)
			}
		default:
			t.Error("the refusal queued no audit event")
		}
		for _, peer := range []string{"127.0.0.1", "192.168.1.5"} {
			if code, answer := login(t, st, peer); code != http.StatusOK {
				t.Errorf("sign-in from %s = %d %s, want 200", peer, code, answer)
			}
		}
		const proxy, bridge = "10.0.0.2", "172.17.0.1"
		e.set(func(e *env) {
			e.trusted = []netip.Prefix{netip.MustParsePrefix(proxy + "/32")}
			e.forwarders = []netip.Addr{netip.MustParseAddr(bridge)}
		})
		w := newBrowser(t, siteOrigin, proxy).sendWith(st, http.MethodPost, st.path(t, routeLoginPassword),
			`{"username":"admin","password":"`+testPassword+`"}`, http.Header{"X-Forwarded-For": {"192.168.1.9"}})
		if w.Code != http.StatusForbidden || codeOf(t, w) != string(accesshttp.CodePasswordLoginDisabled) {
			t.Errorf("sign-in of a private client through the listed proxy = %d %s, want 403 %s", w.Code, w.Body, accesshttp.CodePasswordLoginDisabled)
		}
		if code, answer := login(t, st, bridge); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
			t.Errorf("sign-in from the host's forwarder %s = %d %s, want 403 %s", bridge, code, answer, accesshttp.CodePasswordLoginDisabled)
		}
	})
	t.Run("password sign-in parsed off beside another broken key", func(t *testing.T) {
		e := newEnv()
		st := newSite(t, e, accesstest.NewMemStore(), nil)
		createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
		e.set(func(e *env) { e.auth = parsed(t, map[string]any{keyPasswordLogin: no}) })
		for _, peer := range []string{"127.0.0.1", "203.0.113.7"} {
			if code, answer := login(t, st, peer); code != http.StatusForbidden || answer != string(accesshttp.CodePasswordLoginDisabled) {
				t.Errorf("sign-in from %s = %d %s, want 403 %s", peer, code, answer, accesshttp.CodePasswordLoginDisabled)
			}
		}
	})
}

func TestSignInBusyAnswers503(t *testing.T) {
	e := newEnv()
	puts := &atomic.Int64{}
	st := newSite(t, e, failureCounting{MemStore: accesstest.NewMemStore(), puts: puts}, nil)
	createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
	var permits []*access.HashPermit
	for range 4 {
		p, err := st.s.hasher.h.Reserve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		permits = append(permits, p)
	}
	w := newBrowser(t, siteOrigin, "192.0.2.10").call(t, st, routeLoginPassword, accesshttp.LoginRequest{Username: "admin", Password: "not the password at all"})
	if w.Code != http.StatusServiceUnavailable || codeOf(t, w) != string(accesshttp.CodeBusy) {
		t.Errorf("sign-in with every permit held = %d %s, want 503 %s", w.Code, w.Body, accesshttp.CodeBusy)
	}
	if got := puts.Load(); got != 0 {
		t.Errorf("failure records written by a busy sign-in = %d, want 0", got)
	}
	w = newBrowser(t, siteOrigin, "127.0.0.1").call(t, st, "authSetupPassword", accesshttp.SetupPasswordRequest{Username: "bob", Password: testPassword})
	if w.Code != http.StatusConflict {
		t.Errorf("setup after setup with every permit held = %d %s, want 409", w.Code, w.Body)
	}
	for _, p := range permits {
		p.Release()
	}

	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := range 16 {
		wg.Go(func() {
			w := newBrowser(t, siteOrigin, "10.0.0."+strconv.Itoa(i+1)).call(t, st, routeLoginPassword, accesshttp.LoginRequest{Username: "admin", Password: testPassword})
			switch {
			case w.Code == http.StatusOK:
				ok.Add(1)
			case w.Code == http.StatusServiceUnavailable && codeOf(t, w) == string(accesshttp.CodeBusy):
			default:
				t.Errorf("concurrent sign-in %d = %d %s, want 200 or 503 %s", i, w.Code, w.Body, accesshttp.CodeBusy)
			}
		})
	}
	wg.Wait()
	if ok.Load() == 0 {
		t.Error("no concurrent sign-in succeeded")
	}
}

// storeSite is a site over a fresh state store signing in through idp.
func storeSite(t *testing.T, idp *accesstest.IdP, auth Config) (*site, *statedb.Store) {
	t.Helper()
	db := openStore(t)
	e := newEnv()
	e.auth = auth
	st := newSite(t, e, db.Accounts(), func(d *Deps) { d.Store, d.OIDC = db.Accounts(), idp.Client() })
	createUser(t, st.s, "admin", access.RoleAdmin, testPassword)
	return st, db
}

func commits(t *testing.T, db *statedb.Store) uint64 {
	t.Helper()
	in, err := db.Inspect(t.Context(), statedb.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return in.Commits
}

func TestOIDCStartsWriteNothing(t *testing.T) {
	idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: "https://idp.example.com", Groups: []string{"media"}})
	st, db := storeSite(t, idp, oidcSection(t, idp, siteCallback, nil))
	long := "/" + strings.Repeat("a", 3<<10)

	before := commits(t, db)
	for i := range 1_000 {
		peer := "10.1." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250+1)
		if code, _ := newBrowser(t, siteOrigin, peer).startSignIn(t, st); code != http.StatusFound {
			t.Fatalf("start %d = %d, want 302", i, code)
		}
	}
	if after := commits(t, db); after != before {
		t.Errorf("store commits across 1,000 starts = %d, want 0", after-before)
	}

	t.Run("a sign-in start with a 3 KiB redirect", func(t *testing.T) {
		b := newBrowser(t, siteOrigin, "192.0.2.10")
		w := b.send(st, http.MethodGet, st.path(t, "authOIDCStartSignIn")+"?redirect="+url.QueryEscape(long), "")
		if w.Code != http.StatusFound {
			t.Fatalf("start = %d %s, want 302", w.Code, w.Body)
		}
		for _, c := range w.Result().Cookies() {
			if len(c.Value) > 1<<10 {
				t.Errorf("flow cookie %s is %d bytes: the redirect was sealed into it", c.Name, len(c.Value))
			}
		}
		cb := b.send(st, http.MethodGet, authorize(t, idp, w.Header().Get("Location")), "")
		if loc := cb.Header().Get("Location"); cb.Code != http.StatusFound || loc != "/" {
			t.Errorf("callback = %d %q, want a redirect to /", cb.Code, loc)
		}
	})
	t.Run("a step-up start with a 3 KiB redirect", func(t *testing.T) {
		b := newBrowser(t, siteOrigin, "127.0.0.1")
		b.signInPassword(t, st, "admin")
		w := b.call(t, st, "authOIDCStartStepUp", accesshttp.RedirectRequest{Redirect: long})
		if w.Code != http.StatusBadRequest || codeOf(t, w) != string(accesshttp.CodeBadRequest) {
			t.Errorf("step-up start = %d %s, want 400 %s", w.Code, w.Body, accesshttp.CodeBadRequest)
		}
	})
}

func TestOIDCLinkBoundToBrowser(t *testing.T) {
	idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: "https://idp.example.com"})
	st, db := storeSite(t, idp, oidcSection(t, idp, siteCallback, map[string]any{keyAutoProvision: no, keyAllowedGroups: []string{}}))
	createUser(t, st.s, "carol", access.RoleUser, testPassword)
	var locations []string
	record := func(w *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		if loc := w.Header().Get("Location"); loc != "" {
			locations = append(locations, loc)
		}
		return w
	}
	a := newBrowser(t, siteOrigin, "192.0.2.10")
	code, loc := a.startSignIn(t, st)
	locations = append(locations, loc)
	if code != http.StatusFound {
		t.Fatalf("start = %d", code)
	}
	record(a.send(st, http.MethodGet, authorize(t, idp, loc), ""))

	link := func(b *browser) *httptest.ResponseRecorder {
		b.signInPassword(t, st, "carol")
		return record(b.call(t, st, "authOIDCLinkPending", nil))
	}
	if w := link(newBrowser(t, siteOrigin, "192.0.2.11")); w.Code != http.StatusNotFound || codeOf(t, w) != string(accesshttp.CodeNotFound) {
		t.Errorf("another browser's link = %d %s, want 404 %s: no pending link travels with it", w.Code, w.Body, accesshttp.CodeNotFound)
	}
	if w := link(a); w.Code != http.StatusOK {
		t.Fatalf("the starting browser's link = %d %s, want 200", w.Code, w.Body)
	}
	if _, ok, err := db.Accounts().UserByOIDC(t.Context(), access.OIDCIdentity{Issuer: idp.Issuer(), Subject: accesstest.DefaultIdPSubject}); err != nil || !ok {
		t.Errorf("the link is not stored: %v, %v", ok, err)
	}
	for _, loc := range locations {
		for _, c := range a.jar.Cookies(a.origin) {
			if len(c.Value) >= 16 && strings.Contains(loc, c.Value) {
				t.Errorf("redirect %q carries cookie %s", loc, c.Name)
			}
		}
		if strings.Contains(strings.ToLower(loc), "token") {
			t.Errorf("redirect %q names a token", loc)
		}
	}
}

func TestOIDCMaxLengthFieldsSignIn(t *testing.T) {
	issuer := "https://idp.example.com/"
	issuer += strings.Repeat("r", 255-len(issuer))
	host := "nas.example"
	for len(host) < 255-len("https://")-len(callbackPath) {
		host = "h" + host
		if n := strings.IndexByte(host, '.'); n == 63 {
			host = "h." + host[1:]
		}
	}
	redirect := "https://" + host + callbackPath
	clientID := strings.Repeat("c", 255)
	for name, n := range map[string]int{"issuer": len(issuer), "redirect": len(redirect), "client id": len(clientID)} {
		if n != 255 {
			t.Fatalf("%s is %d bytes, want 255", name, n)
		}
	}
	idp := accesstest.NewIdP(t, accesstest.IdPOptions{URL: issuer, ClientID: clientID, Groups: []string{"media"}})
	st, _ := storeSite(t, idp, oidcSection(t, idp, redirect, nil))
	b := newBrowser(t, "https://"+host, "192.0.2.10")
	if code, loc := b.signInOIDC(t, st, idp); code != http.StatusFound || loc != "/" || !hasSession(b) {
		t.Errorf("sign-in at the longest issuer, client id and redirect = %d %q, session %v; want signed in", code, loc, hasSession(b))
	}
}
