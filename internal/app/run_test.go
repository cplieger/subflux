package app

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"go.yaml.in/yaml/v4"
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
	status *status.Store
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
	b := &booted{paths: testPaths(dir), done: make(chan error, 1)}
	if text != "" {
		if err := os.WriteFile(b.paths.Config, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, gotStatus := make(chan *settings.Service, 1), make(chan *status.Store, 1)
	cfg := Config{Paths: b.paths, LogOutput: io.Discard, Settings: func(s *settings.Service) { got <- s }, Status: func(s *status.Store) { gotStatus <- s }}
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
	b.svc, b.status = <-got, <-gotStatus
	if !eventually(func() bool { _, err := os.Stat(b.paths.Health); return err == nil }) {
		t.Fatal("the health marker never appeared")
	}
	return b
}

func testPaths(dir string) Paths {
	return Paths{Config: filepath.Join(dir, "config.yaml"), Store: filepath.Join(dir, "subflux.bolt"), Health: filepath.Join(dir, "healthy")}
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
	paths := testPaths(t.TempDir())
	err = Run(t.Context(), Config{Paths: paths, LogOutput: io.Discard})
	if err == nil || !strings.Contains(err.Error(), held.Addr().String()) {
		t.Errorf("Run = %v, want an error naming %s", err, held.Addr())
	}
	storeReleased(t, paths)
}

// storeReleased fails t unless the state file opens at once, so Run closed
// it (statedb.Open waits 5 s on a held lock).
func storeReleased(t *testing.T, paths Paths) {
	t.Helper()
	started := time.Now()
	s, err := statedb.Open(paths.Store)
	if err != nil {
		t.Fatalf("statedb.Open after Run = %v, want the store closed", err)
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("statedb.Open took %v, waiting on a lock Run kept", took)
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
}

func TestBootFailureClosesStore(t *testing.T) {
	cases := []struct {
		arm  func(t *testing.T)
		name string
		want string
	}{
		{name: "activation", want: "activate the loaded settings", arm: func(t *testing.T) {
			shorten(t, &wrapActivate, func(activateFunc) activateFunc {
				return func(context.Context, settings.Active) error { return errors.New("store refused") }
			})
		}},
		{name: "job_records", want: "unfinished jobs", arm: func(t *testing.T) {
			shorten(t, &statusPersister, func(s *statedb.Store) status.Persister {
				return &faultyPersister{Persister: s, unfinished: errors.New("read failed")}
			})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.arm(t)
			prev := slog.Default()
			t.Cleanup(func() { slog.SetDefault(prev) })
			paths := testPaths(t.TempDir())
			if err := os.WriteFile(paths.Config, []byte(validLanguages), 0o600); err != nil {
				t.Fatal(err)
			}
			ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			err = Run(t.Context(), Config{Paths: paths, Listener: ln, LogOutput: io.Discard})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Run = %v, want an error naming %q", err, c.want)
			}
			if _, err := os.Stat(paths.Health); !os.IsNotExist(err) {
				t.Errorf("the health marker exists after a failed boot (%v)", err)
			}
			storeReleased(t, paths)
		})
	}
}

// faultyPersister fails UnfinishedJobs with unfinished, and the next
// putFails PutJob calls.
type faultyPersister struct {
	status.Persister
	unfinished error
	putFails   atomic.Int32
}

func (f *faultyPersister) UnfinishedJobs(ctx context.Context) ([]status.Job, error) {
	if f.unfinished != nil {
		return nil, f.unfinished
	}
	return f.Persister.UnfinishedJobs(ctx)
}

func (f *faultyPersister) PutJob(ctx context.Context, j status.Job) error {
	if f.putFails.Add(-1) >= 0 {
		return errors.New("put failed")
	}
	return f.Persister.PutJob(ctx, j)
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

// fakeSource is a change source the test signals by hand.
type fakeSource struct {
	ch       chan struct{}
	snap     settings.Snapshot
	diverged atomic.Bool
}

func (f *fakeSource) Changed() <-chan struct{}    { return f.ch }
func (f *fakeSource) Snapshot() settings.Snapshot { return f.snap }
func (f *fakeSource) Diverged() bool              { return f.diverged.Load() }

// newLoop is a change loop over src with a fresh level, registry and status
// store, running a JSON handler and calling no hook.
func newLoop(t *testing.T, src changeSource) *changeLoop {
	t.Helper()
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	return &changeLoop{svc: src, level: new(slog.LevelVar), metrics: obs.New(), alerts: st, running: slogx.JSON}
}

func hasAlert(st *status.Store, src status.AlertSource) bool {
	snap, _ := st.Snapshot()
	return slices.ContainsFunc(snap.Alerts, func(a status.Alert) bool { return a.Source == src })
}

func TestChangeLoopRaisesConfigWhileDiverged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f := &fakeSource{ch: make(chan struct{}, 1), snap: settings.Load(path, settings.Registries{})}
	applied := make(chan struct{})
	l := newLoop(t, f)
	l.applied = func() { applied <- struct{}{} }
	go func() { _ = l.run(t.Context()) }()
	step := func(diverged bool) {
		t.Helper()
		f.diverged.Store(diverged)
		f.ch <- struct{}{}
		select {
		case <-applied:
		case <-time.After(waitBudget):
			t.Fatal("the loop did not apply the change")
		}
		if got := hasAlert(l.alerts, status.AlertConfig); got != diverged {
			t.Errorf("Diverged %v: config alert active = %v", diverged, got)
		}
	}
	step(true)
	step(true)
	step(false)
}

func TestRestartPendingComparesRunningFormat(t *testing.T) {
	dir := t.TempDir()
	load := func(server string) settings.Snapshot {
		t.Helper()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte("server:\n"+server+validLanguages), 0o600); err != nil {
			t.Fatal(err)
		}
		return settings.Load(path, settings.Registries{})
	}
	t.Run("loop", func(t *testing.T) {
		f := &fakeSource{ch: make(chan struct{}, 1)}
		applied := make(chan struct{})
		l := newLoop(t, f)
		l.running, l.applied = slogx.Text, func() { applied <- struct{}{} }
		go func() { _ = l.run(t.Context()) }()
		for _, c := range []struct {
			server  string
			pending bool
		}{
			{"  log_format: text\n  log_level: debug\n", false},
			{"  log_format: json\n", true},
			{"  log_format: text\n", false},
		} {
			f.snap = load(c.server)
			f.ch <- struct{}{}
			select {
			case <-applied:
			case <-time.After(waitBudget):
				t.Fatal("the loop did not apply the commit")
			}
			if got := hasAlert(l.alerts, status.AlertRestartPending); got != c.pending {
				t.Errorf("running text, commit %q: restart_pending = %v, want %v", c.server, got, c.pending)
			}
		}
	})
	t.Run("boot_format", func(t *testing.T) {
		b := boot(t, "server:\n  log_format: text\n"+validLanguages, nil)
		saveText(t, b.svc, "server:\n  log_format: json\n"+validLanguages)
		if !eventually(func() bool { return hasAlert(b.status, status.AlertRestartPending) }) {
			t.Error("booted with text, a commit switching to JSON raised no restart_pending")
		}
	})
}

func TestChangeLoopCallsAppliedPerCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	svc := settings.NewService(path, settings.Registries{}, settings.Load(path, settings.Registries{}), nil, nil)
	level := new(slog.LevelVar)
	calls := make(chan slog.Level, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		l := newLoop(t, svc)
		l.level, l.applied = level, func() { calls <- level.Level() }
		done <- l.run(ctx)
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
		l := newLoop(t, f)
		l.applied = func() { n <- struct{}{} }
		go func() { _ = l.run(ctx) }()
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
		l := newLoop(t, svc)
		l.level = level
		go func() { _ = l.run(ctx) }()
		saveText(t, svc, "server:\n  log_level: debug\n"+validLanguages)
		if !eventually(func() bool { return level.Level() == slog.LevelDebug }) {
			t.Error("the loop with no hook did not apply the commit")
		}
	})
	t.Run("only_reader", func(t *testing.T) {
		if got := settingsChangedReaders(t); len(got) != 1 || got[0] != "changeLoop.run" {
			t.Errorf("settings' Changed() is read by %v, want changeLoop.run alone", got)
		}
	})
}

func TestJobEndedDuringHTTPDrainIsPersisted(t *testing.T) {
	faulty := &faultyPersister{}
	shorten(t, &statusPersister, func(s *statedb.Store) status.Persister {
		faulty.Persister = s
		return faulty
	})
	entered, release := make(chan status.JobID, 1), make(chan struct{})
	var st atomic.Pointer[status.Store]
	b := boot(t, "", func(c *Config) {
		c.Routes = func(mux *http.ServeMux) {
			mux.HandleFunc("POST /job", func(w http.ResponseWriter, r *http.Request) {
				j, _, err := st.Load().Begin(r.Context(), status.Job{Kind: status.Scan, Owner: status.SystemOwner}, nil)
				if err != nil {
					http.Error(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				entered <- j.ID
				<-release
				st.Load().Update(j.ID, func(j *status.Job) { j.State, j.Outcome = status.Done, status.Succeeded })
			})
		}
	})
	st.Store(b.status)
	faulty.putFails.Store(1)
	go func() {
		req, _ := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodPost, "http://"+b.addr+"/job", http.NoBody)
		if resp, err := http.DefaultTransport.RoundTrip(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	var id status.JobID
	select {
	case id = <-entered:
	case <-time.After(waitBudget):
		t.Fatal("the job never started")
	}
	b.cancel()
	if !eventually(func() bool { return !b.markerPresent() }) {
		t.Fatal("the drain never began")
	}
	close(release)
	if err := b.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	s, err := statedb.Open(b.paths.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, ok, err := s.GetJob(t.Context(), id)
	if err != nil || !ok || j.State != status.Done || j.Outcome != status.Succeeded {
		t.Errorf("the job's record after the drain = %+v, %v, %v; want Done, Succeeded", j, ok, err)
	}
}

func TestDrainCancelsBlockedSave(t *testing.T) {
	shorten(t, &httpGrace, 200*time.Millisecond)
	shorten(t, &drainBudget, time.Second)
	var hold atomic.Bool
	blocked := make(chan struct{})
	shorten(t, &wrapActivate, func(f activateFunc) activateFunc {
		return func(ctx context.Context, a settings.Active) error {
			if hold.Load() {
				close(blocked)
				<-ctx.Done()
				return ctx.Err()
			}
			return f(ctx, a)
		}
	})
	b := boot(t, validLanguages, nil)
	hold.Store(true)
	saved := make(chan error, 1)
	go func() {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte("server:\n  log_level: debug\n"+validLanguages), &doc); err != nil {
			saved <- err
			return
		}
		_, _, err := b.svc.Save(context.WithoutCancel(t.Context()), doc.Content[0], b.svc.Snapshot().Revision())
		saved <- err
	}()
	select {
	case <-blocked:
	case <-time.After(waitBudget):
		t.Fatal("the Save never reached activate")
	}
	started := time.Now()
	b.cancel()
	err := b.wait(t)
	if err == nil || !strings.Contains(err.Error(), "settings save overran") {
		t.Errorf("Run = %v, want the settings overrun", err)
	}
	if took := time.Since(started); took > time.Second {
		t.Errorf("Run took %v, over the drain budget", took)
	}
	select {
	case err := <-saved:
		if !errors.Is(err, settings.ErrActivation) {
			t.Errorf("Save = %v, want ErrActivation", err)
		}
	case <-time.After(waitBudget):
		t.Fatal("the blocked Save never returned")
	}
	if got, err := os.ReadFile(b.paths.Config); err != nil || string(got) != validLanguages {
		t.Errorf("config.yaml after the cancelled Save = %q, %v; want the boot file", got, err)
	}
	if s, err := statedb.Open(b.paths.Store); !errors.Is(err, statedb.ErrLocked) {
		if err == nil {
			_ = s.Close()
		}
		t.Errorf("statedb.Open after the overrun = %v, want the store still held", err)
	}
}

// settingsChangedReaders type-checks the package's non-test files and names
// each function calling settings.Service's Changed, directly or through
// changeSource; status.Store's Changed, a broadcast, does not count.
func settingsChangedReaders(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	if _, err := conf.Check("github.com/cplieger/subflux/internal/app", fset, files, info); err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{
		"(*github.com/cplieger/subflux/internal/settings.Service).Changed": true,
		"(github.com/cplieger/subflux/internal/app.changeSource).Changed":  true,
	}
	var readers []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil {
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				name = recv.(*ast.Ident).Name + "." + name
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if m, ok := info.Uses[sel.Sel].(*types.Func); ok && reads[m.FullName()] {
						readers = append(readers, name)
					}
				}
				return true
			})
		}
	}
	return readers
}
