package status_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

func hasAlert(s *status.Store, src status.AlertSource) bool {
	snap, _ := s.Snapshot()
	for _, a := range snap.Alerts {
		if a.Source == src {
			return true
		}
	}
	return false
}

func stored(t *testing.T, p status.Persister, id status.JobID) status.Job {
	t.Helper()
	j, ok, err := p.GetJob(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("GetJob(%s) = %v, %v", id, ok, err)
	}
	return j
}

// failTerminal makes the first n terminal writes fail; landed makes each
// failed write store the record first, as a write whose commit landed but
// reported failure would.
func failTerminal(h *hookPersister, n int32, landed bool) *atomic.Int32 {
	var calls atomic.Int32
	h.setPut(func(ctx context.Context, j status.Job) error {
		if j.State != status.Done || calls.Add(1) > n {
			return nil
		}
		if landed {
			if err := h.MemPersister.PutJob(ctx, j); err != nil {
				return err
			}
		}
		return errors.New("disk full")
	})
	return &calls
}

func TestTerminalPersistRetried(t *testing.T) {
	for _, landed := range []bool{false, true} {
		name := "failed"
		if landed {
			name = "landed_then_failed"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := capture.Default(t)
				h := newHook()
				s := open(t, h)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error)
				go func() { done <- s.Run(ctx) }()
				calls := failTerminal(h, 2, landed)
				j := begin(t, s, 7, nil, nil)
				finish(s, j.ID, status.Succeeded)
				if !hasAlert(s, status.AlertJobRecords) {
					t.Fatal("a failed terminal write raised no job_records alert")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if calls.Load() != 2 || !hasAlert(s, status.AlertJobRecords) {
					t.Errorf("after 1 s: %d attempts, alert %v; want the second attempt failed and the alert kept", calls.Load(), hasAlert(s, status.AlertJobRecords))
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if calls.Load() != 2 {
					t.Errorf("a retry ran 1 s after a failed retry (%d attempts); want the backoff doubled", calls.Load())
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if got := stored(t, h, j.ID); got.State != status.Done || got.Outcome != status.Succeeded {
					t.Errorf("stored %v %v, want Done Succeeded", got.State, got.Outcome)
				}
				if hasAlert(s, status.AlertJobRecords) {
					t.Error("job_records still raised after the record was stored")
				}
				if n := rec.CountLevel(slog.LevelWarn, "could not store a finished job"); n != 2 {
					t.Errorf("%d WARN lines, want one per failure", n)
				}
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Run = %v", err)
				}
			})
		})
	}
}

func TestShutdownMakesLastAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHook()
		s := open(t, h)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- s.Run(ctx) }()
		failTerminal(h, 1, false)
		j := begin(t, s, 7, nil, nil)
		finish(s, j.ID, status.Failed)
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
		if got := stored(t, h, j.ID); got.State != status.Done {
			t.Errorf("after shutdown the record reads %v, want Done", got.State)
		}
	})
}

func TestTerminalPersistIsDetached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHook()
		s := open(t, h)
		var deadline time.Duration
		h.setPut(func(ctx context.Context, j status.Job) error {
			d, ok := ctx.Deadline()
			if !ok {
				return errors.New("no deadline")
			}
			deadline = time.Until(d)
			return nil
		})
		j := begin(t, s, 7, nil, nil)
		finish(s, j.ID, status.Succeeded)
		if deadline != 5*time.Second {
			t.Errorf("terminal write budget %v, want 5s", deadline)
		}
	})
}

func TestRunSweepsOldRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := capture.Default(t)
		mem := statustest.NewMemPersister()
		now := time.Now()
		ins := func(id status.JobID, ended time.Time) {
			j := status.Job{ID: id, Kind: status.Scan, Owner: 7, State: status.Done, Outcome: status.Succeeded, Ended: ended, Rev: 1}
			if _, _, err := mem.InsertJob(t.Context(), j, status.DefaultLimits); err != nil {
				t.Fatal(err)
			}
		}
		const old, newer = status.JobID("00000000000000000000000000000001"), status.JobID("00000000000000000000000000000002")
		ins(old, now.Add(-25*time.Hour))
		ins(newer, now.Add(-23*time.Hour))
		s := open(t, mem)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- s.Run(ctx) }()
		synctest.Wait()
		if _, ok, _ := mem.GetJob(t.Context(), old); ok {
			t.Error("Run's first sweep kept a record ended 25 h ago")
		}
		if _, ok, _ := mem.GetJob(t.Context(), newer); !ok {
			t.Error("the sweep deleted a record ended 23 h ago")
		}
		time.Sleep(2*time.Hour + time.Second)
		synctest.Wait()
		if _, ok, _ := mem.GetJob(t.Context(), newer); ok {
			t.Error("the hourly sweep kept a record ended over 24 h ago")
		}
		if n := rec.CountLevel(slog.LevelInfo, "job record sweep finished"); n != 3 {
			t.Errorf("%d sweep completions logged, want 3 (start and two hours)", n)
		}
		cancel()
		<-done
	})
}

// failingSweep fails DeleteJobsEndedBefore.
type failingSweep struct{ *statustest.MemPersister }

func (failingSweep) DeleteJobsEndedBefore(context.Context, time.Time) (int, error) {
	return 0, errors.New("bolt: closed")
}

func TestSweepFailureWarns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := capture.Default(t)
		s := open(t, failingSweep{statustest.NewMemPersister()})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- s.Run(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v", err)
		}
		if rec.CountLevel(slog.LevelWarn, "job record sweep failed") != 1 {
			t.Errorf("logged %q, want one WARN", rec.Messages())
		}
	})
}

// failingOpen fails the reads and writes New makes.
type failingOpen struct {
	*statustest.MemPersister
	read, write bool
}

func (f failingOpen) UnfinishedJobs(ctx context.Context) ([]status.Job, error) {
	if f.read {
		return nil, errors.New("bolt: closed")
	}
	return f.MemPersister.UnfinishedJobs(ctx)
}

func (f failingOpen) PutJob(ctx context.Context, j status.Job) error {
	if f.write {
		return errors.New("bolt: read-only")
	}
	return f.MemPersister.PutJob(ctx, j)
}

func TestNewFailures(t *testing.T) {
	if _, err := status.New(t.Context(), nil); err == nil {
		t.Error("New(nil) succeeded")
	}
	if _, err := status.New(t.Context(), failingOpen{MemPersister: statustest.NewMemPersister(), read: true}); err == nil {
		t.Error("New over a failing read succeeded")
	}
	mem := statustest.NewMemPersister()
	if _, _, err := mem.InsertJob(t.Context(), status.Job{ID: "00000000000000000000000000000003", Kind: status.Scan, Owner: 7, State: status.Running, Rev: 1}, status.DefaultLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := status.New(t.Context(), failingOpen{MemPersister: mem, write: true}); err == nil {
		t.Error("New that could not mark a record interrupted succeeded")
	}
}
