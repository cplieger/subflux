package syncjobs_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeExec scripts job executions. A job whose subtitle path has a release
// channel blocks (after its hook) until released or its context dies; any
// other job settles immediately with a canned confident result.
type fakeExec struct {
	preHook map[string]chan struct{}
	started map[string]chan struct{}
	release map[string]chan syncjobs.ExecResult
	order   []string
	mu      sync.Mutex
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		preHook: map[string]chan struct{}{},
		started: map[string]chan struct{}{},
		release: map[string]chan syncjobs.ExecResult{},
	}
}

// blockOn makes the job for path block until its release channel answers;
// started closes once the job is past its hook.
func (f *fakeExec) blockOn(path string) (started chan struct{}, release chan syncjobs.ExecResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	started = make(chan struct{})
	release = make(chan syncjobs.ExecResult, 1)
	f.started[path] = started
	f.release[path] = release
	return started, release
}

// gateHook parks the job for path BEFORE it invokes its admission hook,
// opening the popped-but-not-admitted race window.
func (f *fakeExec) gateHook(path string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	gate := make(chan struct{})
	f.preHook[path] = gate
	return gate
}

func (f *fakeExec) exec(ctx context.Context, in *syncjobs.ExecInput, hook func() bool) syncjobs.ExecResult {
	f.mu.Lock()
	gate := f.preHook[in.SubtitlePath]
	f.mu.Unlock()
	if gate != nil {
		// ctx arm so a test failing before it releases the gate cannot
		// wedge the harness cleanup; the hook below observes the cancel.
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	if !hook() {
		return syncjobs.ExecResult{Outcome: subflux.JobCancelled, Err: context.Canceled}
	}
	f.mu.Lock()
	f.order = append(f.order, in.SubtitlePath)
	started := f.started[in.SubtitlePath]
	// One close per registration: a post-terminal re-dispatch of the same
	// path (the dedupe test's third job) must not re-close started.
	delete(f.started, in.SubtitlePath)
	release := f.release[in.SubtitlePath]
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release == nil {
		return syncjobs.ExecResult{
			Outcome: subflux.JobResult, Applied: true,
			OffsetMs: 420, Confidence: 0.9, Method: "audio",
		}
	}
	select {
	case r := <-release:
		return r
	case <-ctx.Done():
		return syncjobs.ExecResult{Outcome: subflux.JobCancelled, Err: context.Cause(ctx)}
	}
}

// execOrder snapshots the order jobs actually RAN in.
func (f *fakeExec) execOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.order)
}

type harness struct {
	d      *syncjobs.Dispatcher
	log    *activity.Log
	stops  *activity.StopRegistry
	exec   *fakeExec
	events chan *events.SyncDoneEvent
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// The dispatcher's lifetime must outlive t.Context (shutdown tests cancel
	// it themselves; cleanup cancels it last and waits for the loop).
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{
		log:    activity.New(50),
		stops:  &activity.StopRegistry{},
		exec:   newFakeExec(),
		events: make(chan *events.SyncDoneEvent, 32),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	h.d = syncjobs.New(syncjobs.Deps{
		Exec:        h.exec.exec,
		Log:         h.log,
		Stops:       h.stops,
		PublishDone: func(ev *events.SyncDoneEvent) { h.events <- ev },
	})
	go func() {
		defer close(h.done)
		h.d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-h.done
	})
	return h
}

func input(path string) *syncjobs.ExecInput {
	return &syncjobs.ExecInput{
		Ref: resolve.FileRef{
			MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1",
			Language: "en", Variant: "standard", Source: "external",
		},
		SubtitlePath: path,
		VideoPath:    "/media/movie.mkv",
	}
}

// jobByID polls the registry until the job reaches the wanted state.
func jobByID(t *testing.T, d *syncjobs.Dispatcher, id int64, state syncjobs.JobState) syncjobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, j := range d.Jobs("") {
			if j.JobID == id && j.State == state {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d never reached state %q; registry: %+v", id, state, d.Jobs(""))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitEvent(t *testing.T, h *harness) *events.SyncDoneEvent {
	t.Helper()
	select {
	case ev := <-h.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no sync:done event arrived")
		return nil
	}
}

func TestDispatch_runs_a_job_to_done_result(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	acc, err := h.d.Dispatch(input("/media/movie.en.srt"))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if acc.Existing || acc.JobID == 0 || acc.ActivityID == "" {
		t.Fatalf("Dispatch() = %+v, want fresh ids", acc)
	}

	job := jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
	if job.Outcome != subflux.JobResult || !job.Applied || job.OffsetMs != 420 {
		t.Errorf("settled job = %+v, want applied result with the executor's cumulative offset", job)
	}
	if job.StartedAt == nil || job.EndedAt == nil {
		t.Errorf("job timestamps = %+v/%+v, want both set", job.StartedAt, job.EndedAt)
	}

	ev := waitEvent(t, h)
	if ev.JobID != acc.JobID || !ev.Applied || ev.OffsetMs != 420 || ev.Confidence != 0.9 {
		t.Errorf("sync:done = %+v, want the job's result fields", ev)
	}

	entry, ok := h.log.Get(acc.ActivityID)
	if !ok || !entry.Done || entry.Failed || entry.Cancelled {
		t.Errorf("activity entry = %+v, want a clean terminal", entry)
	}
}

// TestPublishDone_carries_each_terminal_outcome pins the event's typed
// verdict per terminal: the dialog renders from sync:done alone, and Error is
// set for every non-result outcome, so without Outcome a stopped job and a
// crashed one are the same frame.
func TestPublishDone_carries_each_terminal_outcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		res     syncjobs.ExecResult
		want    subflux.JobOutcome
		errored bool
	}{
		{
			name: "result",
			res:  syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true, OffsetMs: 120, Confidence: 0.8},
			want: subflux.JobResult,
		},
		{
			name:    "timeout",
			res:     syncjobs.ExecResult{Outcome: subflux.JobTimeout, Err: errors.New("analysis budget exceeded")},
			want:    subflux.JobTimeout,
			errored: true,
		},
		{
			name:    "cancelled",
			res:     syncjobs.ExecResult{Outcome: subflux.JobCancelled, Err: context.Canceled},
			want:    subflux.JobCancelled,
			errored: true,
		},
		{
			name:    "crash",
			res:     syncjobs.ExecResult{Outcome: subflux.JobCrash, Err: errors.New("ffmpeg exploded")},
			want:    subflux.JobCrash,
			errored: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			started, release := h.exec.blockOn("/a.srt")
			acc, err := h.d.Dispatch(input("/a.srt"))
			if err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}
			<-started
			release <- tc.res

			ev := waitEvent(t, h)
			if ev.JobID != acc.JobID {
				t.Fatalf("sync:done job_id = %d, want the dispatched job %d", ev.JobID, acc.JobID)
			}
			if ev.Outcome != tc.want {
				t.Errorf("sync:done(%s) outcome = %q, want %q", tc.name, ev.Outcome, tc.want)
			}
			if (ev.Error != "") != tc.errored {
				t.Errorf("sync:done(%s) error = %q, want errored = %v (Error alone cannot discriminate the three failures)",
					tc.name, ev.Error, tc.errored)
			}
			// The event carries the RECORD's verdict, not a re-derivation.
			job := jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
			if job.Outcome != ev.Outcome {
				t.Errorf("registry outcome = %q, sync:done outcome = %q, want the same verdict", job.Outcome, ev.Outcome)
			}
		})
	}
}

func TestDispatch_fifo_one_top_level_entry_at_a_time(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	startedA, releaseA := h.exec.blockOn("/a.srt")

	accA, _ := h.d.Dispatch(input("/a.srt"))
	<-startedA
	accB, _ := h.d.Dispatch(input("/b.srt"))
	accC, _ := h.d.Dispatch(input("/c.srt"))

	// While A runs, B and C are QUEUED — visible in the jobs list.
	jobByID(t, h.d, accB.JobID, syncjobs.StateQueued)
	jobByID(t, h.d, accC.JobID, syncjobs.StateQueued)
	if got := h.exec.execOrder(); len(got) != 1 {
		t.Fatalf("exec order while A runs = %v, want only A", got)
	}

	releaseA <- syncjobs.ExecResult{Outcome: subflux.JobResult}
	jobByID(t, h.d, accC.JobID, syncjobs.StateDone)

	want := []string{"/a.srt", "/b.srt", "/c.srt"}
	if got := h.exec.execOrder(); !slices.Equal(got, want) {
		t.Errorf("exec order = %v, want FIFO %v", got, want)
	}
	jobByID(t, h.d, accA.JobID, syncjobs.StateDone)
}

func TestDispatch_same_file_dedupe_answers_existing_ids(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, release := h.exec.blockOn("/a.srt")

	first, _ := h.d.Dispatch(input("/a.srt"))
	<-started
	second, err := h.d.Dispatch(input("/a.srt"))
	if err != nil {
		t.Fatalf("dedupe dispatch error = %v", err)
	}
	if !second.Existing || second.JobID != first.JobID || second.ActivityID != first.ActivityID {
		t.Errorf("same-file dispatch = %+v, want the existing job's ids %+v", second, first)
	}

	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
	jobByID(t, h.d, first.JobID, syncjobs.StateDone)

	// Terminal: the dedupe entry released, a re-dispatch is a NEW job.
	third, _ := h.d.Dispatch(input("/a.srt"))
	if third.Existing || third.JobID == first.JobID {
		t.Errorf("post-terminal dispatch = %+v, want a fresh job", third)
	}
}

func TestDispatch_capacity_overflow_is_typed_and_dedupe_still_answers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/hold0.srt")
	first, _ := h.d.Dispatch(input("/hold0.srt"))
	<-started
	for i := 1; i < syncjobs.MaxJobs; i++ {
		if _, err := h.d.Dispatch(input(fmt.Sprintf("/hold%d.srt", i))); err != nil {
			t.Fatalf("dispatch %d error = %v", i, err)
		}
	}

	// The lease is full: a DISTINCT file answers the typed capacity refusal.
	if _, err := h.d.Dispatch(input("/overflow.srt")); !errors.Is(err, syncjobs.ErrCapacity) {
		t.Fatalf("dispatch over capacity error = %v, want ErrCapacity", err)
	}

	// Same-file dedupe answers 202 with the EXISTING ids even at cap.
	again, err := h.d.Dispatch(input("/hold0.srt"))
	if err != nil || !again.Existing || again.JobID != first.JobID {
		t.Errorf("same-file at cap = (%+v, %v), want the existing ids", again, err)
	}
}

func TestCancel_queued_releases_capacity_immediately(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/hold0.srt")
	if _, err := h.d.Dispatch(input("/hold0.srt")); err != nil {
		t.Fatal(err)
	}
	<-started
	var queued []syncjobs.Accepted
	for i := 1; i < syncjobs.MaxJobs; i++ {
		acc, err := h.d.Dispatch(input(fmt.Sprintf("/hold%d.srt", i)))
		if err != nil {
			t.Fatal(err)
		}
		queued = append(queued, acc)
	}
	if _, err := h.d.Dispatch(input("/overflow.srt")); !errors.Is(err, syncjobs.ErrCapacity) {
		t.Fatalf("pre-cancel dispatch error = %v, want ErrCapacity", err)
	}

	victim := queued[len(queued)-1]
	if got := h.d.Cancel(victim.ActivityID); got != syncjobs.CancelledQueued {
		t.Fatalf("Cancel(queued) = %v, want CancelledQueued", got)
	}

	// The slot released IMMEDIATELY: the next dispatch enters.
	acc, err := h.d.Dispatch(input("/next.srt"))
	if err != nil {
		t.Fatalf("post-cancel dispatch error = %v, want admission", err)
	}
	if acc.Existing {
		t.Errorf("post-cancel dispatch = %+v, want a fresh job", acc)
	}

	// queued → done(cancelled) is RETAINED and reload-visible; never executed.
	job := jobByID(t, h.d, victim.JobID, syncjobs.StateDone)
	if job.Outcome != subflux.JobCancelled || job.StartedAt != nil {
		t.Errorf("cancelled queued job = %+v, want done(cancelled) never started", job)
	}
	if slices.Contains(h.exec.execOrder(), fmt.Sprintf("/hold%d.srt", len(queued))) {
		t.Errorf("cancelled queued job executed; want settle-never-execute")
	}
	entry, _ := h.log.Get(victim.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("activity entry = %+v, want terminal cancelled", entry)
	}
	// No success event for a never-admitted cancellation.
	select {
	case ev := <-h.events:
		if ev.JobID == victim.JobID {
			t.Errorf("queued-cancel published sync:done %+v, want none", ev)
		}
	default:
	}
}

func TestCancel_running_converts_and_settles_on_worker_exit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/a.srt")
	acc, _ := h.d.Dispatch(input("/a.srt"))
	<-started

	if got := h.d.Cancel(acc.ActivityID); got != syncjobs.CancelConverted {
		t.Fatalf("Cancel(running) = %v, want CancelConverted", got)
	}

	// The stop entry's context cancel unblocks the exec; the job settles
	// done(cancelled) on worker exit and the terminal event still publishes.
	job := jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
	if job.Outcome != subflux.JobCancelled || job.StartedAt == nil {
		t.Errorf("converted job = %+v, want done(cancelled) after running", job)
	}
	ev := waitEvent(t, h)
	if ev.JobID != acc.JobID || ev.Applied || ev.Error == "" {
		t.Errorf("sync:done = %+v, want the cancelled job's terminal", ev)
	}
	// The conversion is the only path that settles a job the client still
	// thinks is queued, and its event must SAY cancelled: the error string it
	// also carries is what a crash carries too.
	if ev.Outcome != subflux.JobCancelled {
		t.Errorf("sync:done outcome = %q, want %q", ev.Outcome, subflux.JobCancelled)
	}
	entry, _ := h.log.Get(acc.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("activity entry = %+v, want terminal cancelled", entry)
	}
}

func TestCancel_wins_the_admission_race_and_the_worker_never_spawns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	gate := h.exec.gateHook("/a.srt")

	acc, _ := h.d.Dispatch(input("/a.srt"))
	// The job is popped and parked BEFORE its admission hook; the cancel
	// lands in exactly the race window the hook exists to close.
	deadline := time.Now().Add(5 * time.Second)
	for h.d.Cancel(acc.ActivityID) == syncjobs.CancelUnknown {
		if time.Now().After(deadline) {
			t.Fatal("job never became cancellable")
		}
	}
	close(gate)

	job := jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
	if job.Outcome != subflux.JobCancelled {
		t.Fatalf("job outcome = %q, want cancelled", job.Outcome)
	}
	if job.StartedAt != nil {
		t.Errorf("job started despite the refused admission (stop entry registered? running published?)")
	}
	if got := h.exec.execOrder(); len(got) != 0 {
		t.Errorf("exec order = %v, want the worker never to have run", got)
	}
}

func TestCancel_terminal_is_dismiss_only(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	acc, _ := h.d.Dispatch(input("/a.srt"))
	jobByID(t, h.d, acc.JobID, syncjobs.StateDone)

	if got := h.d.Cancel(acc.ActivityID); got != syncjobs.CancelTerminal {
		t.Fatalf("Cancel(terminal) = %v, want CancelTerminal", got)
	}
	// The registry is never touched by a terminal row's dismissal.
	if got := len(h.d.Jobs("")); got != 1 {
		t.Errorf("registry size after terminal cancel = %d, want 1", got)
	}
}

func TestCancel_unknown_activity_id_falls_through(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if got := h.d.Cancel("act-nope"); got != syncjobs.CancelUnknown {
		t.Errorf("Cancel(unknown) = %v, want CancelUnknown", got)
	}
}

// TestStop_registered_before_running_publishes pins the admission-hook
// ordering: at the instant the running upsert publishes, the stop entry
// already exists — a stop request between running-publish and registration
// is unrepresentable.
func TestStop_registered_before_running_publishes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	type observation struct {
		id          string
		cancellable bool
	}
	seen := make(chan observation, 8)
	h.log.SetOnUpsert(func(e activity.Entry) {
		if !e.Queued && !e.Done {
			seen <- observation{id: e.ID, cancellable: h.stops.Cancellable(e.ID)}
		}
	})

	started, release := h.exec.blockOn("/a.srt")
	acc, _ := h.d.Dispatch(input("/a.srt"))
	<-started

	select {
	case obs := <-seen:
		if obs.id != acc.ActivityID || !obs.cancellable {
			t.Errorf("running publish observed stop registration = %+v, want cancellable at publish time", obs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no running upsert observed")
	}
	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
	jobByID(t, h.d, acc.JobID, syncjobs.StateDone)

	// Terminal unregisters before terminal-publish: the entry is no longer
	// cancellable once done.
	if h.stops.Cancellable(acc.ActivityID) {
		t.Error("stop registration survived the terminal transition")
	}
}

func TestShutdown_settles_queued_and_waits_for_the_admitted_worker(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/running.srt")
	running, _ := h.d.Dispatch(input("/running.srt"))
	<-started
	q1, _ := h.d.Dispatch(input("/q1.srt"))
	q2, _ := h.d.Dispatch(input("/q2.srt"))

	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after lifetime cancellation")
	}

	// The admitted worker was WAITED for (its ctx died, the exec returned
	// cancelled); queued jobs settled cancelled without ever executing.
	for _, acc := range []syncjobs.Accepted{running, q1, q2} {
		job := jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
		if job.Outcome != subflux.JobCancelled {
			t.Errorf("job %d outcome = %q, want cancelled at shutdown", acc.JobID, job.Outcome)
		}
	}
	if got := h.exec.execOrder(); !slices.Equal(got, []string{"/running.srt"}) {
		t.Errorf("exec order = %v, want only the admitted job to have run", got)
	}

	// The drained dispatcher refuses new work.
	if _, err := h.d.Dispatch(input("/late.srt")); !errors.Is(err, syncjobs.ErrShuttingDown) {
		t.Errorf("post-drain dispatch error = %v, want ErrShuttingDown", err)
	}
}

func TestJobs_total_order_is_numeric(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var last syncjobs.Accepted
	for i := range 12 {
		acc, err := h.d.Dispatch(input(fmt.Sprintf("/m%d.srt", i)))
		if err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
		jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
		last = acc
	}
	jobs := h.d.Jobs("")
	if len(jobs) != 12 {
		t.Fatalf("registry size = %d, want 12", len(jobs))
	}
	// accepted_at DESC, job_id DESC — and NUMERIC: job 12 sorts before job 9
	// (a lexicographic order would put "9" first).
	if jobs[0].JobID != last.JobID {
		t.Errorf("jobs[0].JobID = %d, want the newest %d", jobs[0].JobID, last.JobID)
	}
	for i := 1; i < len(jobs); i++ {
		if jobs[i].JobID >= jobs[i-1].JobID {
			t.Fatalf("total order violated at %d: %d then %d", i, jobs[i-1].JobID, jobs[i].JobID)
		}
	}
}

func TestJobs_filters_by_batch_activity_id(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	acc, _ := h.d.Dispatch(input("/a.srt"))
	jobByID(t, h.d, acc.JobID, syncjobs.StateDone)
	// The field exists now; the batch itself arrives with the season
	// endpoint, so a filter matches nothing yet.
	if got := h.d.Jobs("batch-1"); len(got) != 0 {
		t.Errorf("Jobs(batch-1) = %v, want empty (no batch jobs exist)", got)
	}
	if got := h.d.Jobs(""); len(got) != 1 {
		t.Errorf("Jobs(\"\") = %v, want the one job", got)
	}
}

// TestRetention_and_cap runs the dispatcher under a virtual clock: retention
// prunes done records past DefaultPruneAge, queued/running records are never
// evicted, and the registry cap evicts only past-retention records.
func TestRetention_and_cap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		exec := newFakeExec()
		log := activity.New(50)
		d := syncjobs.New(syncjobs.Deps{
			Exec:        exec.exec,
			Log:         log,
			Stops:       &activity.StopRegistry{},
			PublishDone: func(*events.SyncDoneEvent) {},
			RegistryCap: 2,
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

		settle := func(path string) syncjobs.Accepted {
			acc, err := d.Dispatch(input(path))
			if err != nil {
				t.Fatalf("dispatch %s: %v", path, err)
			}
			synctest.Wait() // the run loop settles the job before time moves
			return acc
		}

		// Three young done records over a cap of 2: the cap may NOT evict
		// them — they are inside the retention window.
		settle("/one.srt")
		settle("/two.srt")
		settle("/three.srt")
		if got := len(d.Jobs("")); got != 3 {
			t.Fatalf("registry size = %d, want 3 (cap evicts only past-retention)", got)
		}

		// Age them past retention; the next insert's cap pass evicts down to
		// the cap, oldest first, and the prune tick removes the rest.
		time.Sleep(activity.DefaultPruneAge + time.Minute)
		holdStarted, release := exec.blockOn("/running.srt")
		accRunning, _ := d.Dispatch(input("/running.srt"))
		<-holdStarted
		jobs := d.Jobs("")
		if got := len(jobs); got != 2 {
			t.Fatalf("registry size after cap eviction = %d, want 2 (cap + the running job)", got)
		}

		// Prune removes the remaining past-retention done record; the
		// RUNNING record is never evicted, however old.
		time.Sleep(activity.DefaultPruneAge + time.Minute)
		d.Prune(activity.DefaultPruneAge)
		jobs = d.Jobs("")
		if len(jobs) != 1 || jobs[0].JobID != accRunning.JobID || jobs[0].State != syncjobs.StateRunning {
			t.Fatalf("registry after prune = %+v, want only the running job", jobs)
		}

		release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
		synctest.Wait()
	})
}

// TestRetention_never_evicts_a_live_batchs_done_items pins the prune/batch
// seam: an early-done item of a still-running batch crosses retention while
// a sibling item wedges, and the server's prune tick fires mid-batch. The
// done record must survive — the batch finalizers dereference every item id
// and the batch_activity_id read lists the full set — and the batch must
// finalize with complete counts. Once the batch is terminal, its aged items
// go on the next tick.
func TestRetention_never_evicts_a_live_batchs_done_items(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		exec := newFakeExec()
		log := activity.New(50)
		d := syncjobs.New(syncjobs.Deps{
			Exec:        exec.exec,
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

		// Item 1 settles immediately with the canned applied result; item 2
		// wedges until released.
		wedgedStarted, release := exec.blockOn("/e2.srt")
		acc, err := d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
		if err != nil {
			t.Fatalf("DispatchBatch() error = %v", err)
		}
		<-wedgedStarted

		// The wedged item consumes more than the retention window; the prune
		// tick fires mid-batch. Both item records must survive it.
		time.Sleep(activity.DefaultPruneAge + time.Minute)
		d.Prune(activity.DefaultPruneAge)
		jobs := d.Jobs(acc.ActivityID)
		if len(jobs) != 2 {
			t.Fatalf("Jobs(%q) after a mid-batch prune = %d records, want the full item set: %+v",
				acc.ActivityID, len(jobs), jobs)
		}
		doneCount := 0
		for _, j := range jobs {
			if j.State == syncjobs.StateDone {
				doneCount++
			}
		}
		if doneCount != 1 {
			t.Fatalf("done items after prune = %d, want the early item retained as done: %+v", doneCount, jobs)
		}

		// Releasing the wedged item finalizes the batch: no panic, and the
		// aggregate counts fold BOTH items (a pruned id would miscount).
		release <- syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true}
		synctest.Wait()
		entry, ok := log.Get(acc.ActivityID)
		if !ok || !entry.Done || entry.Failed || entry.Cancelled {
			t.Fatalf("batch activity after finalize = %+v, want a clean terminal", entry)
		}
		if entry.Detail != "Fixture S01 · 2 files · 2 synced" {
			t.Errorf("terminal detail = %q, want the full 2-item aggregate", entry.Detail)
		}

		// Terminal batch: the aged early item now goes; the fresh one stays.
		d.Prune(activity.DefaultPruneAge)
		jobs = d.Jobs(acc.ActivityID)
		if len(jobs) != 1 || jobs[0].Ordinal != 2 {
			t.Errorf("Jobs(%q) after the post-terminal prune = %+v, want only the fresh item", acc.ActivityID, jobs)
		}
	})
}
