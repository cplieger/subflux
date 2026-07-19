package authhandlers

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func httpsRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	return r
}

// TestSessionCookie_name_selects_by_scheme pins subflux's observable cookie
// contract through the library config: bare name over HTTP, __Host- form over
// TLS, and (config-level) over a forwarded-proto HTTPS signal. The
// forwarded-header case relies on SanitizeForwardedProto having stripped
// spoofed values before this layer; see the sanitizer tests below.
func TestSessionCookie_name_selects_by_scheme(t *testing.T) {
	t.Parallel()

	httpReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := SessionCookie.CookieName(httpReq); got != CookieNameHTTP {
		t.Errorf("HTTP request: cookie name = %q, want %q", got, CookieNameHTTP)
	}

	if got := SessionCookie.CookieName(httpsRequest()); got != CookieNameSecure {
		t.Errorf("TLS request: cookie name = %q, want %q (__Host- prefix)", got, CookieNameSecure)
	}

	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if got := SessionCookie.CookieName(proxied); got != CookieNameSecure {
		t.Errorf("X-Forwarded-Proto=https: cookie name = %q, want %q", got, CookieNameSecure)
	}
}

func TestSessionCookie_http_omits_secure(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SessionCookie.SetCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil), "tok", 0)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want exactly 1 Set-Cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieNameHTTP {
		t.Errorf("name = %q, want %q", c.Name, CookieNameHTTP)
	}
	if c.Secure {
		t.Error("Secure must be false over plain HTTP (LAN support)")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must always be set")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want \"/\"", c.Path)
	}
}

func TestSessionCookie_https_sets_secure_host_cookie(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SessionCookie.SetCookie(rec, httpsRequest(), "tok", 0)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want exactly 1 Set-Cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieNameSecure {
		t.Errorf("name = %q, want %q (__Host- prefix over HTTPS)", c.Name, CookieNameSecure)
	}
	if !c.Secure {
		t.Error("Secure must be set over HTTPS")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must always be set")
	}
}

func TestSessionCookie_roundtrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		makeReq  func() *http.Request
		wantName string
	}{
		{"http", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) }, CookieNameHTTP},
		{"https", httpsRequest, CookieNameSecure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			SessionCookie.SetCookie(rec, tc.makeReq(), "round-trip-token", 0)
			set := rec.Result().Cookies()
			if len(set) != 1 {
				t.Fatalf("want 1 Set-Cookie, got %d", len(set))
			}
			if set[0].Name != tc.wantName {
				t.Fatalf("set cookie name = %q, want %q", set[0].Name, tc.wantName)
			}

			readReq := tc.makeReq()
			readReq.AddCookie(set[0])
			if got := SessionCookie.ReadCookie(readReq); got != "round-trip-token" {
				t.Errorf("ReadCookie = %q, want %q", got, "round-trip-token")
			}
		})
	}
}

func TestSessionCookie_clear_expires_immediately(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SessionCookie.ClearCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 Set-Cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative (immediate expiry)", cookies[0].MaxAge)
	}
	if cookies[0].Value != "" {
		t.Errorf("Value = %q, want empty", cookies[0].Value)
	}
}

func TestSessionCookie_read_absent_returns_empty(t *testing.T) {
	t.Parallel()
	if got := SessionCookie.ReadCookie(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("ReadCookie with no cookie = %q, want empty", got)
	}
}

// --- SanitizeForwardedProto ---

// sanitizeProbe runs SanitizeForwardedProto over a request with the given
// remote addr and an X-Forwarded-Proto: https header, returning the header
// value the inner handler observed.
func sanitizeProbe(t *testing.T, remoteAddr string) string {
	t.Helper()
	var seen string
	h := SanitizeForwardedProto(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Forwarded-Proto")
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	r.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(httptest.NewRecorder(), r)
	return seen
}

// setTrustedProxiesForTest installs a trusted-proxy set and restores the
// previous set on cleanup. The set is process-global (hot-reload semantics),
// so tests touching it must not run in parallel with each other.
func setTrustedProxiesForTest(t *testing.T, cidrs ...string) {
	t.Helper()
	prev := trustedProxies.Load()
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", c, err)
		}
		nets = append(nets, n)
	}
	SetTrustedProxies(nets)
	t.Cleanup(func() {
		if prev != nil {
			trustedProxies.Store(prev)
		} else {
			SetTrustedProxies(nil)
		}
	})
}

// TestSanitizeForwardedProto_strips_from_untrusted_peer confirms the spoofing
// fix: a direct (non-proxy) client cannot influence the scheme decision, so a
// forged X-Forwarded-Proto over LAN HTTP can no longer flip the session
// cookie to the __Host-/Secure form.
func TestSanitizeForwardedProto_strips_from_untrusted_peer(t *testing.T) {
	setTrustedProxiesForTest(t, "10.0.0.0/8")
	if got := sanitizeProbe(t, "192.168.1.50:41234"); got != "" {
		t.Errorf("header after sanitize from untrusted peer = %q, want stripped", got)
	}
}

// TestSanitizeForwardedProto_preserves_from_trusted_proxy confirms the
// header survives when the direct peer is a configured trusted proxy (which
// always overwrites the header for the requests it forwards).
func TestSanitizeForwardedProto_preserves_from_trusted_proxy(t *testing.T) {
	setTrustedProxiesForTest(t, "10.0.0.0/8")
	if got := sanitizeProbe(t, "10.0.0.2:443"); got != "https" {
		t.Errorf("header after sanitize from trusted proxy = %q, want %q", got, "https")
	}
}

// TestSanitizeForwardedProto_default_trusts_nothing confirms the zero
// configuration strips the header from every request.
func TestSanitizeForwardedProto_default_trusts_nothing(t *testing.T) {
	setTrustedProxiesForTest(t) // empty set
	if got := sanitizeProbe(t, "10.0.0.2:443"); got != "" {
		t.Errorf("header with no trusted proxies = %q, want stripped", got)
	}
}
