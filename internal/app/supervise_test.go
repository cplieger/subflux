package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitBudget bounds every wait in these tests; a supervisor that misses a
// cause fails at it with a diagnostic instead of hanging.
const waitBudget = 5 * time.Second

// journal records the drain's steps in order.
type journal struct {
	steps []string
	mu    sync.Mutex
}

func (j *journal) add(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.steps = append(j.steps, s)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.steps...)
}

func (j *journal) index(s string) int {
	for i, x := range j.list() {
		if x == s {
			return i
		}
	}
	return -1
}

// blocker runs until its context ends, then records its name.
func blocker(j *journal, name string) component {
	return component{name: name, run: func(ctx context.Context) error {
		<-ctx.Done()
		j.add(name + " stopped")
		return nil
	}}
}

func returner(name string, err error, after <-chan struct{}) component {
	return component{name: name, run: func(context.Context) error {
		<-after
		return err
	}}
}

func base(j *journal) *supervision {
	return &supervision{
		beginDrain: func() { j.add("drain") },
		shutdownSettings: func(context.Context) error {
			j.add("settings")
			return nil
		},
		closeStore: func() error {
			j.add("store closed")
			return nil
		},
		drainBudget: waitBudget,
		httpGrace:   waitBudget,
	}
}

func superviseAsync(ctx context.Context, sv *supervision) <-chan error {
	done := make(chan error, 1)
	go func() { done <- supervise(ctx, sv) }()
	return done
}

func result(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(waitBudget):
		t.Fatal("supervise did not return: a component's return was not seen as a cause")
		return nil
	}
}

func TestSuperviseFirstCause(t *testing.T) {
	t.Run("producer_error", func(t *testing.T) {
		j := &journal{}
		boom := errors.New("listener gone")
		sv := base(j)
		sv.producers = []component{returner("http server", boom, closed()), blocker(j, "loop")}
		sv.sinks = []component{blocker(j, "status")}
		err := result(t, superviseAsync(t.Context(), sv))
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "http server") {
			t.Errorf("supervise = %v, want the producer's error naming it", err)
		}
		steps := j.list()
		drain, loop, settings, sink, closed := j.index("drain"), j.index("loop stopped"), j.index("settings"), j.index("status stopped"), j.index("store closed")
		if drain != 0 || drain > loop || loop > settings || settings > sink || sink > closed {
			t.Errorf("drain order %v; want drain, producers, settings, sinks, store", steps)
		}
	})
	t.Run("sink_returns_early", func(t *testing.T) {
		j := &journal{}
		sv := base(j)
		sv.producers = []component{blocker(j, "http server")}
		sv.sinks = []component{returner("status", nil, closed())}
		if err := result(t, superviseAsync(t.Context(), sv)); err == nil || err.Error() != "status exited" {
			t.Errorf("supervise = %v, want \"status exited\"", err)
		}
		if j.index("http server stopped") < 0 || j.index("store closed") < 0 {
			t.Errorf("drain steps %v; want the producer stopped and the store closed", j.list())
		}
	})
	t.Run("ctx_ends", func(t *testing.T) {
		j := &journal{}
		ctx, cancel := context.WithCancel(t.Context())
		sv := base(j)
		producerStopped := make(chan struct{})
		sv.producers = []component{{name: "http server", run: func(ctx context.Context) error {
			<-ctx.Done()
			close(producerStopped)
			return nil
		}}}
		sv.sinks = []component{blocker(j, "status")}
		sv.beginDrain = func() {
			select {
			case <-producerStopped:
				j.add("producer stopped before drain")
			case <-time.After(100 * time.Millisecond):
			}
			j.add("drain")
		}
		done := superviseAsync(ctx, sv)
		cancel()
		if err := result(t, done); err != nil {
			t.Errorf("supervise = %v, want nil", err)
		}
		if j.index("drain") != 0 || j.index("store closed") < 0 {
			t.Errorf("drain steps %v; want drain first, before ctx's end reaches a producer", j.list())
		}
	})
	t.Run("stop_after_drain", func(t *testing.T) {
		j := &journal{}
		ctx, cancel := context.WithCancel(t.Context())
		sv := base(j)
		served := make(chan struct{})
		sv.producers = []component{{
			name: "http server",
			run: func(context.Context) error {
				<-served
				j.add("http server stopped")
				return nil
			},
			stop: func(context.Context) error {
				j.add("stop")
				close(served)
				return nil
			},
		}}
		done := superviseAsync(ctx, sv)
		cancel()
		if err := result(t, done); err != nil {
			t.Errorf("supervise = %v, want nil", err)
		}
		drain, stop, stopped, settings := j.index("drain"), j.index("stop"), j.index("http server stopped"), j.index("settings")
		if drain != 0 || stop < drain || stopped < stop || settings < stopped || j.index("store closed") < 0 {
			t.Errorf("drain steps %v; want drain, stop, the producer's return, settings, store", j.list())
		}
	})
	t.Run("stop_error", func(t *testing.T) {
		j := &journal{}
		boom, grace := errors.New("listener gone"), errors.New("grace expired")
		sv := base(j)
		sv.producers = []component{{
			name: "http server",
			run:  func(context.Context) error { return boom },
			stop: func(context.Context) error { return grace },
		}}
		err := result(t, superviseAsync(t.Context(), sv))
		if !errors.Is(err, boom) || !errors.Is(err, grace) || !strings.HasPrefix(err.Error(), "http server: listener gone") {
			t.Errorf("supervise = %v, want the cause first, joined with the stop's error", err)
		}
		if j.index("store closed") >= 0 {
			t.Error("the store closed after a stop error")
		}
	})
	t.Run("producer_overrun", func(t *testing.T) {
		j := &journal{}
		boom := errors.New("listener gone")
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		sv := base(j)
		sv.httpGrace = 50 * time.Millisecond
		sv.producers = []component{returner("http server", boom, closed()), {name: "stuck", run: func(context.Context) error {
			<-release
			return nil
		}}}
		err := result(t, superviseAsync(t.Context(), sv))
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "producers overran") {
			t.Errorf("supervise = %v, want the cause joined with the overrun", err)
		}
		if j.index("store closed") >= 0 {
			t.Error("the store closed after an overrun")
		}
		if j.index("settings") < 0 {
			t.Error("settings Shutdown did not run after a producer overrun")
		}
	})
	t.Run("drain_error", func(t *testing.T) {
		j := &journal{}
		flush := errors.New("flush failed")
		ctx, cancel := context.WithCancel(t.Context())
		sv := base(j)
		sv.producers = []component{{name: "writer", run: func(ctx context.Context) error {
			<-ctx.Done()
			return flush
		}}, {name: "loop", run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}}}
		done := superviseAsync(ctx, sv)
		cancel()
		err := result(t, done)
		if !errors.Is(err, flush) || errors.Is(err, context.Canceled) {
			t.Errorf("supervise = %v, want the writer's drain error and not the loop's cancellation", err)
		}
		if j.index("store closed") >= 0 {
			t.Error("the store closed after a drain error")
		}
	})
	t.Run("sink_overrun", func(t *testing.T) {
		j := &journal{}
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		ctx, cancel := context.WithCancel(t.Context())
		sv := base(j)
		sv.drainBudget = 50 * time.Millisecond
		sv.sinks = []component{{name: "stuck", run: func(context.Context) error {
			<-release
			return nil
		}}}
		done := superviseAsync(ctx, sv)
		cancel()
		if err := result(t, done); err == nil || !strings.Contains(err.Error(), "sinks overran") {
			t.Errorf("supervise = %v, want a sink overrun", err)
		}
		if j.index("store closed") >= 0 {
			t.Error("the store closed after an overrun")
		}
	})
	t.Run("settings_overrun", func(t *testing.T) {
		j := &journal{}
		ctx, cancel := context.WithCancel(t.Context())
		sv := base(j)
		sv.httpGrace = 50 * time.Millisecond
		sv.shutdownSettings = func(ctx context.Context) error {
			<-ctx.Done()
			j.add("settings")
			return ctx.Err()
		}
		sv.sinks = []component{blocker(j, "status")}
		done := superviseAsync(ctx, sv)
		cancel()
		err := result(t, done)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "settings") {
			t.Errorf("supervise = %v, want the settings overrun", err)
		}
		if j.index("settings") > j.index("status stopped") {
			t.Errorf("drain steps %v; want settings before the sinks", j.list())
		}
		if j.index("store closed") >= 0 {
			t.Error("the store closed with a Save still running")
		}
	})
}

func closed() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func TestRunWithoutSinksServesUntilCancel(t *testing.T) {
	j := &journal{}
	ctx, cancel := context.WithCancel(t.Context())
	sv := base(j)
	sv.producers = []component{blocker(j, "http server"), blocker(j, "loop")}
	done := superviseAsync(ctx, sv)
	select {
	case err := <-done:
		t.Fatalf("supervise returned %v with no sink and no cause", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := result(t, done); err != nil {
		t.Errorf("supervise = %v, want nil", err)
	}
}

func TestNilProducerReturnStartsDrainWhileSiblingRuns(t *testing.T) {
	j := &journal{}
	sv := base(j)
	sv.producers = []component{returner("loop", nil, closed()), blocker(j, "http server")}
	err := result(t, superviseAsync(t.Context(), sv))
	if err == nil || err.Error() != "loop exited" {
		t.Errorf("supervise = %v, want \"loop exited\"", err)
	}
	if j.index("http server stopped") < 0 {
		t.Error("the sibling producer was not stopped")
	}
}

func TestNilSinkReturnStartsDrainWhileSiblingRuns(t *testing.T) {
	j := &journal{}
	sv := base(j)
	sv.producers = []component{blocker(j, "http server")}
	sv.sinks = []component{returner("audit", nil, closed()), blocker(j, "status")}
	err := result(t, superviseAsync(t.Context(), sv))
	if err == nil || err.Error() != "audit exited" {
		t.Errorf("supervise = %v, want \"audit exited\"", err)
	}
	if j.index("status stopped") < 0 || j.index("http server stopped") < 0 {
		t.Errorf("drain steps %v; want both siblings stopped", j.list())
	}
}
