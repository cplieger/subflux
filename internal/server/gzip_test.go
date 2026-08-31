package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/webhttp/v2"
)

// gzipChain composes a handler the way buildHandler does — Recoverer outside,
// the gzip writer inside — so panic-unwind tests exercise the real contract
// between the two layers.
func gzipChain(h http.HandlerFunc) http.Handler {
	return webhttp.Chain(h,
		webhttp.Recoverer(webhttp.WithRecoverLogger(slog.New(slog.DiscardHandler))),
		gzipMW(),
	)
}

// gzipDo serves one request against h and returns the recorder. An empty
// acceptEncoding leaves the header unset.
func gzipDo(t *testing.T, h http.Handler, method, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, http.NoBody)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// gunzipAll decodes b as a single gzip stream and fails on any leftover or
// malformed trailing bytes — a second response appended after the stream
// (multistream mode reads it as another gzip member) turns into an error.
func gunzipAll(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader(body): %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading gzip body: %v", err)
	}
	if err := zr.Close(); err != nil {
		t.Fatalf("closing gzip reader: %v", err)
	}
	return out
}

// jsonBodyOf builds a syntactically valid JSON document of exactly n bytes
// (n >= 10), padded with a compressible run.
func jsonBodyOf(t *testing.T, n int) []byte {
	t.Helper()
	const prefix, suffix = `{"pad":"`, `"}`
	if n < len(prefix)+len(suffix) {
		t.Fatalf("jsonBodyOf(%d): too small for a JSON envelope", n)
	}
	b := make([]byte, 0, n)
	b = append(b, prefix...)
	for len(b) < n-len(suffix) {
		b = append(b, 'a')
	}
	return append(b, suffix...)
}

// jsonHandler writes body as application/json with an explicit Content-Length
// and status.
func jsonHandler(status int, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func TestGzipMW_body_over_floor_compresses_and_drops_content_length(t *testing.T) {
	t.Parallel()
	body := jsonBodyOf(t, gzipMinBytes+1)
	rec := gzipDo(t, gzipChain(jsonHandler(http.StatusOK, body)), http.MethodGet, "/api/x", "gzip")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want %q", ce, "gzip")
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q, want removed", cl)
	}
	if vary := rec.Header().Values("Vary"); !slicesContainsFold(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", vary)
	}
	if rec.Body.Len() >= len(body) {
		t.Errorf("compressed body = %d bytes, want smaller than the %d-byte original", rec.Body.Len(), len(body))
	}
	if got := gunzipAll(t, rec.Body.Bytes()); !bytes.Equal(got, body) {
		t.Errorf("decoded body = %d bytes, want the original %d bytes", len(got), len(body))
	}
}

func TestGzipMW_body_at_floor_stays_identity(t *testing.T) {
	t.Parallel()
	body := jsonBodyOf(t, gzipMinBytes)
	rec := gzipDo(t, gzipChain(jsonHandler(http.StatusOK, body)), http.MethodGet, "/api/x", "gzip")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none", ce)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("body = %d bytes, want the original %d bytes verbatim", rec.Body.Len(), len(body))
	}
	if vary := rec.Header().Values("Vary"); !slicesContainsFold(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding on the identity variant too", vary)
	}
}

func TestGzipMW_tiny_ok_envelope_stays_identity(t *testing.T) {
	t.Parallel()
	plain := httptest.NewRecorder()
	httpapi.Ok(plain)

	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		httpapi.Ok(w)
	}), http.MethodGet, "/api/x", "gzip")

	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none", ce)
	}
	if !bytes.Equal(rec.Body.Bytes(), plain.Body.Bytes()) {
		t.Errorf("body = %q, want the unwrapped envelope %q", rec.Body.String(), plain.Body.String())
	}
}

func TestGzipMW_multi_write_crossing_compresses_one_stream(t *testing.T) {
	t.Parallel()
	chunk := bytes.Repeat([]byte("a"), 400)
	want := bytes.Repeat(chunk, 3) // 1200 bytes, crosses on the third write
	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for range 3 {
			_, _ = w.Write(chunk)
		}
	}), http.MethodGet, "/api/x", "gzip")

	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q", ce, "gzip")
	}
	// Multistream(false) stops at the first member's end: a writer that
	// restarted a stream per Write would decode only the first chunk.
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader(body): %v", err)
	}
	zr.Multistream(false)
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading first gzip member: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("first gzip member = %d bytes, want the whole %d-byte body in one stream", len(got), len(want))
	}
}

func TestGzipMW_non_200_status_preserved_on_both_sides(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		size     int
		wantGzip bool
	}{
		{"small_error_identity", http.StatusNotFound, 64, false},
		{"large_error_compresses", http.StatusBadRequest, 2048, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := jsonBodyOf(t, tc.size)
			rec := gzipDo(t, gzipChain(jsonHandler(tc.status, body)), http.MethodGet, "/api/x", "gzip")

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			ce := rec.Header().Get("Content-Encoding")
			if tc.wantGzip {
				if ce != "gzip" {
					t.Fatalf("Content-Encoding = %q, want %q", ce, "gzip")
				}
				if got := gunzipAll(t, rec.Body.Bytes()); !bytes.Equal(got, body) {
					t.Errorf("decoded body = %d bytes, want %d", len(got), len(body))
				}
				return
			}
			if ce != "" {
				t.Errorf("Content-Encoding = %q, want none", ce)
			}
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Errorf("body = %d bytes, want %d verbatim", rec.Body.Len(), len(body))
			}
		})
	}
}

func TestGzipMW_panic_before_commit_discards_buffer_and_500s(t *testing.T) {
	t.Parallel()
	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("a"), gzipMinBytes)) // exactly at the floor: still buffered
		panic("boom before commit")
	}), http.MethodGet, "/api/x", "gzip")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json (Recoverer's envelope)", ct)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("internal_error")) {
		t.Errorf("body = %q, want Recoverer's internal_error envelope", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("aaaa")) {
		t.Errorf("body = %q, buffered handler bytes leaked onto the wire", rec.Body.String())
	}
}

func TestGzipMW_panic_after_commit_seals_stream_without_second_response(t *testing.T) {
	t.Parallel()
	body := jsonBodyOf(t, 2048)
	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body) // crosses the floor: compressed commit
		panic("boom after commit")
	}), http.MethodGet, "/api/x", "gzip")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the committed 200", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q", ce, "gzip")
	}
	// gunzipAll reads to EOF: an appended 500 envelope would fail the decode,
	// and a missing trailer (stream not sealed) would be an unexpected EOF.
	got := gunzipAll(t, rec.Body.Bytes())
	if !bytes.Equal(got, body) {
		t.Errorf("decoded body = %d bytes, want the %d bytes written before the panic", len(got), len(body))
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("internal_error")) {
		t.Errorf("body carries a second response after the gzip stream")
	}
}

func TestGzipMW_events_path_short_circuits_before_wrapping(t *testing.T) {
	t.Parallel()
	var got http.ResponseWriter
	h := webhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = w
	}), gzipMW())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got != http.ResponseWriter(rec) {
		t.Errorf("/api/events handler writer = %T, want the raw writer (unbuffered SSE)", got)
	}
}

func TestGzipMW_non_json_and_range_responses_stay_identity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		status      int
	}{
		{"range_206_video", "video/mp4", http.StatusPartialContent},
		{"range_206_json_status_gate", "application/json", http.StatusPartialContent},
		{"yaml", "text/yaml", http.StatusOK},
		{"event_stream", "text/event-stream", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := bytes.Repeat([]byte("b"), 2048)
			rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write(body)
			}), http.MethodGet, "/api/x", "gzip")

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none", ce)
			}
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Errorf("body = %d bytes, want %d verbatim", rec.Body.Len(), len(body))
			}
		})
	}
}

func TestGzipMW_request_gates_stay_identity(t *testing.T) {
	t.Parallel()
	body := jsonBodyOf(t, 2048)
	tests := []struct {
		name           string
		method         string
		acceptEncoding string
	}{
		{"q0_refusal", http.MethodGet, "gzip;q=0"},
		{"no_offer", http.MethodGet, ""},
		{"head", http.MethodHead, "gzip"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := gzipDo(t, gzipChain(jsonHandler(http.StatusOK, body)), tc.method, "/api/x", tc.acceptEncoding)

			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none", ce)
			}
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Errorf("body = %d bytes, want %d verbatim", rec.Body.Len(), len(body))
			}
		})
	}
}

func TestGzipMW_bodyless_statuses_pass_through(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
			}), http.MethodGet, "/api/x", "gzip")

			if rec.Code != status {
				t.Errorf("status = %d, want %d", rec.Code, status)
			}
			if ce := rec.Header().Get("Content-Encoding"); ce != "" {
				t.Errorf("Content-Encoding = %q, want none", ce)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body = %d bytes, want empty", rec.Body.Len())
			}
		})
	}
}

func TestGzipMW_pre_encoded_response_not_double_wrapped(t *testing.T) {
	t.Parallel()
	// Pre-compressed by the handler (the static handler's shape): the wrapper
	// must pass it through untouched rather than wrapping gzip in gzip.
	var pre bytes.Buffer
	zw := gzip.NewWriter(&pre)
	if _, err := zw.Write(jsonBodyOf(t, 2048)); err != nil {
		t.Fatalf("building pre-encoded fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing pre-encoded fixture: %v", err)
	}

	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pre.Bytes())
	}), http.MethodGet, "/api/x", "gzip")

	if got := rec.Header().Values("Content-Encoding"); len(got) != 1 || got[0] != "gzip" {
		t.Errorf("Content-Encoding = %q, want exactly one %q (the handler's own)", got, "gzip")
	}
	if !bytes.Equal(rec.Body.Bytes(), pre.Bytes()) {
		t.Errorf("body = %d bytes, want the handler's pre-encoded %d bytes verbatim", rec.Body.Len(), pre.Len())
	}
}

func TestGzipMW_flush_forces_identity(t *testing.T) {
	t.Parallel()
	first := bytes.Repeat([]byte("a"), 100)
	second := bytes.Repeat([]byte("b"), 2048)
	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(first)
		w.(http.Flusher).Flush()
		_, _ = w.Write(second) // over the floor, but the decision is already identity
	}), http.MethodGet, "/api/x", "gzip")

	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding = %q, want none after a Flush", ce)
	}
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
	want := append(append([]byte(nil), first...), second...)
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Errorf("body = %d bytes, want the %d flushed+streamed bytes verbatim", rec.Body.Len(), len(want))
	}
}

func TestGzipMW_vary_not_duplicated(t *testing.T) {
	t.Parallel()
	body := jsonBodyOf(t, 2048)
	rec := gzipDo(t, gzipChain(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding") // the static handler sets its own
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}), http.MethodGet, "/api/x", "gzip")

	count := 0
	for _, v := range rec.Header().Values("Vary") {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Accept-Encoding") {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("Vary names Accept-Encoding %d times, want exactly once (values %q)", count, rec.Header().Values("Vary"))
	}
}

func TestAcceptsGzip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"GZIP", true},
		{"gzip;q=0", false},
		{"gzip;Q=0", false}, // parameter names are case-insensitive (RFC 9110)
		{"gzip;q=0.5", true},
		{"gzip;q=zz", true}, // malformed q on a named coding reads as accepting
		{"br", false},
		{"br, gzip", true},
		{"*", true},
		{"*;q=0", false},
		{"gzip;q=0, *", false}, // the explicit refusal wins over the wildcard
		{"identity;q=1, *;q=0.5", true},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/x", http.NoBody)
		if tc.header != "" {
			req.Header.Set("Accept-Encoding", tc.header)
		}
		if got := acceptsGzip(req); got != tc.want {
			t.Errorf("acceptsGzip(Accept-Encoding: %q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// TestBuildHandler_gzips_large_json pins the WIRING: the gzip writer is in the
// production chain built by buildHandler, inside Recoverer, and a large JSON
// response leaves the server compressed. The raw transport (DisableCompression)
// keeps net/http from transparently stripping the evidence.
func TestBuildHandler_gzips_large_json(t *testing.T) {
	t.Parallel()
	s := &Server{metrics: obs.New()}
	body := jsonBodyOf(t, 4096)

	mux := http.NewServeMux()
	mux.Handle("GET /api/big", jsonHandler(http.StatusOK, body))

	ts := httptest.NewServer(s.buildHandler(mux))
	defer ts.Close()

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/big", http.NoBody)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/big: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want %q", ce, "gzip")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if got := gunzipAll(t, raw); !bytes.Equal(got, body) {
		t.Errorf("decoded body = %d bytes, want the original %d bytes", len(got), len(body))
	}
}

// slicesContainsFold reports whether any Vary value names target,
// case-insensitively, including inside a comma-joined list.
func slicesContainsFold(values []string, target string) bool {
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), target) {
				return true
			}
		}
	}
	return false
}
