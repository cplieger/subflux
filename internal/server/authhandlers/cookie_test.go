package authhandlers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func httpsRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	return r
}

func TestSessionCookieName_selects_by_scheme(t *testing.T) {
	t.Parallel()

	httpReq := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := SessionCookieName(httpReq); got != CookieNameHTTP {
		t.Errorf("HTTP request: cookie name = %q, want %q", got, CookieNameHTTP)
	}

	if got := SessionCookieName(httpsRequest()); got != CookieNameSecure {
		t.Errorf("TLS request: cookie name = %q, want %q (__Host- prefix)", got, CookieNameSecure)
	}

	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if got := SessionCookieName(proxied); got != CookieNameSecure {
		t.Errorf("X-Forwarded-Proto=https: cookie name = %q, want %q", got, CookieNameSecure)
	}
}

func TestSetSessionCookie_http_omits_secure(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil), "tok", 0)

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

func TestSetSessionCookie_https_sets_secure_host_cookie(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, httpsRequest(), "tok", 0)

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
			SetSessionCookie(rec, tc.makeReq(), "round-trip-token", 0)
			set := rec.Result().Cookies()
			if len(set) != 1 {
				t.Fatalf("want 1 Set-Cookie, got %d", len(set))
			}
			if set[0].Name != tc.wantName {
				t.Fatalf("set cookie name = %q, want %q", set[0].Name, tc.wantName)
			}

			readReq := tc.makeReq()
			readReq.AddCookie(set[0])
			if got := ReadSessionCookie(readReq); got != "round-trip-token" {
				t.Errorf("ReadSessionCookie = %q, want %q", got, "round-trip-token")
			}
		})
	}
}

func TestClearSessionCookie_expires_immediately(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil))

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

func TestReadSessionCookie_absent_returns_empty(t *testing.T) {
	t.Parallel()
	if got := ReadSessionCookie(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("ReadSessionCookie with no cookie = %q, want empty", got)
	}
}
