package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cplieger/ssrf"
)

// posterMaxRedirects caps the redirect chain the poster proxy will follow.
const posterMaxRedirects = 5

// newPosterClient builds the HTTP client used by the poster-image proxy
// (HandlePreviewPoster). The origin is the operator-configured arr
// (Sonarr/Radarr), which is legitimately PRIVATE (e.g. http://sonarr:8989 or a
// LAN IP). The transport is therefore left as the net/http default and is NOT
// swapped for ssrf.SafeTransport, whose public-only / bare-hostname-reject dial
// would block the real arr. SSRF defense is applied at the redirect boundary
// instead — see posterRedirectGuard.
func newPosterClient() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: posterRedirectGuard,
	}
}

// posterRedirectGuard is the poster proxy's CheckRedirect policy. The arr's
// MediaCover endpoint commonly 3xx-redirects a poster to a third-party public
// CDN (TMDB, fanart.tv), which is legitimate, but a compromised arr or poisoned
// metadata could instead redirect to an internal host (e.g. 169.254.169.254) —
// an SSRF vector, since the bare default transport follows it.
//
// The guard allows a redirect whose host equals the ORIGINAL request host (the
// arr self-redirecting, even to a private/LAN address) and requires any
// CROSS-HOST redirect to pass ssrf's public-only validation (blocking redirects
// to internal hosts). via[0] is the original client request.
func posterRedirectGuard(req *http.Request, via []*http.Request) error {
	if len(via) >= posterMaxRedirects {
		return errors.New("poster proxy: stopped after too many redirects")
	}
	// Same-origin host (arr self-redirect): allowed even when private/LAN.
	if len(via) > 0 && strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return nil
	}
	// Cross-host: must resolve to a public host, blocking redirects to
	// internal/loopback/link-local addresses.
	return ssrf.SafeRedirectPolicy(nil)(req, via)
}
