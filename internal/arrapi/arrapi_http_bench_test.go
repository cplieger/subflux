package arrapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"subflux/internal/httputil"
)

func BenchmarkFetchAll_prealloc(b *testing.B) {
	// 100 small JSON items to exercise the pre-allocation heuristic.
	body := []byte(`[`)
	for i := range 100 {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, `{"id":1,"title":"item"}`...)
	}
	body = append(body, ']')

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	type item struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}

	c := &Client{
		httpClient:     srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test",
		defaultTimeout: 0,
	}

	b.ResetTimer()
	for range b.N {
		_, _ = fetchAll[item](context.Background(), c, "/test")
	}
}
