package server

import (
	"net/http"
	"net/url"
	"testing"
)

// mustReq builds a minimal *http.Request carrying only a parsed URL, which is
// all posterRedirectGuard (and ssrf.SafeRedirectPolicy) inspect.
func mustReq(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return &http.Request{URL: u}
}

// TestPosterRedirectGuard verifies the poster proxy redirect policy directly
// (no live redirect): a same-host arr self-redirect is allowed even when the
// arr is private, a cross-host redirect to a public CDN over https is allowed,
// and a cross-host redirect to any internal address is blocked. The guard is
// exercised with crafted req/via because ssrf treats a live httptest server
// (127.0.0.1) as non-public.
func TestPosterRedirectGuard(t *testing.T) {
	// The arr origin is legitimately private; it must remain reachable.
	const arrOrigin = "http://192.168.1.10:8989/api/v3/mediacover/12/poster.jpg"

	tests := []struct {
		name      string
		target    string // redirect target (becomes req.URL)
		wantBlock bool
	}{
		{
			name:      "cross-host redirect to cloud metadata IP is blocked",
			target:    "https://169.254.169.254/latest/meta-data/",
			wantBlock: true,
		},
		{
			name:      "cross-host redirect to private LAN host is blocked",
			target:    "https://10.0.0.5/internal",
			wantBlock: true,
		},
		{
			name:      "cross-host redirect to loopback is blocked",
			target:    "http://127.0.0.1:8080/admin",
			wantBlock: true,
		},
		{
			name:      "same-host arr self-redirect (private) is allowed",
			target:    "http://192.168.1.10:8989/MediaCover/12/poster-500.jpg",
			wantBlock: false,
		},
		{
			name:      "cross-host redirect to public CDN over https is allowed",
			target:    "https://image.tmdb.org/t/p/w500/abc123.jpg",
			wantBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			via := []*http.Request{mustReq(t, arrOrigin)}
			req := mustReq(t, tc.target)

			err := posterRedirectGuard(req, via)

			if tc.wantBlock && err == nil {
				t.Errorf("posterRedirectGuard(target=%q) = nil, want blocked", tc.target)
			}
			if !tc.wantBlock && err != nil {
				t.Errorf("posterRedirectGuard(target=%q) = %v, want allowed", tc.target, err)
			}
		})
	}
}

// TestPosterRedirectGuard_hopCap verifies the redirect chain is capped even for
// an otherwise-allowed same-host target.
func TestPosterRedirectGuard_hopCap(t *testing.T) {
	origin := mustReq(t, "http://192.168.1.10:8989/poster.jpg")
	via := make([]*http.Request, posterMaxRedirects)
	for i := range via {
		via[i] = origin
	}
	req := mustReq(t, "http://192.168.1.10:8989/poster-final.jpg")

	if err := posterRedirectGuard(req, via); err == nil {
		t.Errorf("posterRedirectGuard with %d prior hops = nil, want too-many-redirects error",
			posterMaxRedirects)
	}
}
