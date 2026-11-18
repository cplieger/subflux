package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/go/httpserver/v2"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/settings"
)

// loaded is the snapshot settings.Load gives config text.
func loaded(t *testing.T, text string) settings.Snapshot {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings.Load(path, registries(providers.New(), filepath.Join(dir, "subflux.bolt")))
}

// ask sends method path from peer with host through h.
func ask(h http.Handler, peer, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.RemoteAddr, req.Host = peer, host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const (
	remotePeer   = "192.0.2.7:51000"
	loopbackPeer = "127.0.0.1:51000"
)

func TestHostGate(t *testing.T) {
	listed := loaded(t, "server:\n  allowed_hosts: [nas.lan]\n"+validLanguages)
	if listed.Mode() != settings.ModeActive {
		t.Fatalf("the listed fixture is %v, want Active", listed.Mode())
	}
	failed := loaded(t, "server:\n  allowed_hosts: [nas.lan, 'bad host']\n"+validLanguages)
	if _, ok := failed.Server(); ok {
		t.Fatal("the failed fixture's server section parsed")
	}
	cases := []struct {
		snap        settings.Snapshot
		name        string
		peer, host  string
		wantAllowed bool
	}{
		{name: "listed host", snap: listed, peer: remotePeer, host: "nas.lan:8374", wantAllowed: true},
		{name: "unlisted host", snap: listed, peer: remotePeer, host: "other.lan:8374"},
		{name: "loopback peer and host", snap: listed, peer: loopbackPeer, host: "127.0.0.1:8374", wantAllowed: true},
		{name: "public peer naming loopback", snap: listed, peer: remotePeer, host: "127.0.0.1:8374"},
		{name: "loopback peer naming another host", snap: listed, peer: loopbackPeer, host: "evil.example"},
		{name: "empty list", snap: loaded(t, validLanguages), peer: remotePeer, host: "other.lan", wantAllowed: true},
		{name: "failed section keeps its parsed entries", snap: failed, peer: remotePeer, host: "nas.lan", wantAllowed: true},
		{name: "failed section exempts no loopback", snap: failed, peer: loopbackPeer, host: "127.0.0.1:8374"},
		{name: "unreadable file denies every host", snap: loaded(t, "- not a mapping\n"), peer: loopbackPeer, host: "127.0.0.1:8374"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := hostGate(func() settings.Snapshot { return c.snap }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			}))
			rec := ask(h, c.peer, c.host, "/")
			if c.wantAllowed {
				if rec.Code != http.StatusTeapot {
					t.Errorf("= %d, want the request passed on", rec.Code)
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("= %d, want 403", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type %q, want JSON", ct)
			}
			var body httpserver.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "host_not_allowed" || body.Message != "host not allowed" {
				t.Errorf("code %q message %q, want host_not_allowed and host not allowed", body.Code, body.Message)
			}
			if !strings.Contains(body.Remediation, "server.allowed_hosts") {
				t.Errorf("remediation %q, want it to name server.allowed_hosts", body.Remediation)
			}
			if id := rec.Header().Get(httpserver.HeaderRequestID); body.RequestID == "" || body.RequestID != id {
				t.Errorf("request_id %q, X-Request-ID %q, want one non-empty id", body.RequestID, id)
			}
		})
	}
}

func TestHostGateReadsSnapshotPerRequest(t *testing.T) {
	var snap atomic.Pointer[settings.Snapshot]
	listed, open := loaded(t, "server:\n  allowed_hosts: [nas.lan]\n"+validLanguages), loaded(t, validLanguages)
	snap.Store(&listed)
	h := handler(obs.New(), nil, func() settings.Snapshot { return *snap.Load() }, nil)
	if code := ask(h, remotePeer, "other.lan", "/").Code; code != http.StatusForbidden {
		t.Fatalf("before the save = %d, want 403", code)
	}
	snap.Store(&open)
	if code := ask(h, remotePeer, "other.lan", "/").Code; code != http.StatusOK {
		t.Errorf("after the save emptied the list = %d, want 200", code)
	}
}

func TestHostGateCoversEveryRoute(t *testing.T) {
	listed := loaded(t, "server:\n  allowed_hosts: [nas.lan]\n"+validLanguages)
	m := obs.New()
	h := handler(m, nil, func() settings.Snapshot { return listed }, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/probe", func(http.ResponseWriter, *http.Request) {})
	})
	for _, path := range []string{"/", "/api/health", "/metrics", "/api/probe", "/elsewhere"} {
		if code := ask(h, remotePeer, "other.lan", path).Code; code != http.StatusForbidden {
			t.Errorf("%s from an unlisted host = %d, want 403", path, code)
		}
		if code := ask(h, remotePeer, "nas.lan", path).Code; code == http.StatusForbidden {
			t.Errorf("%s from a listed host = 403", path)
		}
	}
	rec := httptest.NewRecorder()
	m.Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `status="403"`) {
		t.Errorf("no refusal recorded in http_requests_total:\n%s", rec.Body.String())
	}
	refused := ask(h, remotePeer, "other.lan", "/")
	var body httpserver.ErrorResponse
	if err := json.Unmarshal(refused.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if id := refused.Header().Get(httpserver.HeaderRequestID); id == "" || body.RequestID != id {
		t.Errorf("request_id %q, want the access log's X-Request-ID %q", body.RequestID, id)
	}
}

func TestBootSnapshotRefusalLogsThroughTheProcessLogger(t *testing.T) {
	out := &syncBuffer{}
	b := boot(t, "server:\n  allowed_hosts: [nas.lan]\n  log_level: warn\n"+validLanguages, func(c *Config) { c.LogOutput = out })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+b.addr+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an unlisted Host = %d, want 403", resp.StatusCode)
	}
	refusals := 0
	for line := range strings.Lines(out.String()) {
		var rec struct{ Level, Msg, Host, Remote string }
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Msg != "httpserver: host not allowed" {
			continue
		}
		refusals++
		if rec.Level != "WARN" || rec.Host != "evil.example" || !strings.HasPrefix(rec.Remote, "127.0.0.1:") {
			t.Errorf("refusal line %+v, want WARN with host evil.example and the loopback remote", rec)
		}
	}
	if refusals != 1 {
		t.Errorf("%d refusal lines at log_level warn, want 1:\n%s", refusals, out.String())
	}
}
