package syncworker

// Season-batch admission tests (D2), in THIS package because the REAL
// admission is the Client's single execution slot and the spawn seam that
// scripts what runs inside it lives here. The dispatcher under test is the
// real syncjobs.Dispatcher; only the worker spawn is scripted.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
)

// slotProbe scripts and observes the work that runs INSIDE the admission
// slot: per-key entry signals, per-key releases, peak concurrency.
type slotProbe struct {
	mu      sync.Mutex
	entered chan string
	release map[string]chan struct{}
	active  int
	max     int
}

func newSlotProbe(keys ...string) *slotProbe {
	p := &slotProbe{
		entered: make(chan string, 16),
		release: make(map[string]chan struct{}, len(keys)),
	}
	for _, k := range keys {
		p.release[k] = make(chan struct{})
	}
	return p
}

// spawn is the Client's spawn seam: it runs while the caller HOLDS the
// single slot, so active/max measure real admitted concurrency. The key is
// the subtitle path for batch items and the video path for the automatic
// sync (whose request carries no subtitle path).
func (p *slotProbe) spawn(ctx context.Context, req *Request) (*Response, error) {
	key := req.SubtitlePath
	if key == "" {
		key = req.VideoPath
	}
	p.mu.Lock()
	p.active++
	if p.active > p.max {
		p.max = p.active
	}
	rel := p.release[key]
	p.mu.Unlock()
	p.entered <- key
	if rel != nil {
		select {
		case <-rel:
		case <-ctx.Done():
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return &Response{Version: ProtocolVersion, Result: wireFromResult(&subsync.SyncResult{
		Method: subsync.MethodAudio, Offset: 500, Confidence: 0.9,
	})}, nil
}

func (p *slotProbe) peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

// awaitEnter waits for the slot to admit the given key.
func (p *slotProbe) awaitEnter(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-p.entered:
		if got != want {
			t.Fatalf("slot admitted %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("slot never admitted %q", want)
	}
}

func batchOf(paths ...string) *syncjobs.BatchInput {
	in := &syncjobs.BatchInput{Detail: "Fixture S01", SeriesID: 7, Season: 1}
	for i, p := range paths {
		in.Items = append(in.Items, syncjobs.ExecInput{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeEpisode,
				MediaID:   fmt.Sprintf("tvdb-81189-s01e%02d", i+1),
				Language:  "en", Variant: "standard", Source: "external",
			},
			SubtitlePath: p,
			VideoPath:    fmt.Sprintf("/tv/e%d.mkv", i+1),
		})
	}
	return in
}

// TestBatch_concurrency_exactly_one_and_automatic_interleaves pins the two
// admission properties through the REAL slot: batch items are admitted one
// at a time (observed peak concurrency 1), and BETWEEN items the automatic
// sync path (SyncExec, nil hook) can acquire the slot — a batch waits like
// anyone else while it holds it. synctest's durably-blocked guarantee is
// what makes "the automatic sync is queued at the semaphore" an observable
// instant rather than a sleep.
func TestBatch_concurrency_exactly_one_and_automatic_interleaves(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe := newSlotProbe("/e1.srt", "/e2.srt", "/auto.mkv")
		client := newSeamClient(probe.spawn)
		log := activity.New(50)
		exec := func(ctx context.Context, in *syncjobs.ExecInput, hook func() bool) syncjobs.ExecResult {
			out := client.RunAudio(ctx, []byte(tinySRT), in.VideoPath, in.SubtitlePath, hook)
			if out.Outcome != OutcomeResult {
				return syncjobs.ExecResult{Outcome: subflux.JobCancelled, Err: out.Err}
			}
			return syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true, Confidence: 0.9}
		}
		ctx, cancel := context.WithCancel(t.Context())
		d := syncjobs.New(syncjobs.Deps{
			Exec:        exec,
			Log:         log,
			Stops:       &activity.StopRegistry{},
			PublishDone: func(*events.SyncDoneEvent) {},
		})
		done := make(chan struct{})
		go func() {
			defer close(done)
			d.Run(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		acc, err := d.DispatchBatch(batchOf("/e1.srt", "/e2.srt"))
		if err != nil {
			t.Fatalf("DispatchBatch() error = %v", err)
		}
		probe.awaitEnter(t, "/e1.srt")

		// The automatic path queues at the held slot (SyncExec surface, no
		// hook), exactly as the poller's sync does.
		autoDone := make(chan struct{})
		go func() {
			defer close(autoDone)
			client.Audio(context.Background(), []byte(tinySRT), "/auto.mkv", "")
		}()
		// Everything else in the bubble is durably blocked once Wait
		// returns — the automatic sync is IN the semaphore's waiter queue.
		synctest.Wait()

		// Item 1 releases; the automatic sync — already waiting — is
		// admitted BEFORE item 2: interleaving observed at the real slot.
		close(probe.release["/e1.srt"])
		probe.awaitEnter(t, "/auto.mkv")
		close(probe.release["/auto.mkv"])
		probe.awaitEnter(t, "/e2.srt")
		close(probe.release["/e2.srt"])
		<-autoDone

		deadline := time.Now().Add(10 * time.Second)
		for {
			e, ok := log.Get(acc.ActivityID)
			if ok && e.Done {
				if e.Failed || e.Cancelled || e.Current != 2 {
					t.Errorf("batch entry = %+v, want a clean 2/2 completion", e)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("batch never settled; entry: %+v", e)
			}
			time.Sleep(time.Millisecond)
		}
		if got := probe.peak(); got != 1 {
			t.Errorf("observed admitted concurrency = %d, want EXACTLY 1 through the real slot", got)
		}
	})
}

// TestBatch_wedged_item_consumes_its_ceiling_and_the_batch_proceeds pins
// the per-item budget under the batch: a worker that never returns burns
// its own maxWorkerRuntime (virtual clock), settles timeout, and the NEXT
// item still runs — one wedge never wedges the season.
func TestBatch_wedged_item_consumes_its_ceiling_and_the_batch_proceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wedged := make(chan struct{}) // never closed: the wedge
		client := newSeamClient(func(ctx context.Context, req *Request) (*Response, error) {
			if req.SubtitlePath == "/wedged.srt" {
				select {
				case <-wedged:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &Response{Version: ProtocolVersion, Result: wireFromResult(&subsync.SyncResult{
				Method: subsync.MethodAudio, Offset: 500, Confidence: 0.9,
			})}, nil
		})
		log := activity.New(50)
		var outcomes []subflux.JobOutcome
		var mu sync.Mutex
		exec := func(ctx context.Context, in *syncjobs.ExecInput, hook func() bool) syncjobs.ExecResult {
			out := client.RunAudio(ctx, []byte(tinySRT), in.VideoPath, in.SubtitlePath, hook)
			res := syncjobs.ExecResult{Err: out.Err}
			switch out.Outcome {
			case OutcomeResult:
				res.Outcome = subflux.JobResult
				res.Applied = true
			case OutcomeTimeout:
				res.Outcome = subflux.JobTimeout
			case OutcomeCancelled:
				res.Outcome = subflux.JobCancelled
			default:
				res.Outcome = subflux.JobCrash
			}
			mu.Lock()
			outcomes = append(outcomes, res.Outcome)
			mu.Unlock()
			return res
		}
		ctx, cancel := context.WithCancel(t.Context())
		d := syncjobs.New(syncjobs.Deps{
			Exec:        exec,
			Log:         log,
			Stops:       &activity.StopRegistry{},
			PublishDone: func(*events.SyncDoneEvent) {},
		})
		done := make(chan struct{})
		go func() {
			defer close(done)
			d.Run(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		acc, err := d.DispatchBatch(batchOf("/wedged.srt", "/healthy.srt"))
		if err != nil {
			t.Fatalf("DispatchBatch() error = %v", err)
		}

		// The wedged worker holds the slot until its own budget fires —
		// the virtual clock covers the full ceiling — then item 2 runs.
		// Bounded: an hour of virtual time dwarfs the 15-minute budget.
		synctest.Wait()
		settledBoth := false
		for range 60 {
			jobs := d.Jobs(acc.ActivityID)
			settled := 0
			for _, j := range jobs {
				if j.State == syncjobs.StateDone {
					settled++
				}
			}
			if settled == 2 {
				settledBoth = true
				break
			}
			time.Sleep(time.Minute)
		}
		if !settledBoth {
			t.Fatalf("batch never settled both items past the budget; registry: %+v", d.Jobs(acc.ActivityID))
		}

		byOrdinal := map[int]syncjobs.Job{}
		for _, j := range d.Jobs(acc.ActivityID) {
			byOrdinal[j.Ordinal] = j
		}
		if byOrdinal[1].Outcome != subflux.JobTimeout {
			t.Errorf("wedged item outcome = %q, want timeout from its own budget", byOrdinal[1].Outcome)
		}
		if byOrdinal[2].Outcome != subflux.JobResult {
			t.Errorf("follow-up item outcome = %q, want the batch to proceed past the wedge", byOrdinal[2].Outcome)
		}
		mu.Lock()
		got := len(outcomes)
		mu.Unlock()
		if got != 2 {
			t.Errorf("executed items = %d, want both to have run", got)
		}
	})
}
