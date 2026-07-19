package server

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeAssets is a minimal embedded-static stand-in: one asset, one entrypoint.
func fakeAssets() fstest.MapFS {
	return fstest.MapFS{
		"app.js":     &fstest.MapFile{Data: []byte("console.log(1)")},
		"login.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
}

func TestComputeStaticETags_hashes_every_file(t *testing.T) {
	t.Parallel()
	etags, err := computeStaticETags(fakeAssets())
	if err != nil {
		t.Fatalf("computeStaticETags: %v", err)
	}
	if len(etags) != 2 {
		t.Fatalf("etags = %d entries, want 2", len(etags))
	}
	for p, e := range etags {
		if len(e) < 4 || e[0] != '"' || e[len(e)-1] != '"' {
			t.Errorf("etag[%s] = %s, want quoted strong ETag", p, e)
		}
	}
}

func TestAssetETag_policy_boundaries(t *testing.T) {
	t.Parallel()
	etags := map[string]string{
		"app.js":     `"aaa"`,
		"app.js.gz":  `"ddd"`,
		"index.html": `"bbb"`,
		"login.html": `"ccc"`,
	}
	cases := []struct {
		path string
		want bool
	}{
		{"/app.js", true},
		{"/./app.js", true}, // path cleaning
		{"/index.html", false},
		{"/login.html", false},
		{"/", false},
		{"/api/state", false},
		{"/missing.js", false},
		{"/app.js.gz", false}, // gz siblings are a transport encoding, not resources
	}
	for _, tc := range cases {
		if _, ok := assetETag(etags, tc.path); ok != tc.want {
			t.Errorf("assetETag(%q) ok = %v, want %v", tc.path, ok, tc.want)
		}
	}
}

func TestCacheControlFor_header_selection(t *testing.T) {
	t.Parallel()
	etags := map[string]string{"app.js": `"abc123"`}
	mw := cacheControlFor(etags)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		path     string
		method   string
		wantCC   string
		wantETag string
	}{
		{"/app.js", http.MethodGet, "no-cache", `"abc123"`},
		{"/app.js", http.MethodHead, "no-cache", `"abc123"`},
		{"/app.js", http.MethodPost, "no-store", ""}, // non-read methods never get asset caching
		{"/api/state", http.MethodGet, "no-store", ""},
		{"/", http.MethodGet, "no-store", ""},
		{"/login.html", http.MethodGet, "no-store", ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != tc.wantCC {
			t.Errorf("%s %s: Cache-Control = %q, want %q", tc.method, tc.path, got, tc.wantCC)
		}
		if got := rec.Header().Get("ETag"); got != tc.wantETag {
			t.Errorf("%s %s: ETag = %q, want %q", tc.method, tc.path, got, tc.wantETag)
		}
	}
}

// The full conditional-request flow: the middleware pre-sets the ETag, and
// net/http's ServeFileFS (via serveAsset) answers If-None-Match with 304 and
// no body — the asset is revalidated, not re-downloaded.
func TestStaticAsset_304_revalidation(t *testing.T) {
	t.Parallel()
	assets := fakeAssets()
	etags, err := computeStaticETags(assets)
	if err != nil {
		t.Fatalf("computeStaticETags: %v", err)
	}
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, r, fs.FS(assets), etags, "app.js")
	})
	h := cacheControlFor(etags)(serve)

	// First fetch: 200 with body, ETag, no-cache.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first fetch status = %d, want 200", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first fetch carried no ETag")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("first fetch returned empty body")
	}

	// Revalidation: 304, empty body, same ETag.
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("304 carried a body (%d bytes)", rec2.Body.Len())
	}

	// A stale ETag (binary changed) gets the fresh 200.
	req3 := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req3.Header.Set("If-None-Match", `"stale"`)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK || rec3.Body.Len() == 0 {
		t.Errorf("stale-etag fetch = %d (%d bytes), want fresh 200 with body", rec3.Code, rec3.Body.Len())
	}
}

// gzAssets extends fakeAssets with a precompressed .gz sibling for app.js,
// mirroring what cmd/bundle emits.
func gzAssets(t *testing.T) (fstest.MapFS, string) {
	t.Helper()
	m := fakeAssets()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(m["app.js"].Data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	m["app.js.gz"] = &fstest.MapFile{Data: buf.Bytes()}
	return m, buf.String()
}

func TestServeAsset_gzip_negotiation(t *testing.T) {
	t.Parallel()
	assets, gzBody := gzAssets(t)
	etags, err := computeStaticETags(assets)
	if err != nil {
		t.Fatalf("computeStaticETags: %v", err)
	}
	if etags["app.js.gz"] == etags["app.js"] {
		t.Fatal("sibling and identity ETags must differ")
	}

	// A gzip-accepting client gets the sibling's bytes, the ORIGINAL
	// Content-Type, and the sibling's own ETag.
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	serveAsset(rec, req, assets, etags, "app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("gzip fetch status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want the original javascript type", ct)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
	if rec.Body.String() != gzBody {
		t.Errorf("body = %d bytes, want the %d precompressed bytes", rec.Body.Len(), len(gzBody))
	}
	if got, want := rec.Header().Get("ETag"), etags["app.js.gz"]; got != want {
		t.Errorf("ETag = %s, want the sibling's %s", got, want)
	}

	// The sibling's ETag revalidates with 304.
	req304 := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req304.Header.Set("Accept-Encoding", "gzip")
	req304.Header.Set("If-None-Match", etags["app.js.gz"])
	rec304 := httptest.NewRecorder()
	serveAsset(rec304, req304, assets, etags, "app.js")
	if rec304.Code != http.StatusNotModified {
		t.Errorf("gzip revalidation status = %d, want 304", rec304.Code)
	}

	// A client without gzip support gets the identity bytes.
	reqID := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	recID := httptest.NewRecorder()
	serveAsset(recID, reqID, assets, etags, "app.js")
	if recID.Code != http.StatusOK {
		t.Fatalf("identity fetch status = %d, want 200", recID.Code)
	}
	if got := recID.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("identity Content-Encoding = %q, want empty", got)
	}
	if recID.Body.String() != string(assets["app.js"].Data) {
		t.Errorf("identity body = %q, want the raw file", recID.Body.String())
	}
}

// An asset with no precompressed sibling serves identity bytes even to
// gzip-accepting clients (icons, and every asset in a from-source checkout
// where cmd/bundle hasn't run).
func TestServeAsset_no_sibling_serves_identity(t *testing.T) {
	t.Parallel()
	assets := fakeAssets()
	etags, err := computeStaticETags(assets)
	if err != nil {
		t.Fatalf("computeStaticETags: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	serveAsset(rec, req, assets, etags, "app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty (no sibling)", got)
	}
	if rec.Body.String() != string(assets["app.js"].Data) {
		t.Errorf("body = %q, want the raw file", rec.Body.String())
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
}
