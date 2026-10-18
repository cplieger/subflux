package app

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"go.yaml.in/yaml/v3"
)

// syncBuffer is a log destination safe for the app's goroutines.
type syncBuffer struct {
	b  bytes.Buffer
	mu sync.Mutex
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// shorten sets a test seam for the test's duration.
func shorten[T any](t *testing.T, v *T, to T) {
	t.Helper()
	prev := *v
	*v = to
	t.Cleanup(func() { *v = prev })
}

type booted struct {
	svc    *settings.Service
	done   chan error
	cancel context.CancelFunc
	paths  Paths
	addr   string
}

// boot runs Run on a temp directory with config text (absent when empty)
// and waits for the health marker; mutate edits the Config first.
func boot(t *testing.T, text string, mutate func(*Config)) *booted {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	dir := t.TempDir()
	b := &booted{paths: Paths{Config: filepath.Join(dir, "config.yaml"), Health: filepath.Join(dir, "healthy")}, done: make(chan error, 1)}
	if text != "" {
		if err := os.WriteFile(b.paths.Config, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := make(chan *settings.Service, 1)
	cfg := Config{Paths: b.paths, LogOutput: io.Discard, Settings: func(s *settings.Service) { got <- s }}
	if cfg.Listener == nil {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Listener = ln
	}
	if mutate != nil {
		mutate(&cfg)
	}
	b.addr = cfg.Listener.Addr().String()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	b.cancel = cancel
	go func() { b.done <- Run(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-b.done:
		case <-time.After(waitBudget):
			t.Error("Run did not return at cleanup")
		}
	})
	b.svc = <-got
	if !eventually(func() bool { _, err := os.Stat(b.paths.Health); return err == nil }) {
		t.Fatal("the health marker never appeared")
	}
	return b
}

func eventually(cond func() bool) bool {
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func (b *booted) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-b.done:
		b.done <- err
		return err
	case <-time.After(waitBudget):
		t.Fatal("Run did not return")
		return nil
	}
}

func (b *booted) markerPresent() bool {
	_, err := os.Stat(b.paths.Health)
	return err == nil
}

// validLanguages is the smallest languages section that parses, for a file
// whose test is about another section: an absent one fails the file.
const validLanguages = "languages:\n  default: [{tag: en}]\n"

func saveText(t *testing.T, svc *settings.Service, text string) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if _, errs, err := svc.Save(t.Context(), doc.Content[0], svc.Snapshot().Revision()); err != nil || len(errs) != 0 {
		t.Fatalf("Save(%q) = %v, %v", text, errs, err)
	}
}

func TestRunReturnsBindError(t *testing.T) {
	held, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	shorten(t, &listenAddr, held.Addr().String())
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	dir := t.TempDir()
	err = Run(t.Context(), Config{Paths: Paths{Config: filepath.Join(dir, "config.yaml"), Health: filepath.Join(dir, "healthy")}, LogOutput: io.Discard})
	if err == nil || !strings.Contains(err.Error(), held.Addr().String()) {
		t.Errorf("Run = %v, want an error naming %s", err, held.Addr())
	}
}

// failingListener accepts one connection, then fails once fail closes.
type failingListener struct {
	net.Listener
	fail     chan struct{}
	accepted atomic.Bool
}

func (l *failingListener) Accept() (net.Conn, error) {
	if l.accepted.CompareAndSwap(false, true) {
		return l.Listener.Accept()
	}
	<-l.fail
	return nil, errors.New("accept: listener torn down")
}

// holdRequest boots with a route that blocks until release closes, sends
// one request to it and returns once the handler is running.
func holdRequest(t *testing.T, mutate func(*Config)) (b *booted, release chan struct{}) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	b = boot(t, "", func(c *Config) {
		c.Routes = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /slow", func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			})
		}
		if mutate != nil {
			mutate(c)
		}
	})
	go func() {
		req, _ := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodGet, "http://"+b.addr+"/slow", http.NoBody)
		if resp, err := http.DefaultTransport.RoundTrip(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	return b, release
}

func TestListenerFailureAfterReadiness(t *testing.T) {
	failing := func(t *testing.T) (*failingListener, func(*Config)) {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		fl := &failingListener{Listener: ln, fail: make(chan struct{})}
		return fl, func(c *Config) { c.Listener = fl }
	}
	t.Run("active_request_finishes", func(t *testing.T) {
		shorten(t, &drainBudget, 2*time.Second)
		shorten(t, &httpGrace, time.Second)
		fl, use := failing(t)
		b, release := holdRequest(t, use)
		close(fl.fail)
		if !eventually(func() bool { return !b.markerPresent() }) {
			t.Fatal("the health marker stayed after the listener failed")
		}
		select {
		case err := <-b.done:
			t.Fatalf("Run returned %v with a request in flight", err)
		case <-time.After(100 * time.Millisecond):
		}
		released := time.Now()
		close(release)
		err := b.wait(t)
		if err == nil || !strings.Contains(err.Error(), "listener torn down") || strings.Contains(err.Error(), "shutdown") {
			t.Errorf("Run = %v, want the accept error alone", err)
		}
		if took := time.Since(released); took > time.Second {
			t.Errorf("Run took %v after the request finished", took)
		}
	})
	t.Run("active_request_overruns_grace", func(t *testing.T) {
		shorten(t, &drainBudget, time.Second)
		shorten(t, &httpGrace, 200*time.Millisecond)
		fl, use := failing(t)
		b, release := holdRequest(t, use)
		t.Cleanup(func() { close(release) })
		started := time.Now()
		close(fl.fail)
		err := b.wait(t)
		if err == nil || !strings.HasPrefix(err.Error(), "http server: accept: listener torn down") || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run = %v, want the accept error first, joined with the shutdown's overrun", err)
		}
		if took := time.Since(started); took > time.Second {
			t.Errorf("Run took %v after the failure, over the drain budget", took)
		}
	})
}

func TestTerminationDrains(t *testing.T) {
	t.Run("marker_false_before_http_shutdown", func(t *testing.T) {
		b, release := holdRequest(t, nil)
		b.cancel()
		if !eventually(func() bool { return !b.markerPresent() }) {
			t.Fatal("the marker stayed while the drain waited on a request")
		}
		close(release)
		if err := b.wait(t); err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	})
	t.Run("handler_ignoring_cancellation", func(t *testing.T) {
		shorten(t, &httpGrace, 200*time.Millisecond)
		shorten(t, &drainBudget, time.Second)
		b, release := holdRequest(t, nil)
		t.Cleanup(func() { close(release) })
		started := time.Now()
		b.cancel()
		err := b.wait(t)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run = %v with a handler past the grace, want the shutdown's overrun", err)
		}
		if took := time.Since(started); took > time.Second {
			t.Errorf("Run took %v, over the drain budget", took)
		}
	})
	t.Run("preconnected_client", func(t *testing.T) {
		shorten(t, &httpGrace, time.Second)
		shorten(t, &drainBudget, 2*time.Second)
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		al := &acceptLog{Listener: ln, second: make(chan struct{})}
		b := boot(t, "", func(c *Config) { c.Listener = al })
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", b.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		select {
		case <-al.second:
		case <-time.After(waitBudget):
			t.Fatal("the server never accepted the connection")
		}
		started := time.Now()
		b.cancel()
		if err := b.wait(t); err != nil {
			t.Errorf("Run = %v with a connection that sent no request", err)
		}
		if took := time.Since(started); took > 500*time.Millisecond {
			t.Errorf("the drain waited %v on a connection that sent no request", took)
		}
	})
	t.Run("slow_body_cut_at_read_timeout", func(t *testing.T) {
		shorten(t, &readTimeout, 200*time.Millisecond)
		readErr := make(chan error, 1)
		b := boot(t, "", func(c *Config) {
			c.Routes = func(mux *http.ServeMux) {
				mux.HandleFunc("POST /upload", func(_ http.ResponseWriter, r *http.Request) {
					_, err := io.Copy(io.Discard, r.Body)
					readErr <- err
				})
			}
		})
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", b.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := io.WriteString(conn, "POST /upload HTTP/1.1\r\nHost: x\r\nContent-Length: 1048576\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			chunk := bytes.Repeat([]byte("a"), 1024)
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				if _, err := conn.Write(chunk); err != nil {
					return
				}
				select {
				case <-stop:
					return
				case <-tick.C:
				}
			}
		}()
		select {
		case err := <-readErr:
			if err == nil {
				t.Error("a 1 KiB/s body was read to the end")
			}
		case <-time.After(3 * time.Second):
			t.Error("a 1 KiB/s body was still being read after the read timeout")
		}
	})
}

// acceptLog closes second when Accept is entered for the second time:
// http.Server marks the first connection StateNew before that call.
type acceptLog struct {
	net.Listener
	second chan struct{}
	n      atomic.Int32
}

func (a *acceptLog) Accept() (net.Conn, error) {
	if a.n.Add(1) == 2 {
		close(a.second)
	}
	return a.Listener.Accept()
}

// fakeSource is a change source the test signals by hand.
type fakeSource struct {
	ch   chan struct{}
	snap settings.Snapshot
}

func (f *fakeSource) Changed() <-chan struct{}    { return f.ch }
func (f *fakeSource) Snapshot() settings.Snapshot { return f.snap }

func TestChangeLoopCallsAppliedPerCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	svc := settings.NewService(path, settings.Registries{}, settings.Load(path, settings.Registries{}), nil, nil)
	level := new(slog.LevelVar)
	calls := make(chan slog.Level, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- changeLoop(ctx, svc, level, obs.New(), func() { calls <- level.Level() })
	}()
	levels := []string{"debug", "info", "warn", "error"}
	want := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	for i := range 100 {
		name := levels[i%len(levels)]
		saveText(t, svc, "server:\n  log_level: "+name+"\n"+validLanguages)
		select {
		case got := <-calls:
			if got != want[name] {
				t.Fatalf("commit %d: applied saw level %v, want %v", i, got, want[name])
			}
		case <-time.After(waitBudget):
			t.Fatalf("commit %d: applied was not called", i)
		}
		select {
		case <-calls:
			t.Fatalf("commit %d: applied was called twice", i)
		default:
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("changeLoop = %v", err)
	}

	t.Run("signal_without_commit", func(t *testing.T) {
		f := &fakeSource{ch: make(chan struct{}, 1), snap: svc.Snapshot()}
		n := make(chan struct{}, 2)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = changeLoop(ctx, f, new(slog.LevelVar), obs.New(), func() { n <- struct{}{} }) }()
		f.ch <- struct{}{}
		select {
		case <-n:
		case <-time.After(waitBudget):
			t.Fatal("a Changed signal without a commit did not call applied")
		}
		select {
		case <-n:
			t.Error("one signal called applied twice")
		case <-time.After(50 * time.Millisecond):
		}
	})
	t.Run("nil_hook", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		svc := settings.NewService(path, settings.Registries{}, settings.Load(path, settings.Registries{}), nil, nil)
		level := new(slog.LevelVar)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = changeLoop(ctx, svc, level, obs.New(), nil) }()
		saveText(t, svc, "server:\n  log_level: debug\n"+validLanguages)
		if !eventually(func() bool { return level.Level() == slog.LevelDebug }) {
			t.Error("the loop with no hook did not apply the commit")
		}
	})
	t.Run("only_reader", func(t *testing.T) {
		fset := token.NewFileSet()
		pkgs, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		var readers []string
		for _, file := range pkgs {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Changed" {
							readers = append(readers, fn.Name.Name)
						}
					}
					return true
				})
			}
		}
		if len(readers) != 1 || readers[0] != "changeLoop" {
			t.Errorf("Changed() is read by %v, want changeLoop alone", readers)
		}
	})
}
