package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/go/health/v2"
	"github.com/cplieger/go/httpserver/v2"
	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/wirecodes"
)

// chainRig is the product handler over a snapshot with a 200, a panicking
// and a large JSON test route, and a stream and a video route.
func chainRig(t *testing.T, snap settings.Snapshot, seen func(*http.Request)) http.Handler {
	t.Helper()
	return handler(obs.New(), func() settings.Snapshot { return snap }, clientaddr.NewDetector(), func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/ok", func(w http.ResponseWriter, r *http.Request) {
			if seen != nil {
				seen(r)
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /api/panic", func(http.ResponseWriter, *http.Request) { panic("test route") })
		mux.HandleFunc("GET /api/big", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"pad":%q}`, strings.Repeat("a", 2048))
		})
		for path, typ := range map[string]string{"/api/stream": "text/event-stream", "/api/video": "video/mp4"} {
			mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", typ)
				_, _ = w.Write(bytes.Repeat([]byte("a"), 4096))
			})
		}
	})
}

// envelope decodes an error body, which must carry its X-Request-Id.
func envelope(t *testing.T, h http.Header, body []byte) httpwire.Envelope {
	t.Helper()
	e, err := httpwire.DecodeEnvelope(body)
	if err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	if id := h.Get(httpserver.HeaderRequestID); e.RequestID == "" || e.RequestID != id {
		t.Errorf("request_id %q, X-Request-Id %q; want one id", e.RequestID, id)
	}
	return e
}

// markRemediation gives code a marker remediation for t's span.
func markRemediation(t *testing.T, code string) string {
	t.Helper()
	was, ok := wirecodes.Catalog.Lookup(code)
	if !ok {
		t.Fatalf("%s is not in the catalog", code)
	}
	marker := "marker for " + code
	if err := wirecodes.Catalog.SetRemediation(code, marker); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wirecodes.Catalog.SetRemediation(code, was.Remediation) })
	return marker
}

// serveOps runs opsConfig's listener on a loopback port until t ends.
func serveOps(t *testing.T) string {
	t.Helper()
	ops, err := httpserver.ListenOps(t.Context(), opsConfig("127.0.0.1:0", obs.New(), []health.Check{{Signal: health.SignalFunc(func() bool { return true }), Reason: "up"}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- httpserver.RunWithOps(ctx, ops) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("RunWithOps: %v", err)
		}
	})
	return ops.Addr().String()
}

func TestRefusalsUseCatalog(t *testing.T) {
	hostMarker := markRemediation(t, "host_not_allowed")
	internalMarker := markRemediation(t, "internal_error")
	notFoundMarker := markRemediation(t, "not_found")
	h := chainRig(t, loaded(t, "server:\n  allowed_hosts: [nas.lan]\n"+validLanguages), nil)

	t.Run("a refused host", func(t *testing.T) {
		rec := ask(h, remotePeer, "other.lan", "/api/ok")
		if e := envelope(t, rec.Header(), rec.Body.Bytes()); rec.Code != http.StatusForbidden || e.Code != "host_not_allowed" || e.Remediation != hostMarker {
			t.Errorf("= %d %+v, want 403 host_not_allowed with the catalog's remediation", rec.Code, e)
		}
	})
	t.Run("a recovered panic", func(t *testing.T) {
		rec := ask(h, remotePeer, "nas.lan", "/api/panic")
		if e := envelope(t, rec.Header(), rec.Body.Bytes()); rec.Code != http.StatusInternalServerError || e.Code != "internal_error" || e.Remediation != internalMarker {
			t.Errorf("= %d %+v, want 500 internal_error with the catalog's remediation", rec.Code, e)
		}
	})
	t.Run("an unknown path on the ops listener", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+serveOps(t)+"/nothing", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if e := envelope(t, resp.Header, body); resp.StatusCode != http.StatusNotFound || e.Code != "not_found" || e.Remediation != notFoundMarker {
			t.Errorf("= %d %+v, want 404 not_found with the catalog's remediation", resp.StatusCode, e)
		}
	})
}

func TestEveryAnswerCarriesHeaders(t *testing.T) {
	h := chainRig(t, loaded(t, "server:\n  allowed_hosts: [nas.lan]\n"+validLanguages), nil)
	for name, c := range map[string]struct {
		host, path string
		code       int
	}{
		"a refused host":     {"other.lan", "/api/ok", http.StatusForbidden},
		"a recovered panic":  {"nas.lan", "/api/panic", http.StatusInternalServerError},
		"an unknown path":    {"nas.lan", "/api/nothing", http.StatusNotFound},
		"a test route's 200": {"nas.lan", "/api/ok", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			rec := ask(h, remotePeer, c.host, c.path)
			if rec.Code != c.code {
				t.Errorf("= %d, want %d", rec.Code, c.code)
			}
			hd := rec.Header()
			if csp := hd.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Errorf("Content-Security-Policy %q, want script-src 'self' and frame-ancestors 'none'", csp)
			}
			for header, want := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"Cache-Control":          "no-store",
			} {
				if got := hd.Get(header); got != want {
					t.Errorf("%s %q, want %q", header, got, want)
				}
			}
		})
	}
}

// TestProxiedClientOneAddress pins the access log's and a handler's read of a
// proxied client; accounts' TestClientIsTheMiddlewares pins the limiter key
// and the session's LastIP.
func TestProxiedClientOneAddress(t *testing.T) {
	logger, rec := capture.New()
	capture.SwapDefault(t, logger)
	var inner string
	h := chainRig(t, loaded(t, "server:\n  trusted_proxies: [192.0.2.7]\n"+validLanguages), func(r *http.Request) {
		if a, ok := clientaddr.From(r.Context()); ok {
			inner = a.IP().String()
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/ok", http.NoBody)
	req.RemoteAddr = remotePeer
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if inner != "203.0.113.9" {
		t.Errorf("the handler read %q, want the forwarded client 203.0.113.9", inner)
	}
	var logged []string
	for _, r := range rec.Records() {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "client_ip" {
				logged = append(logged, a.Value.String())
			}
			return true
		})
	}
	if len(logged) != 1 || logged[0] != "203.0.113.9" {
		t.Errorf("the access log's client_ip %v, want one line naming 203.0.113.9", logged)
	}
}

func TestGzipSkipsStreams(t *testing.T) {
	h := chainRig(t, loaded(t, validLanguages), nil)
	for path, want := range map[string]string{"/api/big": "gzip", "/api/stream": "", "/api/video": ""} {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req.RemoteAddr, req.Host = remotePeer, "nas.lan"
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding"); got != want {
			t.Errorf("%s Content-Encoding %q, want %q", path, got, want)
		}
	}
}
