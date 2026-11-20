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
	"github.com/cplieger/go/httpserver/v2"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/wirecodes"
)

//go:embed placeholder.html
var placeholder []byte

// csp is every product answer's Content-Security-Policy.
const csp = "default-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

// handler is the product listener's chain over the page and routes. The
// address middleware is outermost, so the access log and every later reader
// see one client address; the headers are set before anything below answers,
// so a refused host and a recovered panic carry them; the host gate is inside
// the logger, so a refusal is logged and counted. /healthz and /metrics are
// the ops listener's (opsConfig).
func handler(m *obs.Metrics, snapshot func() settings.Snapshot, det *clientaddr.Detector, routes func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		_, _ = w.Write(placeholder)
	})
	if routes != nil {
		routes(mux)
	}
	resolver := func() clientaddr.Resolver {
		srv, _ := snapshot().Server()
		return clientaddr.New(srv.TrustedProxies())
	}
	logOpt, recoverOpt := httpMetrics(m)
	return httpserver.Chain(mux,
		clientaddr.Middleware(resolver, det),
		httpserver.Logging(logOpt, httpserver.WithClientIPFunc(clientIP)),
		httpserver.SecurityHeaders(httpserver.WithCSP(csp), httpserver.WithReferrerPolicy("no-referrer")),
		httpserver.NoStore(),
		httpserver.Recoverer(recoverOpt, httpserver.WithRecoverResponder(wirecodes.Catalog.Responder())),
		hostGate(snapshot),
		httpserver.Gzip(),
	)
}

// clientIP is the address clientaddr.Middleware resolved for r.
func clientIP(r *http.Request) string {
	a, ok := clientaddr.From(r.Context())
	if !ok {
		return ""
	}
	return a.IP().String()
}

// opsConfig is the ops listener on addr: GET /healthz over checks and GET
// /metrics. Its empty Hosts runs no host check, since neither route holds a
// secret, and its requests stay out of http_requests_total, which counts the
// product listener's alone.
func opsConfig(addr string, m *obs.Metrics, checks []health.Check) httpserver.OpsConfig {
	return httpserver.OpsConfig{
		Metrics: m.Handler(),
		Healthz: health.CheckHandler(checks...),
		Errors:  wirecodes.Catalog.Responder(),
		Setting: OpsAddrSetting,
		Addr:    addr,
	}
}

// hostGate serves next behind the host policy of the server section in
// force, read per request so a save changing allowed_hosts applies to the
// next request; the policy answers a refused Host 403 host_not_allowed.
func hostGate(snapshot func() settings.Snapshot) httpserver.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			srv, _ := snapshot().Server()
			srv.Hosts().Middleware()(next).ServeHTTP(w, r)
		})
	}
}

func httpMetrics(m *obs.Metrics) (httpserver.LogOption, httpserver.RecoverOption) {
	return httpserver.WithRecordRouteMetric(m.RecordHTTP),
		httpserver.WithPanicHook(func(any, []byte) { m.RecordPanic() })
}

// httpProducer serves srv on ln, and ops beside it, through
// httpserver.RunWithOps, whose shutdown on ctx's end waits for the product
// requests in flight within httpGrace while /healthz keeps answering, then
// stops the ops listener within that listener's own shutdown grace. Its stop
// waits for RunWithOps, so the product overrun is Run's own
// ErrShutdownGraceExpired. Run returns a serve error (a failed Accept) at once,
// the drain's cause, and shuts nothing down on that path, so the stop then
// waits for the requests still running on the connections Serve accepted.
func httpProducer(srv *http.Server, ln net.Listener, ops *httpserver.Ops) component {
	returned := make(chan struct{})
	var serveExited bool
	return component{
		name: "http server",
		run: func(ctx context.Context) error {
			defer close(returned)
			return httpserver.RunWithOps(ctx, ops, httpserver.Served{Server: srv, Listener: ln, Options: []httpserver.RunOption{
				httpserver.WithShutdownGrace(httpGrace),
				httpserver.WithServeExit(func(context.Context) { serveExited = true }),
			}})
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

// healthChecks are the verdict GET /healthz answers: the process past
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

// The ops listener's setting and its address when that setting is blank.
const (
	OpsAddrSetting = "OPS_LISTEN_ADDR"
	defaultOpsAddr = ":8375"
)

// OpsAddr is the ops listener's address from OPS_LISTEN_ADDR's raw value:
// httpserver.ListenAddr's edge trim, and defaultOpsAddr when blank.
func OpsAddr(value string) string { return httpserver.ListenAddr(value, defaultOpsAddr) }

// ProbeHealth is the health subcommand: it GETs /healthz on addr, the ops
// listener's address, over loopback and exits 0 on a 2xx answer, 1
// otherwise, writing the failure to stderr.
func ProbeHealth(addr string) {
	u, err := probe.LocalURL(addr, "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "health: %s %q: %v\n", OpsAddrSetting, addr, err)
		os.Exit(1)
	}
	probe.Run(healthProbeTimeout, u)
}
