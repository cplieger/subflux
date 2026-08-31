package synchandlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/syncworker"
)

// TestExecute_cancelDuringPreHookRead_settlesCancelled pins the queued-DELETE
// race conversion (D1) across the executor's pre-hook window: a cancel that
// lands after the entry is popped but while the executor is still inside its
// pre-hook file read settles done(cancelled) — never done(crash) with an
// error sync:done. The interposed Exec stages the race deterministically by
// converting the cancel just before the executor's read observes the
// cancelled job context.
func TestExecute_cancelDuringPreHookRead_settlesCancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	racedPath := filepath.Join(dir, "raced.en.srt")
	followPath := filepath.Join(dir, "follow.en.srt")
	srt := []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n")
	for _, p := range []string{racedPath, followPath} {
		if err := os.WriteFile(p, srt, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store := &syncFakeStore{offsets: map[string]int64{}}
	exec := &AudioExecutor{Store: store, Proc: fakeProc{}, Runner: &fakeRunner{out: syncworker.RunOutcome{
		Outcome: syncworker.OutcomeResult,
		Result:  subsync.SyncResult{Method: subsync.MethodNone, Confidence: 0.1},
	}}}
	log := activity.New(50)
	published := make(chan *events.SyncDoneEvent, 8)
	racedAcc := make(chan syncjobs.Accepted, 1)

	var d *syncjobs.Dispatcher
	d = syncjobs.New(syncjobs.Deps{
		Exec: func(ctx context.Context, in *syncjobs.ExecInput, hook func() bool) syncjobs.ExecResult {
			if in.SubtitlePath == racedPath {
				// The DELETE lands while the entry is popped but not yet
				// admitted: the conversion arm answers 204 and cancels the
				// job context, which the pre-hook read then observes.
				acc := <-racedAcc
				if got := d.Cancel(acc.ActivityID); got != syncjobs.CancelConverted {
					t.Errorf("Cancel(popped, pre-hook) = %v, want CancelConverted", got)
				}
			}
			return exec.Execute(ctx, in, hook)
		},
		Log:         log,
		Stops:       &activity.StopRegistry{},
		PublishDone: func(ev *events.SyncDoneEvent) { published <- ev },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	ref := resolve.FileRef{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1",
		Language: "en", Variant: "standard", Source: "external",
	}
	acc, err := d.Dispatch(&syncjobs.ExecInput{Ref: ref, SubtitlePath: racedPath, VideoPath: filepath.Join(dir, "m.mkv")})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	racedAcc <- acc

	job := awaitJob(t, d, acc.JobID)
	if job.Outcome != subflux.JobCancelled {
		t.Errorf("raced job = %+v, want done(cancelled), never done(crash)", job)
	}
	if job.StartedAt != nil {
		t.Errorf("raced job StartedAt = %v, want nil (never admitted)", job.StartedAt)
	}

	// A follow-up job flushes the FIFO loop: its event arrives only after
	// job 1 fully settled, so a wrongful error sync:done would precede it.
	acc2, err := d.Dispatch(&syncjobs.ExecInput{Ref: ref, SubtitlePath: followPath, VideoPath: filepath.Join(dir, "m.mkv")})
	if err != nil {
		t.Fatalf("Dispatch(follow-up) error = %v", err)
	}
	select {
	case ev := <-published:
		if ev.JobID != acc2.JobID {
			t.Errorf("first sync:done = job %d (error %q), want job %d only — a never-admitted cancellation publishes nothing", ev.JobID, ev.Error, acc2.JobID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no sync:done for the follow-up job")
	}

	entry, ok := log.Get(acc.ActivityID)
	if !ok || !entry.Done || !entry.Cancelled || entry.Failed {
		t.Errorf("raced activity entry = %+v, want terminal cancelled, not failed", entry)
	}
}
