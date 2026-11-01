package app

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/cplieger/health"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/webhttp/v3"
)

//go:embed placeholder.html
var placeholder []byte

func handler(m *obs.Metrics, marker health.Signal, routes func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/health", health.Handler(marker))
	mux.Handle("GET /metrics", m.Handler())
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		_, _ = w.Write(placeholder)
	})
	if routes != nil {
		routes(mux)
	}
	logOpt, recoverOpt := httpMetrics(m)
	return webhttp.Chain(mux, webhttp.Logging(logOpt), webhttp.Recoverer(recoverOpt))
}

func httpMetrics(m *obs.Metrics) (webhttp.LogOption, webhttp.RecoverOption) {
	return webhttp.WithRecordRouteMetric(m.RecordHTTP),
		webhttp.WithPanicHook(func(any, []byte) { m.RecordPanic() })
}

// httpComponent serves srv on ln until its stop shuts srv down. Serve
// returning while the component is live (a failed Accept) is a cause; the
// drain's Shutdown then still waits for the requests in flight.
func httpComponent(srv *http.Server, ln net.Listener) component {
	idle := &newConns{m: map[net.Conn]struct{}{}}
	srv.ConnState = idle.track
	return component{
		name: "http server",
		run: func(context.Context) error {
			if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
		stop: func(ctx context.Context) error {
			idle.closeAll()
			if err := srv.Shutdown(ctx); err != nil {
				return fmt.Errorf("shutdown: %w", err)
			}
			return nil
		},
	}
}

// newConns tracks connections that have not sent a request. http.Server's
// Shutdown waits up to 5 s for such a connection (a browser's preconnect),
// past httpGrace, so the drain closes them first, and closes on arrival one
// Serve accepts before Shutdown closes the listener: no request is lost on
// one (golang/go#22682).
type newConns struct {
	m        map[net.Conn]struct{}
	mu       sync.Mutex
	draining bool
}

func (n *newConns) track(c net.Conn, st http.ConnState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if st != http.StateNew {
		delete(n.m, c)
		return
	}
	if n.draining {
		_ = c.Close()
		return
	}
	n.m[c] = struct{}{}
}

func (n *newConns) closeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.draining = true
	for c := range n.m {
		_ = c.Close()
		delete(n.m, c)
	}
}
