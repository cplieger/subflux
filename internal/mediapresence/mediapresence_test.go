package mediapresence_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediapresence"
)

type fakeAlerts struct {
	open map[string]string
	mu   sync.Mutex
}

func (a *fakeAlerts) RecordPersistent(source, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.open[source] = msg
}

func (a *fakeAlerts) DismissBySource(source string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.open, source)
}

func (a *fakeAlerts) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.open)
}

type fakeMetrics struct {
	gauge map[string]bool
	mu    sync.Mutex
}

func (m *fakeMetrics) SetMediaRootUnavailable(root string, unavailable bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauge[root] = unavailable
}

func (m *fakeMetrics) DeleteMediaRootUnavailable(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.gauge, root)
}

func (m *fakeMetrics) value(root string) (unavailable, present bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	unavailable, present = m.gauge[root]
	return unavailable, present
}

type rig struct {
	c       *mediapresence.Checker
	alerts  *fakeAlerts
	metrics *fakeMetrics
}

func newRig(t *testing.T, cfg mediapresence.Config, roots ...string) rig {
	t.Helper()
	r := rig{alerts: &fakeAlerts{open: map[string]string{}}, metrics: &fakeMetrics{gauge: map[string]bool{}}}
	cfg.Alerts, cfg.Metrics = r.alerts, r.metrics
	c, err := mediapresence.New(cfg)
	if err != nil {
		t.Fatalf("Setup: New: %v", err)
	}
	c.Bind(roots)
	r.c = c
	return r
}

// populatedRoot returns a root directory holding one unrelated file, the
// shape of a mounted share.
func populatedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "other.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return root
}

// statFailing answers err for path and defers to os.Stat for everything else.
func statFailing(path string, err error) func(string) (fs.FileInfo, error) {
	return func(p string) (fs.FileInfo, error) {
		if p == path {
			return nil, &fs.PathError{Op: "stat", Path: p, Err: err}
		}
		return os.Stat(p)
	}
}

func TestGone_a_missing_file_under_a_mounted_root_is_gone(t *testing.T) {
	root := populatedRoot(t)
	r := newRig(t, mediapresence.Config{}, root)

	gone, err := r.c.Gone(t.Context(), filepath.Join(root, "show", "deleted.mkv"))

	if !gone || err != nil {
		t.Fatalf("Gone(deleted file under a mounted root) = %v, %v; want true, nil", gone, err)
	}
	if n := r.alerts.count(); n != 0 {
		t.Errorf("alerts after a definite deletion = %d, want 0", n)
	}
}

func TestGone_an_existing_file_is_present(t *testing.T) {
	root := populatedRoot(t)
	r := newRig(t, mediapresence.Config{}, root)

	gone, err := r.c.Gone(t.Context(), filepath.Join(root, "other.mkv"))

	if gone || err != nil {
		t.Fatalf("Gone(existing file) = %v, %v; want false, nil", gone, err)
	}
}

func TestGone_a_stat_failure_other_than_not_exist_is_a_root_fault(t *testing.T) {
	for _, tc := range []struct {
		err  error
		name string
	}{
		{name: "EACCES", err: syscall.EACCES},
		{name: "EIO", err: syscall.EIO},
		{name: "ESTALE", err: syscall.ESTALE},
		{name: "ENOTCONN", err: syscall.ENOTCONN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := populatedRoot(t)
			video := filepath.Join(root, "show", "ep.mkv")
			r := newRig(t, mediapresence.Config{Stat: statFailing(video, tc.err)}, root)

			gone, err := r.c.Gone(t.Context(), video)

			if gone || !errors.Is(err, mediapresence.ErrUnavailable) || !errors.Is(err, tc.err) {
				t.Fatalf("Gone(stat %s) = %v, %v; want false and an unavailable error wrapping %v",
					tc.name, gone, err, tc.err)
			}
			if n := r.alerts.count(); n != 1 {
				t.Errorf("alerts after a %s stat = %d, want 1", tc.name, n)
			}
			if v, _ := r.metrics.value(root); !v {
				t.Errorf("media_root_unavailable{%s} = false after a %s stat, want true", root, tc.name)
			}
		})
	}
}

func TestGone_a_missing_root_is_a_fault_not_a_deletion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unmounted")
	r := newRig(t, mediapresence.Config{}, root)

	gone, err := r.c.Gone(t.Context(), filepath.Join(root, "show", "ep.mkv"))

	if gone || !errors.Is(err, mediapresence.ErrUnavailable) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Gone(file under a missing root) = %v, %v; want false and an unavailable error", gone, err)
	}
}

func TestGone_an_empty_root_is_a_fault_not_a_deletion(t *testing.T) {
	root := t.TempDir()
	r := newRig(t, mediapresence.Config{}, root)

	gone, err := r.c.Gone(t.Context(), filepath.Join(root, "show", "ep.mkv"))

	if gone || !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Fatalf("Gone(file under an empty root) = %v, %v; want false and an unavailable error", gone, err)
	}
	if n := r.alerts.count(); n != 1 {
		t.Errorf("alerts after an empty root = %d, want 1", n)
	}
}

func TestGone_a_path_outside_every_root_proves_nothing(t *testing.T) {
	r := newRig(t, mediapresence.Config{}, populatedRoot(t))

	gone, err := r.c.Gone(t.Context(), filepath.Join(t.TempDir(), "ep.mkv"))

	if gone || !errors.Is(err, mediapresence.ErrOutsideRoots) {
		t.Fatalf("Gone(path outside the roots) = %v, %v; want false, ErrOutsideRoots", gone, err)
	}
	if n := r.alerts.count(); n != 0 {
		t.Errorf("alerts for a path outside the roots = %d, want 0", n)
	}
}

func TestGone_a_hung_stat_times_out_and_later_calls_do_not_wait_on_it(t *testing.T) {
	root := populatedRoot(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	hang := func(p string) (fs.FileInfo, error) {
		if p == root {
			return os.Stat(p)
		}
		calls.Add(1)
		<-release
		return nil, fs.ErrNotExist
	}
	r := newRig(t, mediapresence.Config{Stat: hang, Timeout: 20 * time.Millisecond}, root)

	start := time.Now()
	gone, err := r.c.Gone(t.Context(), filepath.Join(root, "a.mkv"))
	if gone || !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Fatalf("Gone(hung stat) = %v, %v; want false and an unavailable error", gone, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Gone(hung stat) returned after %s, want about the 20ms timeout", d)
	}

	gone, err = r.c.Gone(t.Context(), filepath.Join(root, "b.mkv"))

	if gone || !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Fatalf("Gone(second path while a stat hangs) = %v, %v; want false and an unavailable error", gone, err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("stat calls while one hangs = %d, want 1 (the second call must not start another)", n)
	}
	if _, unavailable := r.c.Unavailable(filepath.Join(root, "b.mkv")); !unavailable {
		t.Error("Unavailable(path under the hung root) = false, want true")
	}
}

func TestGone_the_faulted_path_resolving_clears_the_fault(t *testing.T) {
	root := populatedRoot(t)
	video := filepath.Join(root, "ep.mkv")
	var broken atomic.Bool
	broken.Store(true)
	stat := func(p string) (fs.FileInfo, error) {
		if p == video && broken.Load() {
			return nil, syscall.EIO
		}
		return os.Stat(p)
	}
	r := newRig(t, mediapresence.Config{Stat: stat}, root)
	if _, err := r.c.Gone(t.Context(), video); err == nil {
		t.Fatal("Setup: Gone(EIO) = nil error, want a fault")
	}

	broken.Store(false)
	gone, err := r.c.Gone(t.Context(), video)

	if !gone || err != nil {
		t.Fatalf("Gone(after recovery) = %v, %v; want true, nil", gone, err)
	}
	if n := r.alerts.count(); n != 0 {
		t.Errorf("alerts after recovery = %d, want 0", n)
	}
	if v, present := r.metrics.value(root); v || !present {
		t.Errorf("media_root_unavailable{%s} = %v (present %v) after recovery, want 0", root, v, present)
	}
}

func TestGone_a_faulted_root_holds_every_other_path_under_it(t *testing.T) {
	root := populatedRoot(t)
	video := filepath.Join(root, "locked", "ep.mkv")
	r := newRig(t, mediapresence.Config{Stat: statFailing(video, syscall.EACCES)}, root)
	if _, err := r.c.Gone(t.Context(), video); err == nil {
		t.Fatal("Setup: Gone(EACCES) = nil error, want a fault")
	}

	for _, sibling := range []string{filepath.Join(root, "other.mkv"), filepath.Join(root, "deleted.mkv")} {
		gone, err := r.c.Gone(t.Context(), sibling)
		if gone || !errors.Is(err, mediapresence.ErrUnavailable) {
			t.Errorf("Gone(%s under a faulted root) = %v, %v; want false and an unavailable error", sibling, gone, err)
		}
	}

	if n := r.alerts.count(); n != 1 {
		t.Errorf("alerts after checking siblings = %d, want 1 (the locked path still faults)", n)
	}
}

func TestGone_a_timeout_faults_the_path_that_started_the_call(t *testing.T) {
	root := populatedRoot(t)
	owner, sibling, missing := filepath.Join(root, "a.mkv"), filepath.Join(root, "other.mkv"), filepath.Join(root, "c.mkv")
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCall := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseCall)
	started := make(chan struct{}, 1)
	stat := func(p string) (fs.FileInfo, error) {
		if p == owner {
			started <- struct{}{}
			<-release
			return nil, syscall.EIO
		}
		return os.Stat(p)
	}
	r := newRig(t, mediapresence.Config{Stat: stat, Timeout: 50 * time.Millisecond}, root)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.c.Gone(ctx, owner)
	}()
	<-started
	cancel()
	<-done
	if _, err := r.c.Gone(t.Context(), sibling); !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Fatalf("Setup: Gone(sibling waiting on the hung call) error = %v, want an unavailable error", err)
	}

	releaseCall()

	deadline := time.Now().Add(5 * time.Second)
	for {
		gone, err := r.c.Gone(t.Context(), sibling)
		if gone || !errors.Is(err, mediapresence.ErrUnavailable) {
			t.Fatalf("Gone(present sibling after the owner's call failed) = %v, %v; want false and an unavailable error",
				gone, err)
		}
		if errors.Is(err, syscall.EIO) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gone(sibling) error = %v 5s after the owner's call failed, want it to carry EIO", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if gone, err := r.c.Gone(t.Context(), missing); gone || !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Errorf("Gone(missing file under the faulted root) = %v, %v; want false and an unavailable error", gone, err)
	}
	if n := r.alerts.count(); n != 1 {
		t.Errorf("alerts after the sibling checks = %d, want 1", n)
	}
}

func TestGone_a_failure_its_cancelled_caller_left_behind_still_holds_the_root(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := populatedRoot(t)
		owner, sibling, missing := filepath.Join(root, "a.mkv"), filepath.Join(root, "other.mkv"), filepath.Join(root, "c.mkv")
		release := make(chan struct{})
		stat := func(p string) (fs.FileInfo, error) {
			if p == owner {
				<-release
				return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.EIO}
			}
			return os.Stat(p)
		}
		r := newRig(t, mediapresence.Config{Stat: stat, Timeout: time.Minute}, root)
		ctx, cancel := context.WithCancel(t.Context())
		ownerErr := make(chan error, 1)
		go func() {
			_, err := r.c.Gone(ctx, owner)
			ownerErr <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-ownerErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("Setup: Gone(cancelled owner) error = %v, want context.Canceled", err)
		}
		type answer struct {
			err  error
			gone bool
		}
		joined := make(chan answer, 1)
		go func() {
			gone, err := r.c.Gone(t.Context(), sibling)
			joined <- answer{gone: gone, err: err}
		}()
		synctest.Wait()

		close(release)

		if got := <-joined; got.gone || !errors.Is(got.err, mediapresence.ErrUnavailable) || !errors.Is(got.err, syscall.EIO) {
			t.Errorf("Gone(sibling that joined the call ending in EIO) = %v, %v; want false and an unavailable error wrapping EIO",
				got.gone, got.err)
		}
		if gone, err := r.c.Gone(t.Context(), missing); gone || !errors.Is(err, mediapresence.ErrUnavailable) {
			t.Errorf("Gone(missing file after the EIO) = %v, %v; want false and an unavailable error", gone, err)
		}
		if n := r.alerts.count(); n != 1 {
			t.Errorf("alerts after the EIO = %d, want 1", n)
		}
	})
}

func TestGone_concurrent_callers_on_a_hung_root_share_one_stat(t *testing.T) {
	root := populatedRoot(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	hang := func(p string) (fs.FileInfo, error) {
		if p == root {
			return os.Stat(p)
		}
		calls.Add(1)
		<-release
		return nil, fs.ErrNotExist
	}
	r := newRig(t, mediapresence.Config{Stat: hang, Timeout: 200 * time.Millisecond}, root)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, name := range []string{"a.mkv", "b.mkv"} {
		wg.Go(func() { _, errs[i] = r.c.Gone(t.Context(), filepath.Join(root, name)) })
	}
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, mediapresence.ErrUnavailable) {
			t.Errorf("Gone(caller %d on a hung root) error = %v, want an unavailable error", i, err)
		}
	}
	start := time.Now()
	if _, err := r.c.Gone(t.Context(), filepath.Join(root, "c.mkv")); !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Errorf("Gone(later caller) error = %v, want an unavailable error", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("Gone(later caller) took %s, want an immediate answer", d)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("stat calls after three callers on a hung root = %d, want 1", n)
	}
}

func TestGone_a_cancelled_caller_leaves_its_stat_to_later_callers(t *testing.T) {
	root := populatedRoot(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	hang := func(p string) (fs.FileInfo, error) {
		if p == root {
			return os.Stat(p)
		}
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil, fs.ErrNotExist
	}
	r := newRig(t, mediapresence.Config{Stat: hang, Timeout: 200 * time.Millisecond}, root)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := r.c.Gone(ctx, filepath.Join(root, "a.mkv"))
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Gone(cancelled caller) error = %v, want context.Canceled", err)
	}

	start := time.Now()
	_, err := r.c.Gone(t.Context(), filepath.Join(root, "b.mkv"))

	if !errors.Is(err, mediapresence.ErrUnavailable) {
		t.Errorf("Gone(caller after a cancelled one) error = %v, want an unavailable error", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Gone(caller after a cancelled one) took %s, want about the 200ms timeout", d)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("stat calls after a cancelled caller and a later one = %d, want 1", n)
	}
}

func TestRun_clears_a_root_once_its_faulted_path_resolves(t *testing.T) {
	root := t.TempDir()
	r := newRig(t, mediapresence.Config{RecheckInterval: 5 * time.Millisecond}, root)
	if _, err := r.c.Gone(t.Context(), filepath.Join(root, "ep.mkv")); err == nil {
		t.Fatal("Setup: Gone(under an empty root) = nil error, want a fault")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.c.Run(ctx) }()

	if err := os.WriteFile(filepath.Join(root, "other.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("Setup: remount: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for r.alerts.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Run did not clear the fault within 5s of the root becoming readable")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run() = %v after cancel, want context.Canceled", err)
	}
}

func TestBind_dropping_a_faulted_root_dismisses_its_alert_and_series(t *testing.T) {
	root := t.TempDir()
	other := populatedRoot(t)
	r := newRig(t, mediapresence.Config{}, root, other)
	if _, err := r.c.Gone(t.Context(), filepath.Join(root, "ep.mkv")); err == nil {
		t.Fatal("Setup: Gone(under an empty root) = nil error, want a fault")
	}

	r.c.Bind([]string{other})

	if n := r.alerts.count(); n != 0 {
		t.Errorf("alerts after the faulted root was unbound = %d, want 0", n)
	}
	if _, present := r.metrics.value(root); present {
		t.Errorf("media_root_unavailable{%s} still exported after the root was unbound", root)
	}
	if v, present := r.metrics.value(other); v || !present {
		t.Errorf("media_root_unavailable{%s} = %v (present %v), want 0", other, v, present)
	}
}
