package syncworker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/subsync"
)

// Typed-core tests (D1): every outcome is discriminated by its own NAMED
// signal — the budget's cause sentinel, the job context, the exit state —
// never by error text and never inferred from ctx.Err alone.

func TestRunAudio_result_carries_the_worker_result(t *testing.T) {
	t.Parallel()
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return &Response{Version: ProtocolVersion, Result: wireFromResult(&subsync.SyncResult{
			Method: subsync.MethodAudio, Offset: 1500, Confidence: 0.9,
		})}, nil
	})
	out := c.RunAudio(t.Context(), []byte(tinySRT), "/v.mkv", "", nil)
	if out.Outcome != OutcomeResult {
		t.Fatalf("outcome = %q (err %v), want result", out.Outcome, out.Err)
	}
	if out.Result.Offset != 1500 || out.Result.Method != subsync.MethodAudio {
		t.Errorf("result = %+v, want the worker's offset/method", out.Result)
	}
}

func TestRunAudio_worker_reported_error_is_crash(t *testing.T) {
	t.Parallel()
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return &Response{Version: ProtocolVersion, Error: "ffmpeg exploded"}, nil
	})
	out := c.RunAudio(t.Context(), []byte(tinySRT), "/v.mkv", "", nil)
	if out.Outcome != OutcomeCrash {
		t.Fatalf("outcome = %q, want crash for a worker-reported job error", out.Outcome)
	}
	if out.Err == nil || out.Err.Error() != "ffmpeg exploded" {
		t.Errorf("err = %v, want the worker's error text", out.Err)
	}
}

func TestRunAudio_spawn_failure_is_crash(t *testing.T) {
	t.Parallel()
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return nil, errors.New("worker process: signal: killed")
	})
	out := c.RunAudio(t.Context(), []byte(tinySRT), "/v.mkv", "", nil)
	if out.Outcome != OutcomeCrash {
		t.Fatalf("outcome = %q, want crash for a spawn failure", out.Outcome)
	}
}

// TestRunAudio_budget_is_its_own_signal pins the discrimination the old
// run() could not make: the budget timer fired vs the caller's context died.
// Both cancel the run context, so only the NAMED cause sentinel can tell
// them apart — and the budget arms UNCONDITIONALLY, so a caller deadline
// LONGER than the budget still times out at the budget (the old conditional
// arm skipped the budget whenever any deadline existed).
func TestRunAudio_budget_is_its_own_signal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSeamClient(func(ctx context.Context, _ *Request) (*Response, error) {
			<-ctx.Done()
			return nil, fmt.Errorf("cancelled: %w", ctx.Err())
		})
		// A caller deadline PAST the budget: the budget must still fire first.
		ctx, cancel := context.WithTimeout(t.Context(), maxWorkerRuntime+25*time.Minute)
		defer cancel()
		start := time.Now()
		out := c.RunAudio(ctx, []byte(tinySRT), "/v.mkv", "", nil)
		if out.Outcome != OutcomeTimeout {
			t.Fatalf("outcome = %q (err %v), want timeout from the budget sentinel", out.Outcome, out.Err)
		}
		if elapsed := time.Since(start); elapsed != maxWorkerRuntime {
			t.Errorf("budget fired after %v, want exactly %v (virtual clock)", elapsed, maxWorkerRuntime)
		}
	})
}

func TestRunAudio_caller_cancellation_is_cancelled_not_timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newSeamClient(func(ctx context.Context, _ *Request) (*Response, error) {
			<-ctx.Done()
			return nil, fmt.Errorf("cancelled: %w", ctx.Err())
		})
		// The caller dies BEFORE the budget: same deadline-shaped ctx error,
		// different named signal, different outcome.
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		defer cancel()
		out := c.RunAudio(ctx, []byte(tinySRT), "/v.mkv", "", nil)
		if out.Outcome != OutcomeCancelled {
			t.Fatalf("outcome = %q (err %v), want cancelled for a caller deadline", out.Outcome, out.Err)
		}
	})
}

// TestRunAudio_no_budget_while_queued pins the admission lease half: a job
// waiting for the single slot has NO budget running — 20 virtual minutes in
// the queue (past the 15-minute budget) still ends in a clean result.
func TestRunAudio_no_budget_while_queued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
			<-release
			return &Response{Version: ProtocolVersion}, nil
		})
		first := make(chan RunOutcome, 1)
		go func() { first <- c.RunAudio(t.Context(), nil, "/hold.mkv", "", nil) }()
		second := make(chan RunOutcome, 1)
		go func() { second <- c.RunAudio(t.Context(), nil, "/queued.mkv", "", nil) }()

		// Both goroutines are parked (one in the seam, one at the slot);
		// the virtual clock advances PAST the budget while the second waits.
		time.Sleep(maxWorkerRuntime + 5*time.Minute)
		close(release)

		if out := <-first; out.Outcome != OutcomeResult {
			t.Errorf("first outcome = %q (err %v), want result", out.Outcome, out.Err)
		}
		if out := <-second; out.Outcome != OutcomeResult {
			t.Errorf("queued outcome = %q (err %v), want result — the budget must arm at ADMISSION, not at dispatch", out.Outcome, out.Err)
		}
	})
}

func TestRunAudio_hook_refusal_never_spawns(t *testing.T) {
	t.Parallel()
	var spawned atomic.Int32
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		spawned.Add(1)
		return &Response{Version: ProtocolVersion}, nil
	})
	out := c.RunAudio(t.Context(), nil, "/v.mkv", "", func() bool { return false })
	if out.Outcome != OutcomeCancelled || !errors.Is(out.Err, ErrAdmissionRefused) {
		t.Fatalf("outcome = %q err %v, want cancelled with ErrAdmissionRefused", out.Outcome, out.Err)
	}
	if spawned.Load() != 0 {
		t.Errorf("worker spawned %d times after a refused admission, want 0", spawned.Load())
	}
}

func TestRunAudio_hook_fires_once_at_slot_acquisition(t *testing.T) {
	t.Parallel()
	var hooks atomic.Int32
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return &Response{Version: ProtocolVersion}, nil
	})
	out := c.RunAudio(t.Context(), nil, "/v.mkv", "", func() bool {
		hooks.Add(1)
		return true
	})
	if out.Outcome != OutcomeResult {
		t.Fatalf("outcome = %q, want result", out.Outcome)
	}
	if hooks.Load() != 1 {
		t.Errorf("hook invoked %d times, want exactly 1", hooks.Load())
	}
}

func TestRunAudio_cancelled_while_waiting_for_the_slot(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	occupied := make(chan struct{})
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		close(occupied)
		<-release
		return &Response{Version: ProtocolVersion}, nil
	})
	go c.RunAudio(t.Context(), nil, "/hold.mkv", "", nil)
	<-occupied

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var hooked atomic.Int32
	out := c.RunAudio(ctx, nil, "/queued.mkv", "", func() bool { hooked.Add(1); return true })
	close(release)
	if out.Outcome != OutcomeCancelled {
		t.Fatalf("outcome = %q, want cancelled while queued", out.Outcome)
	}
	if hooked.Load() != 0 {
		t.Errorf("hook fired %d times for a never-admitted job, want 0", hooked.Load())
	}
}

// TestNewInProcess_runs_the_job_in_this_process pins the fallback client:
// same typed core, no child process.
func TestNewInProcess_runs_the_job_in_this_process(t *testing.T) {
	t.Parallel()
	c := NewInProcess()
	// No video file: the audio strategy reports a no-change result, proving
	// the in-process spawn adapter completes the round trip.
	out := c.RunAudio(t.Context(), []byte(tinySRT), "/nonexistent/v.mkv", "", nil)
	if out.Outcome != OutcomeResult {
		t.Fatalf("outcome = %q (err %v), want result from the in-process adapter", out.Outcome, out.Err)
	}
	if out.Result.Applied() {
		t.Errorf("result = %+v, want no-change without a video", out.Result)
	}
}
