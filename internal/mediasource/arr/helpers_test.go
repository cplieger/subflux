package arr_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

// testKey is a placeholder, not a credential.
const testKey = "test-key-arr"

const v5 = "5.0.0.2635"

// upstream is an httptest Sonarr 5 recording each request's path and key;
// routes override a path's answer.
type upstream struct {
	srv    *httptest.Server
	routes map[string]http.HandlerFunc
	seen   []seenRequest
	mu     sync.Mutex
}

type seenRequest struct{ path, key string }

func newUpstream(t *testing.T) *upstream {
	u := &upstream{routes: map[string]http.HandlerFunc{}}
	u.srv = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.seen = append(u.seen, seenRequest{r.URL.Path, r.Header.Get("X-Api-Key")})
	h := u.routes[r.URL.Path]
	u.mu.Unlock()
	w.Header().Set("X-Application-Version", v5)
	if h != nil {
		h(w, r)
		return
	}
	switch r.URL.Path {
	case "/api":
		_, _ = io.WriteString(w, `{"current":"v5","deprecated":["v3"]}`)
	case "/api/v5/system/status":
		_, _ = io.WriteString(w, `{"appName":"Sonarr","version":"`+v5+`"}`)
	default:
		_, _ = io.WriteString(w, `[]`)
	}
}

func (u *upstream) on(path string, h http.HandlerFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.routes[path] = h
}

func (u *upstream) keys() []seenRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seenRequest(nil), u.seen...)
}

func redirectTo(location string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, location, http.StatusFound)
	}
}

func sonarrClient(t *testing.T, base string) *sonarr.Client {
	t.Helper()
	c, err := sonarr.New(base, testKey,
		sonarr.WithHTTPClient(&http.Client{}),
		sonarr.WithMaxAttempts(1),
		sonarr.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func classified(t *testing.T, err error) *mediasource.SourceError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want one to classify")
	}
	return arr.Classify(err)
}
