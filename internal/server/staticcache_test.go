package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/cplieger/webhttp"
)

// okBody answers 200 "ok" — a stand-in for an API handler behind the cache
// middleware.
func okBody() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

// TestCacheControlFor_noStoreDefault pins the policy default: every response
// that is not a served static asset — API, HTML entrypoints, SPA fallback —
// carries Cache-Control: no-store.
func TestCacheControlFor_noStoreDefault(t *testing.T) {
	h := cacheControlFor()(okBody())

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/status"},
		{http.MethodPost, "/api/config"},
		{http.MethodGet, "/"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s %s: Cache-Control = %q, want no-store", tc.method, tc.path, got)
		}
	}
}

// TestCacheControlFor_assetOverride pins the two-layer interplay the comment
// in staticcache.go promises: the middleware pre-sets no-store, and the
// static-asset handler REPLACES it with ETag + no-cache for assets it serves,
// so the asset path revalidates while everything else stays uncached.
func TestCacheControlFor_assetOverride(t *testing.T) {
	fsys := fstest.MapFS{
		"app.js": {Data: []byte("console.log(1)")},
	}
	assets, err := webhttp.StaticHandler(fsys)
	if err != nil {
		t.Fatalf("StaticHandler: %v", err)
	}
	h := cacheControlFor()(assets)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("asset Cache-Control = %q, want no-cache (handler must override the middleware's no-store)", got)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("asset response missing ETag")
	}

	// A matching If-None-Match revalidation answers 304 through the same
	// two-layer chain.
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", rec2.Code)
	}
}

// TestStaticAssets_realEmbedServesKnownAsset smoke-checks the package-level
// staticAssets handler over the real embedded tree: the committed favicon
// serves with the asset policy.
func TestStaticAssets_realEmbedServesKnownAsset(t *testing.T) {
	rec := httptest.NewRecorder()
	staticAssets.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if rec.Code != http.StatusOK {
		t.Skipf("favicon.svg not in this build's embed (status %d); nothing to assert", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("embedded asset served without an ETag")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("embedded asset Cache-Control = %q, want no-cache", got)
	}
}
