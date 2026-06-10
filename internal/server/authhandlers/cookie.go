package authhandlers

import "net/http"

// Session cookie names. Subflux supports both HTTP (LAN, ip:port) and HTTPS
// (behind Caddy) access from a single instance, so the cookie name and Secure
// flag are chosen per request rather than fixed at deploy time. This is why
// the library's deploy-time CookieConfig posture is not used here.
const (
	CookieNameSecure = "__Host-sfx_session"
	CookieNameHTTP   = "sfx_session"
)

// isHTTPS reports whether the request arrived over HTTPS, honoring the
// X-Forwarded-Proto header set by the reverse proxy.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// SessionCookieName returns the cookie name appropriate for the request scheme.
func SessionCookieName(r *http.Request) string {
	if isHTTPS(r) {
		return CookieNameSecure
	}
	return CookieNameHTTP
}

// SetSessionCookie sets the session cookie on the response, selecting the
// __Host- prefixed Secure cookie over HTTPS and the bare cookie over HTTP.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	secure := isHTTPS(r)
	name := CookieNameHTTP
	if secure {
		name = CookieNameSecure
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is conditional for LAN HTTP support; HttpOnly+SameSite always set
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie clears the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	SetSessionCookie(w, r, "", -1)
}

// ReadSessionCookie reads the session token from the request cookie.
func ReadSessionCookie(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName(r))
	if err != nil {
		return ""
	}
	return c.Value
}
