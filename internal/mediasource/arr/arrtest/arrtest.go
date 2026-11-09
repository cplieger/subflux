// Package arrtest serves a Sonarr's or Radarr's recorded answers to the
// adapters' tests, in process or over a socket, and checks a recording
// directory holds no credential.
package arrtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
	"go.yaml.in/yaml/v4"
)

// Key is the API key every test entry sends; a placeholder, not a credential.
const Key = "test-key-arr"

// Config is an entry for app at url sending Key, decoded by arr.Schema as
// ParseConfigs would; publicURL "" leaves public_url unset.
func Config(t testing.TB, app, url, publicURL string) mediasource.Config {
	t.Helper()
	doc := "url: " + url + "\napi_key: " + Key + "\n"
	if publicURL != "" {
		doc += "public_url: " + publicURL + "\n"
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	vals, errs := arr.Schema(app).Decode(n.Content[0])
	if len(errs) > 0 {
		t.Fatalf("arrtest: %v", errs)
	}
	return mediasource.Config{Values: vals, Type: strings.ToLower(app)}
}

// answer is one recorded answer as testdata/<server>/answers.json lists it.
// Query "date=*" matches any date value.
type answer struct {
	File        string `json:"file"`
	Path        string `json:"path"`
	Query       string `json:"query"`
	ContentType string `json:"contentType"`
	Status      int    `json:"status"`
}

// Request is one request the server answered.
type Request struct{ Method, Target, Key string }

// Server answers each request with the handler set for its path, else the
// recorded answer matching its path and query, else a version-stamped 404
// as the arr itself writes one. Every recorded answer carries the recorded
// server's X-Application-Version. It is safe for concurrent use.
type Server struct {
	t        testing.TB
	dir      string
	version  string
	answers  []answer
	handlers map[string]http.HandlerFunc
	requests []Request
	mu       sync.Mutex
}

// Load reads dir's answers.json and the version its system-status.json
// recorded.
func Load(t testing.TB, dir string) *Server {
	t.Helper()
	s := &Server{t: t, dir: dir, handlers: map[string]http.HandlerFunc{}}
	if err := json.Unmarshal(s.Body("answers.json"), &s.answers); err != nil {
		t.Fatalf("arrtest: %s/answers.json: %v", dir, err)
	}
	var status struct{ Version string }
	if err := json.Unmarshal(s.Body("system-status.json"), &status); err != nil || status.Version == "" {
		t.Fatalf("arrtest: %s/system-status.json names no version: %v", dir, err)
	}
	s.version = status.Version
	return s
}

// Version is the version every answer carries.
func (s *Server) Version() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// SetVersion makes every later answer carry version, as an upgraded or
// swapped server's would.
func (s *Server) SetVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
}

// Body is a recorded file's bytes; call it from the test's goroutine.
func (s *Server) Body(file string) []byte {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, file))
	if err != nil {
		s.t.Fatalf("arrtest: %v", err)
	}
	return b
}

// Handle answers path with h from now on, before any recorded answer; a
// path ending in "/" answers its whole subtree, as in http.ServeMux, unless
// a longer handled path matches.
func (s *Server) Handle(path string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[path] = h
}

// Write sends body as the recorded server would: its version header, JSON
// when body is not empty.
func (s *Server) Write(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("X-Application-Version", s.Version())
	if len(body) > 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// Requests are the requests answered so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// ServeHTTP answers r.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Target: r.URL.RequestURI(), Key: r.Header.Get("X-Api-Key")})
	h := s.handler(r.URL.Path)
	s.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	s.Answer(w, r)
}

// handler is the handler for path; s.mu must be held.
func (s *Server) handler(path string) http.HandlerFunc {
	if h := s.handlers[path]; h != nil {
		return h
	}
	var best string
	for p := range s.handlers {
		if strings.HasSuffix(p, "/") && strings.HasPrefix(path, p) && len(p) > len(best) {
			best = p
		}
	}
	return s.handlers[best]
}

// Answer writes the recorded answer matching r's method, path and query,
// else a version-stamped 404; a Handle handler calls it to fall through.
func (s *Server) Answer(w http.ResponseWriter, r *http.Request) {
	for _, a := range s.answers {
		if a.Path != r.URL.Path || !queryMatches(a.Query, r.URL.RawQuery) || r.Method != http.MethodGet {
			continue
		}
		body, err := os.ReadFile(filepath.Join(s.dir, a.File))
		if err != nil {
			s.t.Errorf("arrtest: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Application-Version", s.Version())
		if a.ContentType != "" {
			w.Header().Set("Content-Type", a.ContentType)
		}
		w.WriteHeader(a.Status)
		_, _ = w.Write(body)
		return
	}
	s.Write(w, http.StatusNotFound, nil)
}

func queryMatches(recorded, got string) bool {
	if rest, ok := strings.CutPrefix(recorded, "date=*"); ok {
		date, tail, _ := strings.Cut(got, "&")
		return strings.HasPrefix(date, "date=") && strings.TrimPrefix(rest, "&") == tail
	}
	return recorded == got
}

// Start serves s on a loopback socket until t ends and returns its URL, for
// a client the test cannot hand Client to.
func (s *Server) Start(t testing.TB) string {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts.URL
}

// Client is an *http.Client reaching s in process, with no socket, so a
// test may run inside testing/synctest; any host reaches s.
func (s *Server) Client() *http.Client { return &http.Client{Transport: roundTripper{s}} }

type roundTripper struct{ s *Server }

func (rt roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
		r = r.Clone(r.Context())
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	rt.s.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

// credentialMarkers are what a recording must never hold.
var credentialMarkers = []string{"x-api-key", "apikey", "api_key", "passkey", "authkey", "torrent_pass", "token="}

// HoldsNoSecret fails t for each file under dir holding a credential marker.
func HoldsNoSecret(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("arrtest: no recording under %s: %v", dir, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lower := bytes.ToLower(b)
		for _, m := range credentialMarkers {
			if bytes.Contains(lower, []byte(m)) {
				t.Errorf("%s holds %q", f, m)
			}
		}
	}
}

// HistoryNamesListed fails t for each history record, in every recording
// under dir, whose idKey ("seriesId" or "movieId") names no root of that
// recording's list file, so a recording publishes no download of a series
// or movie it was not cut to.
func HistoryNamesListed(t *testing.T, dir, list, idKey string) {
	t.Helper()
	recordings, err := filepath.Glob(filepath.Join(dir, "*", "history-since.json"))
	if err != nil || len(recordings) == 0 {
		t.Fatalf("arrtest: no history recording under %s: %v", dir, err)
	}
	for _, f := range recordings {
		var roots []struct{ ID int }
		var history []map[string]any
		readJSON(t, filepath.Join(filepath.Dir(f), list), &roots)
		readJSON(t, f, &history)
		listed := map[float64]bool{}
		for _, r := range roots {
			listed[float64(r.ID)] = true
		}
		for _, h := range history {
			if id, ok := h[idKey].(float64); !ok || !listed[id] {
				t.Errorf("%s: record %v names %s %v, which %s does not list", f, h["id"], idKey, h[idKey], list)
			}
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
