package server

import (
	"bufio"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/server/events"
)

// TestSSEStream_outlives_the_server_read_timeout pins the property the whole
// SSE surface rests on: newHTTPServer arms a whole-request ReadTimeout, and a
// stream served through the production middleware chain must not be ended by
// it. It runs the real http.Server on a real listener, waits past the
// configured ReadTimeout, then publishes and requires the frame to arrive: a
// stream whose request context the read deadline had cancelled would have
// ended, and the read would answer EOF instead. Wall-clock bound to the
// production value; skipped under -short.
func TestSSEStream_outlives_the_server_read_timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits past the production ReadTimeout")
	}
	s := &Server{metrics: obs.New(), events: events.New(0, nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		events.Handle(s.events, w, r)
	})
	srv := newHTTPServer(s.buildHandler(mux))
	if srv.ReadTimeout <= 0 {
		t.Fatalf("newHTTPServer ReadTimeout = %v, want a positive value; the test has nothing to outlive", srv.ReadTimeout)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/api/events", http.NoBody)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("SSE-Wire", "1")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	waitForLine(t, lines, `"verdict":`, 5*time.Second)

	// Past the read deadline nothing has been written to the stream yet
	// (the keepalive is 15s), so what keeps it open is the cleared deadline.
	time.Sleep(srv.ReadTimeout + 500*time.Millisecond)
	s.events.PublishNotify(events.NotifyInfo, "after-read-timeout")
	waitForLine(t, lines, `"after-read-timeout"`, 5*time.Second)
}

// waitForLine reads stream lines until one contains sub, failing when the
// stream ends (the server closed it) or the deadline passes.
func waitForLine(t *testing.T, lines <-chan string, sub string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream ended before a line containing %q arrived", sub)
			}
			if strings.Contains(l, sub) {
				return
			}
		case <-deadline:
			t.Fatalf("no line containing %q within %v", sub, within)
		}
	}
}
