package app

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/cplieger/go/health/v2"
	"github.com/cplieger/go/health/v2/probe"
	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
)

//go:embed placeholder.html
var placeholder []byte

func handler(m *obs.Metrics, checks []health.Check, routes func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/health", health.CheckHandler(checks...))
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

// healthChecks are the verdict GET /api/health answers: the process past
// its start and not draining, and a settings file that loaded. Each reason
// is a fixed sentence, since the endpoint answers with no sign-in.
func healthChecks(verdict *health.Status, svc *settings.Service) []health.Check {
	return []health.Check{
		{Signal: verdict, Reason: "subflux is starting or shutting down"},
		{Signal: health.SignalFunc(func() bool { return svc.Snapshot().Mode() != settings.Failed }), Reason: "The settings file did not load."},
	}
}

// healthProbeTimeout is the health subcommand's budget, below the image's
// HEALTHCHECK --timeout of 5 s, so the probe writes its reason before Docker
// stops it.
const healthProbeTimeout = 4 * time.Second

// ProbeHealth is the health subcommand: it GETs /api/health on addr, the
// HTTP listener's address, over loopback and exits 0 on a 2xx answer, 1
// otherwise, writing the failure to stderr.
func ProbeHealth(addr string) {
	u, err := probe.LocalURL(addr, "/api/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "health:", err)
		os.Exit(1)
	}
	probe.Run(healthProbeTimeout, u)
}
