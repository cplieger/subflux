package mediawrite

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// blockingAlerts holds the first call of op ("record" or "dismiss") for
// source until release is closed, so a test can complete an opposite
// mutation while that one's effects are in flight. Set op and source before
// the call that should block starts.
type blockingAlerts struct {
	Alerts
	entered, release chan struct{}
	op, source       string
	once             sync.Once
}

func newBlockingAlerts() (*blockingAlerts, harnessOpt) {
	b := &blockingAlerts{entered: make(chan struct{}), release: make(chan struct{})}
	return b, func(c *Config) {
		b.Alerts = c.Alerts
		c.Alerts = b
	}
}

func (b *blockingAlerts) hold(op, source string) {
	if op == b.op && source == b.source {
		b.once.Do(func() {
			close(b.entered)
			<-b.release
		})
	}
}

func (b *blockingAlerts) RecordPersistent(source, msg string) {
	b.hold("record", source)
	b.Alerts.RecordPersistent(source, msg)
}

func (b *blockingAlerts) DismissBySource(source string) {
	b.hold("dismiss", source)
	b.Alerts.DismissBySource(source)
}

// B recovers while A, under the same root, is marked; B's alert dismissal
// is still running when A's fault lands, and must not reset the root gauge.
func TestWriter_a_fault_after_a_slow_recovery_keeps_the_root_gauge_set(t *testing.T) {
	block, opt := newBlockingAlerts()
	h := newHarness(t, opt)
	a, b := h.dir(t, "tv/A"), h.dir(t, "tv/B")
	h.setWrite(failUnder(b, erofs))
	if err := h.w.WriteFile(t.Context(), filepath.Join(b, "e.srt"), []byte("1\n")); !errors.Is(err, ErrUnwritable) {
		t.Fatalf("Setup: WriteFile(B) = %v, want ErrUnwritable", err)
	}

	block.op, block.source = "dismiss", folderAlertPrefix+b
	h.setWrite(failUnder(a, erofs))
	recovered := make(chan error, 1)
	go func() { recovered <- h.w.WriteFile(t.Context(), filepath.Join(b, "e.srt"), []byte("1\n")) }()
	<-block.entered

	if err := h.w.WriteFile(t.Context(), filepath.Join(a, "e.srt"), []byte("1\n")); !errors.Is(err, ErrUnwritable) {
		t.Fatalf("WriteFile(A) = %v, want ErrUnwritable", err)
	}
	close(block.release)
	if err := <-recovered; err != nil {
		t.Fatalf("WriteFile(B) after recovery = %v, want nil", err)
	}

	if g := h.gauge(t, h.root); g != 1 {
		t.Errorf("gauge = %v, want 1 while A is marked", g)
	}
	if !h.alerts.HasUndismissed(folderAlertPrefix + a) {
		t.Errorf("alerts = %v, want A's alert raised", h.alertSources())
	}
	if h.alerts.HasUndismissed(folderAlertPrefix + b) {
		t.Error("B's alert is still raised after B recovered")
	}
}

// A recovers while the alert of its own fault is still being raised; the
// recovery's dismissal must land after that raise, not before it.
func TestWriter_a_recovery_during_a_slow_raise_leaves_no_alert(t *testing.T) {
	block, opt := newBlockingAlerts()
	h := newHarness(t, opt)
	a := h.dir(t, "tv/A")
	block.op, block.source = "record", folderAlertPrefix+a
	h.setWrite(failUnder(a, erofs))
	faulted := make(chan error, 1)
	go func() { faulted <- h.w.WriteFile(t.Context(), filepath.Join(a, "e.srt"), []byte("1\n")) }()
	<-block.entered

	h.setWrite(nil)
	if err := h.w.WriteFile(t.Context(), filepath.Join(a, "e.srt"), []byte("1\n")); err != nil {
		t.Fatalf("WriteFile(A) after recovery = %v, want nil", err)
	}
	close(block.release)
	if err := <-faulted; !errors.Is(err, ErrUnwritable) {
		t.Fatalf("the faulting WriteFile(A) = %v, want ErrUnwritable", err)
	}

	if h.alerts.HasUndismissed(folderAlertPrefix + a) {
		t.Error("A's alert is raised although A recovered after the fault")
	}
	if g := h.gauge(t, h.root); g != 0 {
		t.Errorf("gauge = %v, want 0 after A recovered", g)
	}
	if f, ok := h.w.Blocked(filepath.Join(a, "e.mkv")); ok {
		t.Errorf("Blocked = %q, true; want A cleared", f)
	}
}

// A probe asked for while the previous probe's verdict is still being applied
// waits for it and then probes anew, so the newer observation is the one the
// folder ends with.
func TestProbe_a_new_probe_waits_until_the_previous_verdict_is_applied(t *testing.T) {
	tests := []struct {
		name       string
		hold       string // the alert call the first verdict's settlement pauses in
		firstFails bool
	}{
		{name: "pass_then_fault", hold: "dismiss"},
		{name: "fault_then_pass", hold: "record", firstFails: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				block, opt := newBlockingAlerts()
				h := newHarness(t, opt)
				roots := PreflightRequest{Roots: true}
				fails := failUnder(h.root, erofs)
				var passes func(context.Context, string, []byte) error
				firstWrite, secondWrite := passes, fails
				if tc.firstFails {
					firstWrite, secondWrite = fails, passes
				} else {
					h.setWrite(fails)
					if err := h.w.Preflight(t.Context(), roots); !errors.Is(err, ErrUnwritable) {
						t.Fatalf("Setup: Preflight over a failing root = %v, want ErrUnwritable", err)
					}
				}

				block.op, block.source = tc.hold, folderAlertPrefix+h.root
				h.setWrite(firstWrite)
				firstErr := make(chan error, 1)
				go func() { firstErr <- h.w.Preflight(t.Context(), roots) }()
				<-block.entered
				h.setWrite(secondWrite)
				before := h.probeWrites(h.root)
				secondErr := make(chan error, 1)
				go func() { secondErr <- h.w.Preflight(t.Context(), roots) }()
				synctest.Wait()
				if n := h.probeWrites(h.root) - before; n != 0 {
					t.Errorf("probe writes started while the first verdict was being applied = %d, want 0", n)
				}
				close(block.release)

				if err := <-firstErr; errors.Is(err, ErrUnwritable) != tc.firstFails {
					t.Errorf("first Preflight = %v, want unwritable %v", err, tc.firstFails)
				}
				if err := <-secondErr; errors.Is(err, ErrUnwritable) == tc.firstFails {
					t.Errorf("second Preflight = %v, want unwritable %v", err, !tc.firstFails)
				}
				if _, blocked := h.w.Blocked(filepath.Join(h.root, "a.mkv")); blocked == tc.firstFails {
					t.Errorf("Blocked after both verdicts = %v, want %v (the second probe's)", blocked, !tc.firstFails)
				}
				if raised := h.alerts.HasUndismissed(folderAlertPrefix + h.root); raised == tc.firstFails {
					t.Errorf("root alert raised after both verdicts = %v, want %v", raised, !tc.firstFails)
				}
			})
		})
	}
}

// A hung probe that finishes while its timeout fault is still being applied
// stays registered until that verdict lands, so the next probe cannot settle
// first and be overwritten by it.
func TestProbe_a_late_finish_waits_out_its_timeout_verdict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block, opt := newBlockingAlerts()
		h := newHarness(t, opt)
		roots := PreflightRequest{Roots: true}
		hung := make(chan struct{})
		h.setWrite(func(context.Context, string, []byte) error { <-hung; return nil })
		block.op, block.source = "record", folderAlertPrefix+h.root
		timedOut := make(chan error, 1)
		go func() { timedOut <- h.w.Preflight(t.Context(), roots) }()
		<-block.entered
		h.setWrite(nil)
		close(hung)
		synctest.Wait()

		before := h.probeWrites(h.root)
		next := make(chan error, 1)
		go func() { next <- h.w.Preflight(t.Context(), roots) }()
		synctest.Wait()
		if n := h.probeWrites(h.root) - before; n != 0 {
			t.Errorf("probe writes started while the timeout verdict was being applied = %d, want 0", n)
		}
		close(block.release)

		if err := <-timedOut; !errors.Is(err, errProbeTimeout) {
			t.Errorf("Preflight over the hung write = %v, want the timeout", err)
		}
		if err := <-next; err != nil {
			t.Errorf("Preflight after the write recovered = %v, want nil", err)
		}
		if f, blocked := h.w.Blocked(filepath.Join(h.root, "a.mkv")); blocked {
			t.Errorf("Blocked = %q, true; want the later passing probe's verdict", f)
		}
	})
}

// blockingHandler holds every record in Handle until release is closed.
type blockingHandler struct {
	slog.Handler
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingHandler) Handle(context.Context, slog.Record) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

func TestPreflight_the_missing_roots_warning_is_logged_outside_the_lock(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.w.Bind(nil, h.validate)
	handler := &blockingHandler{Handler: h.logs, entered: make(chan struct{}), release: make(chan struct{})}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	preflighted := make(chan error, 1)
	go func() { preflighted <- h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}) }()
	<-handler.entered

	checked := make(chan bool, 1)
	go func() {
		_, blocked := h.w.Blocked(filepath.Join(show, "a.mkv"))
		checked <- blocked
	}()
	select {
	case blocked := <-checked:
		if blocked {
			t.Error("Blocked = true with no media_roots bound, want false")
		}
	case <-time.After(5 * time.Second):
		t.Error("Blocked could not take the writer's lock while the media_roots warning was being logged")
	}
	close(handler.release)
	if err := <-preflighted; err != nil {
		t.Errorf("Preflight with no media_roots = %v, want nil", err)
	}
}

// blockingMetrics holds the first SetMediaRootUnwritable(_, true) until
// release is closed.
type blockingMetrics struct {
	Metrics
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingMetrics) SetMediaRootUnwritable(root string, unwritable bool) {
	if unwritable {
		b.once.Do(func() {
			close(b.entered)
			<-b.release
		})
	}
	b.Metrics.SetMediaRootUnwritable(root, unwritable)
}

func TestWriter_a_paused_metric_call_holds_no_lock(t *testing.T) {
	block := &blockingMetrics{entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, func(c *Config) {
		block.Metrics = c.Metrics
		c.Metrics = block
	})
	show := h.dir(t, "tv/Show")
	h.setWrite(failUnder(show, erofs))
	faulted := make(chan error, 1)
	go func() { faulted <- h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n")) }()
	<-block.entered

	checked := make(chan bool, 1)
	go func() {
		_, blocked := h.w.Blocked(filepath.Join(show, "a.mkv"))
		checked <- blocked
	}()
	select {
	case blocked := <-checked:
		if !blocked {
			t.Error("Blocked = false while the fault's gauge call was paused, want true")
		}
	case <-time.After(5 * time.Second):
		t.Error("Blocked could not take the writer's lock while a metric call was paused")
	}
	close(block.release)
	if err := <-faulted; !errors.Is(err, ErrUnwritable) {
		t.Errorf("WriteFile = %v, want ErrUnwritable", err)
	}
	if g := h.gauge(t, h.root); g != 1 {
		t.Errorf("gauge = %v, want 1", g)
	}
}
