package accounts

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/accesstest"
	"github.com/cplieger/access/account"
	"github.com/cplieger/subflux/internal/clientaddr"
)

// siteOrigin is the origin the test browsers use.
const siteOrigin = "https://nas.example"

// answer is the body writeError sends.
type answer struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func writeError(w http.ResponseWriter, _ *http.Request, e accesshttp.Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(answer{Code: string(e.Code), Reason: e.Reason})
}

// site is a service's routes mounted as app mounts them: on one mux behind
// clientaddr.Middleware.
type site struct {
	s      *Service
	h      http.Handler
	routes map[string]accesshttp.Route
}

// newSite composes a service over store, its Deps from e as edit leaves
// them, and mounts its routes.
func newSite(t *testing.T, e *env, store account.Store, edit func(*Deps)) *site {
	t.Helper()
	d := e.deps()
	if edit != nil {
		edit(&d)
	}
	s, err := compose(t.Context(), &d, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown(t.Context()) })
	return mount(t, s)
}

func mount(t *testing.T, s *Service) *site {
	t.Helper()
	routes, err := s.Routes(writeError)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	byName := map[string]accesshttp.Route{}
	for _, r := range routes {
		mux.Handle(r.Method+" "+r.Path, r.Handler)
		byName[r.Name] = r
	}
	return &site{s: s, h: clientaddr.Middleware(s.deps.Resolver, clientaddr.NewDetector())(mux), routes: byName}
}

func (st *site) path(t *testing.T, name string) string {
	t.Helper()
	r, ok := st.routes[name]
	if !ok {
		t.Fatalf("no route %s", name)
	}
	return r.Path
}

// browser is one user agent: its cookie jar, the origin it sends, and the
// peer address requests arrive from.
type browser struct {
	jar    *cookiejar.Jar
	origin *url.URL
	peer   string
}

func newBrowser(t *testing.T, origin, peer string) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	return &browser{jar: jar, origin: u, peer: peer}
}

// send serves one same-origin request on st with the browser's cookies, and
// keeps the cookies the answer sets.
func (b *browser) send(st *site, method, target, body string) *httptest.ResponseRecorder {
	return b.sendWith(st, method, target, body, nil)
}

// sendWith is send with hdr's headers added.
func (b *browser) sendWith(st *site, method, target, body string, hdr http.Header) *httptest.ResponseRecorder {
	if !strings.Contains(target, "://") {
		target = strings.TrimSuffix(b.origin.String(), "/") + target
	}
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		r.Header.Set("Origin", strings.TrimSuffix(b.origin.String(), "/"))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	r.RemoteAddr = netip.AddrPortFrom(netip.MustParseAddr(b.peer), 40000).String()
	maps.Copy(r.Header, hdr)
	for _, c := range b.jar.Cookies(r.URL) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	st.h.ServeHTTP(w, r)
	b.jar.SetCookies(r.URL, w.Result().Cookies())
	return w
}

// call is send of a named route with v as its JSON body, nil for none.
func (b *browser) call(t *testing.T, st *site, name string, v any) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		body = string(raw)
	}
	return b.send(st, st.routes[name].Method, st.path(t, name), body)
}

func codeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var a answer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return a.Code
}

// signInPassword signs name in from b with testPassword.
func (b *browser) signInPassword(t *testing.T, st *site, name string) {
	t.Helper()
	w := b.call(t, st, routeLoginPassword, accesshttp.LoginRequest{Username: name, Password: testPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("password sign-in as %s = %d %s", name, w.Code, w.Body)
	}
}

// discovery is an IdP client whose discovery requests can be failed and
// are counted.
type discovery struct {
	next  http.RoundTripper
	mu    sync.Mutex
	calls int
	fail  bool
}

func (d *discovery) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
		return d.next.RoundTrip(r)
	}
	d.mu.Lock()
	d.calls++
	fail := d.fail
	d.mu.Unlock()
	if fail {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Request: r, Header: http.Header{}}, nil
	}
	return d.next.RoundTrip(r)
}

func (d *discovery) set(fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail = fail
}

func (d *discovery) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// oidcSection is an auth section signing in through idp with redirect,
// auto-provisioning members of the media group.
func oidcSection(t *testing.T, idp *accesstest.IdP, redirect string, over map[string]any) Config {
	t.Helper()
	kv := map[string]any{
		keyOIDCEnabled: yes, keyOIDCIssuer: idp.Issuer(), keyOIDCClientID: idp.ClientID(),
		keyOIDCSecret: idp.ClientSecret(), keyOIDCRedirect: redirect,
		keyAutoProvision: yes, keyAllowedGroups: []string{"media"},
	}
	maps.Copy(kv, over)
	return parsed(t, kv)
}

// authorize follows authURL at idp and returns the callback URL its
// redirect names.
func authorize(t *testing.T, idp *accesstest.IdP, authURL string) string {
	t.Helper()
	client := &http.Client{Transport: idp.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorize: %v", err)
	}
	_ = resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("authorize answered %s with no redirect", resp.Status)
	}
	return loc.String()
}
