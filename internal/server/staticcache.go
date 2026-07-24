package server

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/cplieger/webhttp"
)

// Static-asset cache policy (S3). The ETag/gzip/revalidation MECHANICS are
// webhttp.StaticHandler's: startup content-hash strong ETags (the embedded
// tree is immutable for the process lifetime), construction-time gzip at
// BestCompression kept only when smaller, Vary: Accept-Encoding, and
// distinct per-representation ETags with their own 304 handling via
// net/http. cmd/bundle no longer emits precompressed .gz siblings — the
// handler compresses the original bytes at the same level, so the embedded
// tree carries each asset exactly once and the ".gz paths are not
// addressable resources" hazard class is gone structurally.
//
// The app keeps only POLICY: assets revalidate (`no-cache`, the handler
// default — browsers spend one conditional GET per asset, answered 304
// until the binary changes), while API responses, the HTML entrypoints, and
// the SPA/login fallback stay `no-store` (per-user data must never be
// cached, and fresh HTML is what makes a new release take effect
// immediately). The auth-gated fallback routing itself lives in handleUI.

// mustStaticHandler builds the shared asset handler over the embedded tree,
// or panics: an unreadable embedded FS is a build defect, not a runtime
// condition (mirrors mustBuildCSPPolicy and mustSub).
func mustStaticHandler(fsys fs.FS) http.Handler {
	h, err := webhttp.StaticHandler(fsys)
	if err != nil {
		panic(fmt.Sprintf("static handler: %v", err))
	}
	return h
}

// staticAssets serves embedded static assets with ETag + no-cache + gzip
// variants. Only real, non-HTML, non-directory paths reach it (handleUI's
// servable gate), so the HTML entrypoints never pick up the asset policy.
var staticAssets = mustStaticHandler(staticSub)

// cacheControlFor returns the middleware that applies the scoped cache
// policy: every response is pre-set `no-store` (API, HTML entrypoints, SPA
// fallback — per-user data and always-fresh HTML), and the static-asset
// handler overrides it with ETag + `no-cache` for the assets it serves
// (headers set later replace earlier ones), so browsers revalidate assets
// with cheap 304s instead of re-downloading the frontend every page load.
func cacheControlFor() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}
