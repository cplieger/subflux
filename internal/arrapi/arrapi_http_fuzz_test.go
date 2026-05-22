package arrapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"subflux/internal/httputil"
)

// FuzzParseHTTPResponse feeds arbitrary byte slices as HTTP response bodies
// with varied status codes to exercise the StatusError construction path.
func FuzzParseHTTPResponse(f *testing.F) {
	f.Add([]byte("not found"), 404)
	f.Add([]byte(""), 500)
	f.Add([]byte(`{"message":"rate limited"}`), 429)
	f.Add([]byte("\x00\xff\xfe"), 502)

	f.Fuzz(func(t *testing.T, body []byte, code int) {
		if code < 100 || code > 599 || code == 200 {
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
		// Must not panic regardless of body content or status code.
		resp, _ := c.get(context.Background(), "/test")
		if resp != nil {
			resp.Body.Close()
		}
	})
}

// FuzzFetchAllDecode feeds arbitrary JSON bodies to fetchAll to ensure
// no panic on malformed input.
func FuzzFetchAllDecode(f *testing.F) {
	f.Add([]byte(`[]`), int64(0))
	f.Add([]byte(`[{"id":1}]`), int64(11))
	f.Add([]byte(`invalid`), int64(100))
	f.Add([]byte(``), int64(-1))
	f.Add([]byte(`[`), int64(9223372036854775807))

	type item struct {
		ID int `json:"id"`
	}

	f.Fuzz(func(t *testing.T, body []byte, contentLength int64) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if contentLength >= 0 {
				w.Header().Set("Content-Length", http.CanonicalHeaderKey(""))
			}
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
		// Must not panic.
		_, _ = fetchAll[item](context.Background(), c, "/test")
	})
}
