package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	pathpkg "path"
	"strings"
)

// Static-asset cache policy (S3). The embedded assets (the bundled app.js /
// login.js, their shared chunks, the CSS bundles, icons) are identical for
// every user and change only with the binary, yet the former blanket
// `Cache-Control: no-store` forced every page load to re-download all of
// them. Assets now carry a startup-computed strong ETag plus `no-cache`
// (revalidate-always): browsers keep a copy and spend one conditional GET
// per asset, answered 304 until the binary changes. API responses and the
// HTML entrypoints keep `no-store` — per-user data must never be cached, and
// fresh HTML is what makes a new release take effect immediately.
//
// Compression: cmd/bundle writes precompressed .gz siblings for every
// .js/.css/.map it emits. When the client accepts gzip and a sibling exists,
// serveAsset hands out the sibling's bytes with Content-Encoding: gzip and
// the ORIGINAL file's Content-Type; the sibling has its own ETag (a
// different representation per RFC 9110), and net/http still handles
// If-None-Match/ranges against whichever representation is served. The .gz
// paths themselves are not directly addressable (they fall through to the
// SPA/login fallback) — the sibling is a transport encoding, not a resource.

// computeStaticETags walks the embedded static tree once and hashes every
// file into a strong ETag, keyed by the URL-relative path ("app.js",
// "vendor/cplieger-actions/index.js"). Same compute-at-startup pattern as
// the CSP hash extraction: the embedded FS is immutable for the process
// lifetime, so the map never changes after boot.
func computeStaticETags(fsys fs.FS) (map[string]string, error) {
	etags := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", p, rerr)
		}
		sum := sha256.Sum256(data)
		etags[p] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	if err != nil {
		return nil, err
	}
	return etags, nil
}

// mustComputeStaticETags is computeStaticETags-or-panic, mirroring
// mustBuildCSPPolicy: an unreadable embedded FS is a build defect, not a
// runtime condition.
func mustComputeStaticETags(fsys fs.FS) map[string]string {
	etags, err := computeStaticETags(fsys)
	if err != nil {
		panic(fmt.Sprintf("static etags: %v", err))
	}
	return etags
}

// staticETags holds the process-lifetime ETag per embedded asset path.
var staticETags = mustComputeStaticETags(staticSub)

// assetETag resolves a request path to its asset ETag. The HTML entrypoints
// are deliberately NOT assets here: they stay no-store so a new release's
// script graph is fetched fresh on the next load, which is exactly what lets
// the ETag'd assets turn over safely. The precompressed .gz siblings are
// excluded too — they are served as the gzip representation of their base
// asset (serveAsset), never as resources of their own.
func assetETag(etags map[string]string, urlPath string) (string, bool) {
	p := strings.TrimPrefix(pathpkg.Clean(urlPath), "/")
	if p == "" || p == indexHTML || p == loginHTML || strings.HasSuffix(p, ".gz") {
		return "", false
	}
	etag, ok := etags[p]
	return etag, ok
}

// cacheControlFor returns the middleware that applies the scoped cache
// policy: ETag + `no-cache` for embedded static assets on GET/HEAD,
// `no-store` for everything else (API, HTML entrypoints, SPA fallback).
// http.ServeFileFS in handleUI evaluates If-None-Match against the ETag this
// middleware pre-sets, so conditional requests get their 304 (and correct
// range handling) from net/http itself.
func cacheControlFor(etags map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				if etag, ok := assetETag(etags, r.URL.Path); ok {
					h := w.Header()
					h.Set("ETag", etag)
					h.Set("Cache-Control", "no-cache")
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}

// serveAsset serves one embedded static asset, preferring the precompressed
// .gz sibling for gzip-accepting clients. cacheControlFor has already set
// the identity ETag + no-cache; when the sibling is chosen this overrides
// the ETag with the sibling's own (the two representations must stay
// distinct for caches) and pins Content-Type to the original extension
// (ServeFileFS would otherwise sniff the gzip bytes). net/http's ServeFileFS
// evaluates If-None-Match against the pre-set ETag, so conditional requests
// get their 304 (and correct range handling) from the stdlib either way.
func serveAsset(w http.ResponseWriter, r *http.Request, fsys fs.FS, etags map[string]string, p string) {
	h := w.Header()
	h.Add("Vary", "Accept-Encoding")

	serve := p
	if acceptsGzip(r) {
		if gzETag, ok := etags[p+".gz"]; ok {
			if ct := mime.TypeByExtension(pathpkg.Ext(p)); ct != "" {
				h.Set("Content-Type", ct)
			}
			h.Set("Content-Encoding", "gzip")
			h.Set("ETag", gzETag)
			serve = p + ".gz"
		}
	}
	http.ServeFileFS(w, r, fsys, serve)
}

// acceptsGzip reports whether the request advertises gzip support. A simple
// substring match over Accept-Encoding is sufficient here: every real
// browser sends a plain token list, and a q=0 opt-out of gzip is not a thing
// browsers do for static assets.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}
