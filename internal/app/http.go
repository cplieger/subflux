package app

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"

	"github.com/cplieger/go/health/v2"
	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/subflux/internal/obs"
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
	return httpserver.Chain(mux, httpserver.Logging(logOpt), httpserver.Recoverer(recoverOpt))
}

func httpMetrics(m *obs.Metrics) (httpserver.LogOption, httpserver.RecoverOption) {
	return httpserver.WithRecordRouteMetric(m.RecordHTTP),
		httpserver.WithPanicHook(func(any, []byte) { m.RecordPanic() })
}

// httpProducer serves srv on ln through httpserver.Run, whose shutdown on
// ctx's end waits for the requests in flight within httpGrace. Its stop
// waits for Run, which that grace bounds, so the overrun is Run's own
// ErrShutdownGraceExpired. Run returns a serve error (a failed Accept) at
// once, the drain's cause, and shuts nothing down on that path, so the stop
// then waits for the requests still running on the connections Serve
// accepted.
func httpProducer(srv *http.Server, ln net.Listener) component {
	returned := make(chan struct{})
	var serveExited bool
	return component{
		name: "http server",
		run: func(ctx context.Context) error {
			defer close(returned)
			return httpserver.Run(ctx, srv, ln, nil,
				httpserver.WithShutdownGrace(httpGrace),
				httpserver.WithServeExit(func(context.Context) { serveExited = true }))
		},
		stop: func(ctx context.Context) error {
			<-returned
			if !serveExited {
				return nil
			}
			if err := srv.Shutdown(ctx); err != nil {
				return fmt.Errorf("shutdown: %w", err)
			}
			return nil
		},
	}
}
