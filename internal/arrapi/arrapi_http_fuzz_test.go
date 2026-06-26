package arrapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/httputil"
)

// FuzzParseHTTPResponse feeds arbitrary byte slices as HTTP response bodies
// with varied non-OK status codes and asserts that get surfaces every one as
// a *StatusError carrying the originating code (cross-function consistency
// between get and classifyResponse), never panicking.
func FuzzParseHTTPResponse(f *testing.F) {
	f.Add([]byte("not found"), 404)
	f.Add([]byte(""), 500)
	f.Add([]byte(`{"message":"rate limited"}`), 429)
	f.Add([]byte("\x00\xff\xfe"), 502)

	f.Fuzz(func(t *testing.T, body []byte, code int) {
		// Scope to non-OK final status codes. 1xx informational responses are
		// consumed by net/http before the final response, so they never reach
		// classifyResponse; 200 is the success path.
		if code < 201 || code > 599 {
			return
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		c := &Client{
			httpClient:     srv.Client(),
			baseURL:        srv.URL,
			apiKey:         "test",
			defaultTimeout: 0,
		}
		resp, err := c.get(context.Background(), "/test")
		if resp != nil {
			resp.Body.Close()
		}
		// Every non-200 response must become a *StatusError with the code.
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("get() on HTTP %d returned error %T (%v), want *StatusError", code, err, err)
		}
		if se.Code != code {
			t.Errorf("get() StatusError.Code = %d, want %d", se.Code, code)
		}
	})
}

// FuzzFetchAllDecode feeds arbitrary JSON bodies to fetchAll to ensure the
// decode path never panics on malformed input.
func FuzzFetchAllDecode(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"id":1}]`))
	f.Add([]byte(`invalid`))
	f.Add([]byte(``))
	f.Add([]byte(`[`))

	type item struct {
		ID int `json:"id"`
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}))
		defer srv.Close()

		c := &Client{
			httpClient:     srv.Client(),
			baseURL:        srv.URL,
			apiKey:         "test",
			defaultTimeout: 0,
		}
		// Must not panic on arbitrary response bodies.
		_, _ = fetchAll[item](context.Background(), c, "/test")
	})
}

// FuzzIdPath checks the path-building invariants of idPath: the result is
// always base + decimal(id) + suffix.
func FuzzIdPath(f *testing.F) {
	f.Add("/api/v3/series/", 123, "")
	f.Add("/api/v3/episode/", 0, "/file")
	f.Add("", -1, "")
	f.Add("/x/", 999999, "/y")

	f.Fuzz(func(t *testing.T, base string, id int, suffix string) {
		result := idPath(base, id, suffix)
		if !strings.HasPrefix(result, base) {
			t.Errorf("idPath(%q, %d, %q) = %q, missing base prefix", base, id, suffix, result)
		}
		if !strings.HasSuffix(result, suffix) {
			t.Errorf("idPath(%q, %d, %q) = %q, missing suffix", base, id, suffix, result)
		}
		middle := result[len(base) : len(result)-len(suffix)]
		if want := strconv.Itoa(id); middle != want {
			t.Errorf("idPath(%q, %d, %q) = %q, middle = %q, want %q", base, id, suffix, result, middle, want)
		}
	})
}

// FuzzStatusErrorIsTransient checks that IsTransient is true exactly for 5xx
// and 429, and that the error message always carries the path (and body when
// non-empty).
func FuzzStatusErrorIsTransient(f *testing.F) {
	f.Add(200, "/api/test", "ok")
	f.Add(429, "/api/rate", "too many")
	f.Add(500, "/api/err", "internal")
	f.Add(404, "/api/miss", "not found")
	f.Add(503, "/api/down", "")

	f.Fuzz(func(t *testing.T, code int, path, body string) {
		if code < 100 || code > 599 {
			return
		}
		e := newStatusError(code, path, body)

		wantTransient := code >= 500 || code == 429
		if e.IsTransient() != wantTransient {
			t.Errorf("IsTransient() = %v for code %d, want %v", e.IsTransient(), code, wantTransient)
		}
		if !strings.Contains(e.Error(), path) {
			t.Errorf("Error() = %q, does not contain path %q", e.Error(), path)
		}
		if body != "" && !strings.Contains(e.Error(), body) {
			t.Errorf("Error() = %q, does not contain body %q", e.Error(), body)
		}
	})
}
