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
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/backup"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
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
	b := &booted{paths: testPaths(t, dir), done: make(chan error, 1)}
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

// testPaths lays the files out in dir and the admin socket in a short
// directory of its own, since a Unix socket path is bounded at 108 bytes.
func testPaths(t *testing.T, dir string) Paths {
	t.Helper()
	sock, err := os.MkdirTemp("", "sfx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	return Paths{
		Config: filepath.Join(dir, "config.yaml"), Store: filepath.Join(dir, "subflux.bolt"), Health: filepath.Join(dir, "healthy"),
		SocketDir: sock, Socket: filepath.Join(sock, "admin.sock"),
	}
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
	paths := testPaths(t, t.TempDir())
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
			paths := testPaths(t, t.TempDir())
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
		close(release)
		err := b.wait(t)
		if err == nil || !strings.Contains(err.Error(), "listener torn down") || strings.Contains(err.Error(), "shutdown") {
			t.Errorf("Run = %v, want the accept error alone", err)
		}
	})
	t.Run("active_request_overruns_grace", func(t *testing.T) {
		shorten(t, &drainBudget, time.Second)
		shorten(t, &httpGrace, 200*time.Millisecond)
		fl, use := failing(t)
		b, release := holdRequest(t, use)
		t.Cleanup(func() { close(release) })
		close(fl.fail)
		err := b.wait(t)
		if err == nil || !strings.HasPrefix(err.Error(), "http server: accept: listener torn down") || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run = %v, want the accept error first, joined with the shutdown's overrun", err)
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
		b.cancel()
		err := b.wait(t)
		if !errors.Is(err, httpserver.ErrShutdownGraceExpired) || strings.Contains(err.Error(), "overran") {
			t.Errorf("Run = %v with a handler past the grace, want the shutdown's overrun alone", err)
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
	return &changeLoop{svc: src, level: new(slog.LevelVar), metrics: obs.New(), alerts: st, exposure: clientaddr.NewDetector(), running: slogx.JSON}
}

func hasAlert(st *status.Store, src status.AlertSource) bool {
	_, ok := alertText(st, src)
	return ok
}

// alertText is the text of src's active alert, false when none is.
func alertText(st *status.Store, src status.AlertSource) (string, bool) {
	snap, _ := st.Snapshot()
	i := slices.IndexFunc(snap.Alerts, func(a status.Alert) bool { return a.Source == src })
	if i < 0 {
		return "", false
	}
	return snap.Alerts[i].Text, true
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

// TestRestartPendingNamesWhatChangesAtRestart: restart_pending is raised
// while a commit holds a log format other than the running handler's, or
// config.yaml's auth section carries disable_auth: true with the booted
// bypass off, and its text names each.
func TestRestartPendingNamesWhatChangesAtRestart(t *testing.T) {
	const (
		format  = "the log format changes at the next restart"
		authOff = "config.yaml's auth.disable_auth: true turns authentication off at a restart that accepts the auth section"
	)
	dir := t.TempDir()
	load := func(t *testing.T, text string) settings.Snapshot {
		t.Helper()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(text+validLanguages), 0o600); err != nil {
			t.Fatal(err)
		}
		return settings.Load(path, settings.Registries{})
	}
	type commit struct{ text, want string }
	run := func(t *testing.T, commits []commit) {
		t.Helper()
		f := &fakeSource{ch: make(chan struct{}, 1)}
		applied := make(chan struct{})
		l := newLoop(t, f)
		l.running, l.applied = slogx.Text, func() { applied <- struct{}{} }
		go func() { _ = l.run(t.Context()) }()
		for _, c := range commits {
			f.snap = load(t, c.text)
			f.ch <- struct{}{}
			select {
			case <-applied:
			case <-time.After(waitBudget):
				t.Fatal("the loop did not apply the commit")
			}
			if got, _ := alertText(l.alerts, status.AlertRestartPending); got != c.want {
				t.Errorf("running text, booted bypass off, commit %q: restart_pending %q, want %q", c.text, got, c.want)
			}
		}
	}
	t.Run("log format", func(t *testing.T) {
		run(t, []commit{
			{"server:\n  log_format: text\n  log_level: debug\n", ""},
			{"server:\n  log_format: json\n", format},
			{"server:\n  log_format: text\n", ""},
		})
	})
	t.Run("disable_auth", func(t *testing.T) {
		run(t, []commit{
			{"server:\n  log_format: text\nauth:\n  disable_auth: false\n", ""},
			{"server:\n  log_format: text\nauth:\n  disable_auth: true\n", authOff},
			{"auth:\n  disable_auth: true\n", format + "; " + authOff},
			{"server:\n  log_format: text\nauth:\n  password_login: nope\n  disable_auth: true\n", ""},
			{"server:\n  log_format: text\n  allowed_hosts: [other.example]\nauth:\n  passkey_origins: ['https://nas.example']\n  disable_auth: true\n", authOff},
		})
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
	svc := settings.NewService(path, settings.Registries{}, settings.Load(path, settings.Registries{}), nil, nil, nil)
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
		svc := settings.NewService(path, settings.Registries{}, settings.Load(path, settings.Registries{}), nil, nil, nil)
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
	b.cancel()
	err := b.wait(t)
	if err == nil || !strings.Contains(err.Error(), "settings save overran") {
		t.Errorf("Run = %v, want the settings overrun", err)
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

// TestActivationOfSettings: the store keys coverage on the sources, the
// language policy and the embedded formats the settings ignore, and records
// the auth section's method record.
func TestActivationOfSettings(t *testing.T) {
	fake := sourcetest.NewFake(&sourcetest.Library{}, mediasource.Capabilities{Kinds: []media.Kind{media.Movie}})
	kinds := []mediasource.AdapterKind{sourcetest.FakeKind("fake", fake)}
	policy, errs := language.ParsePolicy(parseNode(t, "default: [{tag: pt-BR}]"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	emb, errs := embedded.ParseConfig(parseNode(t, "ignore_codecs: [srt, dvbsub, srt]"))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	a := settings.Active{
		Languages: policy,
		Sources:   sourcesOf(t, kinds, "[{id: tv, type: fake, url: 'http://a', token: t1}, {id: films, type: fake, url: 'http://b', token: t2}]"),
		Embedded:  emb,
	}
	got := activationOf(&a)
	if got.Languages.Digest() != policy.Digest() {
		t.Error("Languages is not the languages section's policy")
	}
	if want := []media.SourceID{"tv", "films"}; !slices.Equal(got.Sources, want) {
		t.Errorf("Sources = %v, want %v", got.Sources, want)
	}
	if want := []string{"dvbsub", "srt"}; !slices.Equal(got.IgnoredCodecs, want) {
		t.Errorf("IgnoredCodecs = %v, want %v", got.IgnoredCodecs, want)
	}
	if got.Auth == nil || *got.Auth != accounts.MethodRecord(a.Auth) {
		t.Errorf("Auth = %+v, want the auth section's method record", got.Auth)
	}
}

func TestAuthEventDuringHTTPDrainIsLogged(t *testing.T) {
	logs := &syncBuffer{}
	var accts *accounts.Service
	ready, entered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	signedIn := make(chan int, 1)
	b := boot(t, "", func(c *Config) {
		c.LogOutput = logs
		c.Accounts = func(a *accounts.Service) { accts = a }
		c.Routes = func(mux *http.ServeMux) {
			defer close(ready)
			routes, err := accts.Routes(writeAuthError)
			if err != nil {
				t.Errorf("Routes: %v", err)
				return
			}
			var login string
			for _, r := range routes {
				mux.Handle(r.Method+" "+r.Path, r.Handler)
				if r.Name == "authLoginPassword" {
					login = r.Path
				}
			}
			// Held until the drain begins, then a sign-in that fails, served
			// in-process while HTTP drains.
			mux.HandleFunc("POST /hold", func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
				r := httptest.NewRequest(http.MethodPost, "https://nas.example"+login, strings.NewReader(`{"username":"eve","password":"wrong horse battery staple"}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://nas.example")
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				r.RemoteAddr = "127.0.0.1:40000"
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				signedIn <- w.Code
			})
		}
	})
	<-ready
	go func() {
		req, _ := http.NewRequestWithContext(context.WithoutCancel(t.Context()), http.MethodPost, "http://"+b.addr+"/hold", http.NoBody)
		if resp, err := http.DefaultTransport.RoundTrip(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(waitBudget):
		t.Fatal("the held request never arrived")
	}
	b.cancel()
	if !eventually(func() bool { return !b.markerPresent() }) {
		t.Fatal("the drain never began")
	}
	close(release)
	if code := <-signedIn; code != http.StatusUnauthorized {
		t.Fatalf("the sign-in during the drain answered %d, want 401", code)
	}
	if err := b.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil: both sinks done inside the budget", err)
	}
	if !strings.Contains(logs.String(), `"msg":"accounts: audit","kind":"login.failure"`) {
		t.Errorf("no audit line for the sign-in served during the HTTP drain:\n%s", logs.String())
	}
}

func TestSocketBindFailureDegrades(t *testing.T) {
	t.Run("a refused socket directory", func(t *testing.T) {
		logs := &syncBuffer{}
		b := boot(t, "", func(c *Config) {
			c.LogOutput = logs
			// Group and other access: Listen refuses the directory.
			if err := os.Chmod(c.Paths.SocketDir, 0o755); err != nil {
				t.Fatal(err)
			}
		})
		if !hasAlert(b.status, status.AlertAdminSocket) {
			t.Error("boot ran without the admin socket and raised no admin_socket")
		}
		if !strings.Contains(logs.String(), `"level":"ERROR","msg":"app: could not bind the admin socket`) {
			t.Errorf("no ERROR for the refused socket:\n%s", logs.String())
		}
	})
	t.Run("a bound socket answers", func(t *testing.T) {
		b := boot(t, "", nil)
		if hasAlert(b.status, status.AlertAdminSocket) {
			t.Error("admin_socket raised with the socket bound")
		}
		reply, err := adminsock.Call(t.Context(), b.paths.Socket, &adminsock.Request{Command: adminsock.SetupToken})
		if err != nil || reply.Secret == "" {
			t.Errorf("setup-token over the socket = %+v, %v; want a token", reply, err)
		}
	})
}

func TestBootRaisesAuthDisabled(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{validLanguages + "auth:\n  disable_auth: true\n", true},
		{validLanguages, false},
	} {
		b := boot(t, c.text, nil)
		if got := hasAlert(b.status, status.AlertAuthDisabled); got != c.want {
			t.Errorf("boot of %q: auth_disabled %v, want %v", c.text, got, c.want)
		}
	}
}

func TestExposureResetOnServerSave(t *testing.T) {
	dir := t.TempDir()
	load := func(text string) settings.Snapshot {
		t.Helper()
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return settings.Load(path, settings.Registries{})
	}
	det := clientaddr.NewDetector()
	seen := func() bool {
		_, ok := det.State(time.Now())
		return ok
	}
	expose := func() {
		h := clientaddr.Middleware(func() clientaddr.Resolver { return clientaddr.New(nil) }, det)(http.NotFoundHandler())
		r := httptest.NewRequest(http.MethodGet, "http://nas.example/", nil)
		r.RemoteAddr = "203.0.113.7:40000"
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !seen() {
			t.Fatal("a public client over plain HTTP recorded no exposure")
		}
	}
	f := &fakeSource{ch: make(chan struct{}, 1), snap: load(validLanguages)}
	applied := make(chan struct{})
	l := newLoop(t, f)
	l.exposure, l.applied = det, func() { applied <- struct{}{} }
	l.server, l.serverOK = f.snap.Server()
	go func() { _ = l.run(t.Context()) }()
	for _, c := range []struct {
		name, text string
		reset      bool
	}{
		{"another section changed", validLanguages + "auth:\n  breached_passwords: true\n", false},
		{"the server section failed, its fail-closed values the defaults", validLanguages + "server:\n  trusted_proxies: [nope]\n", true},
		{"the server section absent again", validLanguages, true},
		{"trusted_proxies changed", validLanguages + "server:\n  trusted_proxies: [10.0.0.0/8]\n", true},
		{"the same server section again", validLanguages + "server:\n  trusted_proxies: [10.0.0.0/8]\n", false},
		{"log_level changed", validLanguages + "server:\n  trusted_proxies: [10.0.0.0/8]\n  log_level: debug\n", true},
	} {
		expose()
		f.snap = load(c.text)
		f.ch <- struct{}{}
		select {
		case <-applied:
		case <-time.After(waitBudget):
			t.Fatal("the loop did not apply the change")
		}
		if got := !seen(); got != c.reset {
			t.Errorf("%s: exposure reset %v, want %v", c.name, got, c.reset)
		}
	}
}

func TestDrainsPreferDone(t *testing.T) {
	dir := t.TempDir()
	store, err := statedb.Open(filepath.Join(dir, "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	st, err := status.New(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	snap := settings.Load(path, settings.Registries{})
	svc := settings.NewService(path, settings.Registries{}, snap, nil, nil, nil)
	m := obs.New()
	accts, err := newAccounts(t.Context(), store, svc, m)
	if err != nil {
		t.Fatal(err)
	}
	backups := backup.New(backup.Deps{Store: store, Status: st, Metrics: m, Config: func() backup.Config { return backup.Config{} }})
	sock := adminsock.New(adminsock.Deps{Accounts: accts, Settings: passwordLogin{svc}, Store: store})
	paths := testPaths(t, dir)
	ln, err := adminsock.Listen(t.Context(), paths.SocketDir, paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, c := range []component{
		{name: "accounts", run: accts.Run},
		{name: "backups", run: backups.Run},
		{name: "admin socket", run: func(ctx context.Context) error { return sock.Serve(ctx, ln) }},
		{name: "store health", run: func(ctx context.Context) error { return storeHealth(ctx, store, st) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			exits := make(chan exit, 1)
			done := start(canceled(t.Context()), []component{c}, exits)[0]
			<-done
			if e := <-exits; e.err != nil && !errors.Is(e.err, context.Canceled) {
				t.Fatalf("%s returned %v on its context's end", c.name, e.err)
			}
			for i := range 1000 {
				if !await(expired, done) {
					t.Fatalf("run %d: a drain past its deadline reported its closed done channel as an overrun", i)
				}
			}
		})
	}
}

func TestBootRunsBackups(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	boot(t, validLanguages+"backup:\n  enabled: true\n  path: "+dir+"\n", nil)
	if !eventually(func() bool {
		names, _ := filepath.Glob(filepath.Join(dir, "subflux-*.bolt"))
		return len(names) == 1
	}) {
		t.Error("an enabled backup section with no backup yet wrote none after boot")
	}
}
