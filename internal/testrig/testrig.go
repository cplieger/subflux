package testrig

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/app"
	"github.com/cplieger/subflux/internal/settings"
)

// readyBudget bounds how long Start waits for /api/health to answer 200.
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
	URL      string
	Paths    app.Paths
	once     sync.Once
}

// Start runs app.Run until the test ends, once /api/health answers 200, and
// fails the test if Run returned an error other than the shutdown it caused.
func Start(tb testing.TB, o Options) *Rig {
	tb.Helper()
	dir := tb.TempDir()
	r := &Rig{
		Paths:  app.Paths{Config: filepath.Join(dir, "config.yaml"), Health: filepath.Join(dir, "healthy")},
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
	got := make(chan *settings.Service, 1)
	cfg := app.Config{
		Paths:     r.Paths,
		Listener:  ln,
		LogOutput: io.Discard,
		Settings:  func(s *settings.Service) { got <- s },
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
	if err := r.awaitReady(got); err != nil {
		tb.Fatal(err)
	}
	return r
}

func (r *Rig) awaitReady(got <-chan *settings.Service) error {
	deadline := time.Now().Add(readyBudget)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			r.done <- err
			return errors.Join(errors.New("testrig: app.Run returned during boot"), err)
		case s := <-got:
			r.Settings = s
		default:
		}
		if r.Settings != nil && r.healthy() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("testrig: /api/health did not answer 200 in time")
}

func (r *Rig) healthy() bool {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, r.URL+"/api/health", http.NoBody)
	if err != nil {
		return false
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Stop cancels Run and returns its error; later calls return the same.
func (r *Rig) Stop() error {
	r.once.Do(func() {
		r.cancel()
		r.err = <-r.done
	})
	return r.err
}
