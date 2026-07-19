package authhandlers

import (
	"net"
	"net/http"
	"net/url"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
)

// Request authentication is the library's auth.Authenticator, assembled in the
// server package with subflux's three policies from this file: the session
// cookie (SessionCookie), the unauthorized response (UnauthorizedResponse),
// and live session timeouts (auth.WithTimeoutSource wired to the hot-reloaded
// config). This file is the whole subflux-specific auth glue; the chain
// runner, session verifier, API-key verifier, bypass, and activity throttling
// all come from the library.

// Compile-time assertion: the composite authstore satisfies the library's
// Authenticator store contract (session, activity, user, and API-key lookup).
var _ auth.AuthStore = authstore.AuthStore(nil)

// Session cookie names as they appear on the wire: the bare base name over
// plain HTTP (LAN, ip:port) and the __Host--prefixed Secure form over HTTPS.
// These are the two names SessionCookie's per-request posture alternates
// between; tests assert against them as subflux's observable cookie contract.
const (
	CookieNameHTTP   = "sfx_session"
	CookieNameSecure = "__Host-" + CookieNameHTTP
)

// SessionCookie is subflux's session-cookie configuration. Subflux serves both
// HTTP (LAN, ip:port) and HTTPS (behind a reverse proxy) from a single
// instance, so PosturePerRequest selects CookieNameSecure with the Secure flag
// over HTTPS and CookieNameHTTP without it over plain HTTP, per request.
// TrustForwardedHeaders honors X-Forwarded-Proto for the HTTPS decision; it is
// safe only because SanitizeForwardedProto strips that header from any request
// whose direct peer is not a configured trusted proxy.
var SessionCookie = auth.CookieConfig{
	Posture:               auth.PosturePerRequest,
	Name:                  CookieNameHTTP,
	TrustForwardedHeaders: true,
}

// UnauthorizedResponse writes subflux's unauthorized response: a 302 to /login
// for browsers and subflux's typed 401 JSON envelope otherwise. Installed on
// the library Authenticator via auth.WithUnauthorizedResponse so RequireAuth
// speaks subflux's error vocabulary.
func UnauthorizedResponse(w http.ResponseWriter, r *http.Request) {
	if auth.IsBrowserRequest(r) {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	api.UnauthorizedC(w, r, api.CodeAuthSessionRequired, auth.ErrUnauthenticated.Error())
}

// SanitizeForwardedProto strips the X-Forwarded-Proto header from any request
// whose direct peer is not in the configured trusted-proxy set (the same
// hot-reloadable set ClientIP consults). A trusted reverse proxy always
// overwrites the header for the requests it forwards, so the only surviving
// spoofing path is a direct connection — and for those the header must not
// influence the per-request cookie posture (a forged "https" over plain LAN
// HTTP would flip the session cookie to the __Host-/Secure form the browser
// then refuses to store or send). With no trusted proxies configured the
// header is stripped from every request: scheme detection falls back to the
// unspoofable r.TLS, and HTTPS-terminating deployments declare their proxy via
// trusted_proxies exactly as they already must for client-IP resolution.
func SanitizeForwardedProto(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !peerIsTrustedProxy(r) {
			r.Header.Del("X-Forwarded-Proto")
		}
		next.ServeHTTP(w, r)
	})
}

// peerIsTrustedProxy reports whether the request's direct socket peer is
// inside the configured trusted-proxy CIDR set. The zero configuration (no
// trusted proxies) trusts nothing.
func peerIsTrustedProxy(r *http.Request) bool {
	p := trustedProxies.Load()
	if p == nil || len(*p) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range *p {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
