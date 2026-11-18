package testrig

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/go/health/v2"
	"github.com/cplieger/subflux/internal/app"
	"github.com/cplieger/subflux/internal/settings"
)

// readyBudget bounds how long Start waits for the app to start serving.
const readyBudget = 10 * time.Second

// Options configures one rig.
type Options struct {
	// Mutate edits app.Config before Run; later landings set their fakes here.
	Mutate func(*app.Config)
	// Config is written to the temp config path before boot; nil leaves it
	// absent.
	Config []byte
}

// Rig is one running app.
type Rig struct {
	// Settings is the app's settings service, for saves before an HTTP
	// route exists.
	Settings *settings.Service
	Client   *http.Client
	done     chan error
	cancel   context.CancelFunc
	err      error
	// URL is the product listener's base URL; OpsURL is the ops listener's
	// (/healthz and /metrics), at OpsAddr.
	URL, OpsURL, OpsAddr string
	Paths                app.Paths
	once                 sync.Once
}

// Start runs app.Run until the test ends, once the app has started and its
// ops listener answers /healthz (a 503 over a settings file that did not load
// included), and fails the test if Run returned an error other than the
// shutdown it caused.
func Start(tb testing.TB, o Options) *Rig {
	tb.Helper()
	dir := tb.TempDir()
	sock, mkErr := os.MkdirTemp("", "sfx") //nolint:usetesting // a Unix socket path is bounded at 108 bytes, which a test's TempDir can pass
	if mkErr != nil {
		tb.Fatal(mkErr)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(sock) })
	r := &Rig{
		Paths: app.Paths{
			Config: filepath.Join(dir, "config.yaml"), Store: filepath.Join(dir, "subflux.bolt"),
			SocketDir: sock, Socket: filepath.Join(sock, "admin.sock"),
		},
		Client: &http.Client{Timeout: readyBudget},
		done:   make(chan error, 1),
	}
	if o.Config != nil {
		if err := os.WriteFile(r.Paths.Config, o.Config, 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	ln, err := (&net.ListenConfig{}).Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	r.URL = "http://" + ln.Addr().String()
	got, gotHealth, gotOps := make(chan *settings.Service, 1), make(chan *health.Status, 1), make(chan net.Addr, 1)
	cfg := app.Config{
		Paths:     r.Paths,
		Listener:  ln,
		OpsAddr:   "127.0.0.1:0",
		Ops:       func(a net.Addr) { gotOps <- a },
		LogOutput: io.Discard,
		Settings:  func(s *settings.Service) { got <- s },
		Health:    func(h *health.Status) { gotHealth <- h },
	}
	if o.Mutate != nil {
		o.Mutate(&cfg)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(tb.Context()))
	r.cancel = cancel
	go func() { r.done <- app.Run(ctx, cfg) }()
	tb.Cleanup(func() {
		if err := r.Stop(); err != nil {
			tb.Errorf("app.Run: %v", err)
		}
	})
	if err := r.awaitReady(got, gotHealth, gotOps); err != nil {
		tb.Fatal(err)
	}
	return r
}

func (r *Rig) awaitReady(got <-chan *settings.Service, gotHealth <-chan *health.Status, gotOps <-chan net.Addr) error {
	var verdict *health.Status
	deadline := time.Now().Add(readyBudget)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			r.done <- err
			return errors.Join(errors.New("testrig: app.Run returned during boot"), err)
		case s := <-got:
			r.Settings = s
		case h := <-gotHealth:
			verdict = h
		case a := <-gotOps:
			r.OpsAddr = a.String()
			r.OpsURL = "http://" + r.OpsAddr
		default:
		}
		if r.Settings != nil && verdict != nil && r.OpsURL != "" && verdict.Healthy() && r.serving() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("testrig: the app did not start and answer /healthz in time")
}

// serving reports that the ops listener answers /healthz, whatever the
// verdict.
func (r *Rig) serving() bool {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, r.OpsURL+"/healthz", http.NoBody)
	if err != nil {
		return false
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// Stop cancels Run and returns its error; later calls return the same.
func (r *Rig) Stop() error {
	r.once.Do(func() {
		r.cancel()
		r.err = <-r.done
	})
	return r.err
}

// StartDraining is Start with a product request held open and Stop begun, so
// the app is draining: /healthz answers 503 while the product listener waits
// on the held request. release ends the request and waits for Stop; call it
// within the app's 5 s HTTP grace, or Run returns the grace overrun.
func StartDraining(tb testing.TB) (r *Rig, release func()) {
	tb.Helper()
	held, unblock := make(chan struct{}), make(chan struct{})
	r = Start(tb, Options{Mutate: func(c *app.Config) {
		c.Routes = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /testrig/hold", func(http.ResponseWriter, *http.Request) {
				close(held)
				<-unblock
			})
		}
	}})
	hold := mustRequest(tb, r.URL+"/testrig/hold")
	go func() {
		if resp, err := r.Client.Do(hold); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-held
	stopped := make(chan struct{})
	go func() {
		_ = r.Stop()
		close(stopped)
	}()
	release = sync.OnceFunc(func() {
		close(unblock)
		<-stopped
	})
	tb.Cleanup(release)
	deadline := time.Now().Add(readyBudget)
	for !r.draining(tb) {
		if time.Now().After(deadline) {
			release()
			tb.Fatal("testrig: /healthz never reported the drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r, release
}

// draining reports that /healthz answers 503 naming the drain.
func (r *Rig) draining(tb testing.TB) bool {
	resp, err := r.Client.Do(mustRequest(tb, r.OpsURL+"/healthz"))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return err == nil && resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(b), "subflux is starting or shutting down")
}

func mustRequest(tb testing.TB, url string) *http.Request {
	tb.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		tb.Fatal(err)
	}
	return req
}
