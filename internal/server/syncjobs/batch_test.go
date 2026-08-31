package syncjobs_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
)

// Season batch tests (D2): ONE dispatcher entry, ONE admission slot, ONE
// stop entry; every per-item fact in the registry; items sequential.

// batchInput builds a season batch over the given subtitle paths, ordinal
// order = slice order.
func batchInput(seriesID, season int, paths ...string) *syncjobs.BatchInput {
	in := &syncjobs.BatchInput{
		Detail:   fmt.Sprintf("Fixture S%02d · %d files", season, len(paths)),
		SeriesID: seriesID,
		Season:   season,
	}
	for i, p := range paths {
		in.Items = append(in.Items, syncjobs.ExecInput{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeEpisode,
				MediaID:   fmt.Sprintf("tvdb-81189-s%02de%02d", season, i+1),
				Language:  "en", Variant: "standard", Source: "external",
			},
			SubtitlePath: p,
			VideoPath:    fmt.Sprintf("/tv/e%d.mkv", i+1),
		})
	}
	return in
}

// awaitEntry polls the activity log until pred accepts the entry.
func awaitEntry(t *testing.T, log *activity.Log, id, want string, pred func(activity.Entry) bool) activity.Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e, ok := log.Get(id)
		if ok && pred(e) {
			return e
		}
		if time.Now().After(deadline) {
			t.Fatalf("activity %s never reached %s; entry: %+v", id, want, e)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestDispatchBatch_reload_before_any_event_lists_all_items_queued(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Park item 1 BEFORE its admission hook: nothing has flipped to running
	// and no sync:done exists — the reload window under test.
	gate := h.exec.gateHook("/e1.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt", "/e3.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	if acc.Existing || acc.ActivityID == "" || acc.SkippedLive != 0 {
		t.Fatalf("DispatchBatch() = %+v, want a fresh batch", acc)
	}

	jobs := h.d.Jobs(acc.ActivityID)
	if len(jobs) != 3 {
		t.Fatalf("Jobs(%q) = %d records, want the full item list at acceptance", acc.ActivityID, len(jobs))
	}
	ordinals := make([]int, 0, 3)
	for _, j := range jobs {
		if j.State != syncjobs.StateQueued {
			t.Errorf("job %d state = %q, want queued before any sync:done", j.JobID, j.State)
		}
		if j.BatchActivityID != acc.ActivityID || j.ActivityID != acc.ActivityID {
			t.Errorf("job %d activity ids = (%q, %q), want the batch's %q", j.JobID, j.ActivityID, j.BatchActivityID, acc.ActivityID)
		}
		if j.SeriesID != 7 || j.Season != 1 {
			t.Errorf("job %d scope = series %d season %d, want 7/1", j.JobID, j.SeriesID, j.Season)
		}
		ordinals = append(ordinals, j.Ordinal)
	}
	slices.Sort(ordinals)
	if !slices.Equal(ordinals, []int{1, 2, 3}) {
		t.Errorf("ordinals = %v, want 1..3 set at acceptance", ordinals)
	}

	// The aggregate entry is queued with Total set and no per-item shape.
	entry, ok := h.log.Get(acc.ActivityID)
	if !ok || !entry.Queued || entry.Total != 3 || entry.Current != 0 {
		t.Errorf("batch activity = %+v, want queued 0/3", entry)
	}

	close(gate)
	for _, j := range jobs {
		jobByID(t, h.d, j.JobID, syncjobs.StateDone)
	}
}

func TestDispatchBatch_two_items_retain_both_results_and_publish_own_events(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, release1 := h.exec.blockOn("/e1.srt")
	_, release2 := h.exec.blockOn("/e2.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	release1 <- syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true, OffsetMs: 100, Confidence: 0.9, Method: "audio"}
	release2 <- syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: false, OffsetMs: 200, Confidence: 0.2, Method: "audio"}

	ev1 := waitEvent(t, h)
	ev2 := waitEvent(t, h)
	if ev1.BatchActivityID != acc.ActivityID || ev2.BatchActivityID != acc.ActivityID {
		t.Errorf("event batch ids = %q/%q, want %q on every item event", ev1.BatchActivityID, ev2.BatchActivityID, acc.ActivityID)
	}
	if ev1.JobID == ev2.JobID {
		t.Errorf("both events carry job id %d, want one per item", ev1.JobID)
	}
	if !ev1.Applied || ev1.OffsetMs != 100 || ev2.Applied || ev2.OffsetMs != 200 {
		t.Errorf("events = %+v / %+v, want item 1 applied@100 then item 2 low-confidence@200", ev1, ev2)
	}

	// BOTH results retained in the registry, reconstructable by batch id.
	jobs := h.d.Jobs(acc.ActivityID)
	if len(jobs) != 2 {
		t.Fatalf("Jobs(batch) = %d records, want 2", len(jobs))
	}
	for _, j := range jobs {
		if j.State != syncjobs.StateDone || j.Outcome != subflux.JobResult {
			t.Errorf("job %d = %q/%q, want done(result)", j.JobID, j.State, j.Outcome)
		}
	}
	byOrdinal := map[int]syncjobs.Job{jobs[0].Ordinal: jobs[0], jobs[1].Ordinal: jobs[1]}
	if !byOrdinal[1].Applied || byOrdinal[1].OffsetMs != 100 {
		t.Errorf("item 1 = %+v, want applied@100 retained", byOrdinal[1])
	}
	if byOrdinal[2].Applied || byOrdinal[2].OffsetMs != 200 {
		t.Errorf("item 2 = %+v, want low-confidence@200 retained", byOrdinal[2])
	}

	// The batch aggregate ends clean: 2/2, terminal summary, done.
	entry := awaitEntry(t, h.log, acc.ActivityID, "done", func(e activity.Entry) bool { return e.Done })
	if entry.Failed || entry.Cancelled || entry.Current != 2 || entry.Total != 2 {
		t.Errorf("batch entry = %+v, want a clean 2/2 terminal", entry)
	}
	if entry.Detail != "Fixture S01 · 2 files · 1 synced, 1 low confidence" {
		t.Errorf("terminal detail = %q, want the aggregate summary", entry.Detail)
	}
}

func TestDispatchBatch_partial_failures_batch_completes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, release1 := h.exec.blockOn("/e1.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	release1 <- syncjobs.ExecResult{Outcome: subflux.JobCrash, Err: errors.New("ffmpeg exploded")}

	entry := awaitEntry(t, h.log, acc.ActivityID, "done", func(e activity.Entry) bool { return e.Done })
	if entry.Failed || entry.Cancelled {
		t.Errorf("batch entry = %+v, want completed despite the per-item failure", entry)
	}
	if entry.Detail != "Fixture S01 · 2 files · 1 synced, 1 failed" {
		t.Errorf("terminal detail = %q, want the failure counted in the aggregate", entry.Detail)
	}

	jobs := h.d.Jobs(acc.ActivityID)
	byOrdinal := map[int]syncjobs.Job{}
	for _, j := range jobs {
		byOrdinal[j.Ordinal] = j
	}
	if byOrdinal[1].Outcome != subflux.JobCrash || byOrdinal[1].Error == "" {
		t.Errorf("item 1 = %+v, want its crash retained", byOrdinal[1])
	}
	if byOrdinal[2].Outcome != subflux.JobResult {
		t.Errorf("item 2 = %+v, want the sibling's clean result", byOrdinal[2])
	}
}

func TestDispatchBatch_items_run_sequentially(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started1, release1 := h.exec.blockOn("/e1.srt")
	started2, release2 := h.exec.blockOn("/e2.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	<-started1
	// Item 2 must not start while item 1 runs.
	select {
	case <-started2:
		t.Fatal("item 2 started while item 1 was running; want sequential submission")
	case <-time.After(50 * time.Millisecond):
	}
	release1 <- syncjobs.ExecResult{Outcome: subflux.JobResult}
	<-started2
	release2 <- syncjobs.ExecResult{Outcome: subflux.JobResult}

	if got := h.exec.execOrder(); !slices.Equal(got, []string{"/e1.srt", "/e2.srt"}) {
		t.Errorf("exec order = %v, want ordinal order", got)
	}
	entry := awaitEntry(t, h.log, acc.ActivityID, "done", func(e activity.Entry) bool { return e.Done })
	if entry.Current != 2 || entry.Total != 2 {
		t.Errorf("aggregate = %d/%d, want 2/2", entry.Current, entry.Total)
	}
}

func TestDispatchBatch_stop_mid_item_cancels_via_the_single_stop_entry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started1, _ := h.exec.blockOn("/e1.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt", "/e3.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	<-started1

	// MID-ITEM: the batch's stop entry exists (registered at batch start),
	// so the stop request lands — no 409 window.
	if got := h.stops.RequestStop(acc.ActivityID); got != activity.StopRequested {
		t.Fatalf("RequestStop(mid-item) = %v, want StopRequested via the batch's stop entry", got)
	}

	entry := awaitEntry(t, h.log, acc.ActivityID, "cancelled", func(e activity.Entry) bool { return e.Done && e.Cancelled })
	if !entry.Cancelled {
		t.Fatalf("batch entry = %+v, want terminal cancelled", entry)
	}

	// The in-flight item ran and settles cancelled (its worker exited);
	// queued items settle cancelled WITHOUT ever executing.
	jobs := h.d.Jobs(acc.ActivityID)
	byOrdinal := map[int]syncjobs.Job{}
	for _, j := range jobs {
		if j.State != syncjobs.StateDone || j.Outcome != subflux.JobCancelled {
			t.Errorf("job %d = %q/%q, want done(cancelled)", j.JobID, j.State, j.Outcome)
		}
		byOrdinal[j.Ordinal] = j
	}
	if byOrdinal[1].StartedAt == nil {
		t.Errorf("item 1 = %+v, want it to have run before the stop", byOrdinal[1])
	}
	if byOrdinal[2].StartedAt != nil || byOrdinal[3].StartedAt != nil {
		t.Errorf("queued items started (%+v, %+v), want settle-never-execute", byOrdinal[2], byOrdinal[3])
	}
	if got := h.exec.execOrder(); !slices.Equal(got, []string{"/e1.srt"}) {
		t.Errorf("exec order = %v, want only item 1 to have run", got)
	}

	// The mid-item cancellation publishes ITS event (the worker ran);
	// never-admitted items publish nothing.
	ev := waitEvent(t, h)
	if ev.JobID != byOrdinal[1].JobID || ev.Error == "" {
		t.Errorf("sync:done = %+v, want item 1's cancelled terminal", ev)
	}
	if ev.Outcome != subflux.JobCancelled {
		t.Errorf("sync:done outcome = %q, want %q (the batch's publish carries the item's verdict too)",
			ev.Outcome, subflux.JobCancelled)
	}
	select {
	case extra := <-h.events:
		t.Errorf("unexpected extra event %+v for a never-admitted item", extra)
	default:
	}

	// Capacity released: a fresh dispatch enters.
	if _, err := h.d.Dispatch(input("/after-stop.srt")); err != nil {
		t.Errorf("post-stop dispatch error = %v, want a free slot", err)
	}
}

func TestDispatchBatch_stop_between_items_no_409_window(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Item 2 parks BEFORE its admission hook: the batch sits BETWEEN item
	// 1's terminal and item 2's admission.
	gate2 := h.exec.gateHook("/e2.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	// Wait for item 1 to fully settle (its event proves the worker exited).
	ev := waitEvent(t, h)
	if ev.Error != "" {
		t.Fatalf("item 1 event = %+v, want a clean result", ev)
	}

	// BETWEEN ITEMS: the single stop entry registered at batch start is
	// still live — the stop lands, never a 409.
	if got := h.stops.RequestStop(acc.ActivityID); got != activity.StopRequested {
		t.Fatalf("RequestStop(between items) = %v, want StopRequested", got)
	}
	close(gate2) // item 2 reaches its hook, which refuses post-cancel

	entry := awaitEntry(t, h.log, acc.ActivityID, "cancelled", func(e activity.Entry) bool { return e.Done && e.Cancelled })
	if !entry.Cancelled {
		t.Fatalf("batch entry = %+v, want terminal cancelled", entry)
	}
	jobs := h.d.Jobs(acc.ActivityID)
	byOrdinal := map[int]syncjobs.Job{}
	for _, j := range jobs {
		byOrdinal[j.Ordinal] = j
	}
	if byOrdinal[1].Outcome != subflux.JobResult {
		t.Errorf("item 1 = %+v, want its completed result RETAINED through the stop", byOrdinal[1])
	}
	if byOrdinal[2].Outcome != subflux.JobCancelled || byOrdinal[2].StartedAt != nil {
		t.Errorf("item 2 = %+v, want cancelled never admitted", byOrdinal[2])
	}
}

func TestDispatchBatch_queued_delete_settles_everything_and_releases_capacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, release := h.exec.blockOn("/hold.srt")
	if _, err := h.d.Dispatch(input("/hold.srt")); err != nil {
		t.Fatal(err)
	}
	<-started

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}

	if got := h.d.Cancel(acc.ActivityID); got != syncjobs.CancelledQueued {
		t.Fatalf("Cancel(queued batch) = %v, want CancelledQueued", got)
	}
	for _, j := range h.d.Jobs(acc.ActivityID) {
		if j.State != syncjobs.StateDone || j.Outcome != subflux.JobCancelled || j.StartedAt != nil {
			t.Errorf("item %d = %+v, want done(cancelled) never executed", j.Ordinal, j)
		}
	}
	entry, _ := h.log.Get(acc.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("batch entry = %+v, want terminal cancelled", entry)
	}
	// Slot released IMMEDIATELY; no item event was ever published.
	if _, err := h.d.Dispatch(input("/next.srt")); err != nil {
		t.Errorf("post-cancel dispatch error = %v, want a free slot", err)
	}
	select {
	case ev := <-h.events:
		if ev.BatchActivityID == acc.ActivityID {
			t.Errorf("queued-cancel published %+v, want none", ev)
		}
	default:
	}
	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
}

func TestDispatchBatch_same_scope_answers_existing_id(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, release := h.exec.blockOn("/e1.srt")

	first, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	<-started

	second, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt"))
	if err != nil {
		t.Fatalf("same-scope DispatchBatch() error = %v", err)
	}
	if !second.Existing || second.ActivityID != first.ActivityID {
		t.Errorf("same-scope dispatch = %+v, want the live batch's id %q", second, first.ActivityID)
	}
	// A DIFFERENT season is a fresh batch, not a scope match.
	third, err := h.d.DispatchBatch(batchInput(7, 2, "/other.srt"))
	if err != nil {
		t.Fatalf("other-scope DispatchBatch() error = %v", err)
	}
	if third.Existing || third.ActivityID == first.ActivityID {
		t.Errorf("other-scope dispatch = %+v, want a fresh batch", third)
	}
	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
}

func TestDispatchBatch_takes_one_admission_slot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/e1.srt")
	if _, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt", "/e3.srt")); err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	<-started

	// The 3-item batch consumed ONE slot: MaxJobs-1 singles still enter.
	for i := 1; i < syncjobs.MaxJobs; i++ {
		if _, err := h.d.Dispatch(input(fmt.Sprintf("/s%d.srt", i))); err != nil {
			t.Fatalf("dispatch %d error = %v, want the batch to hold one slot", i, err)
		}
	}
	if _, err := h.d.Dispatch(input("/overflow.srt")); !errors.Is(err, syncjobs.ErrCapacity) {
		t.Errorf("overflow dispatch error = %v, want ErrCapacity", err)
	}
	if _, err := h.d.DispatchBatch(batchInput(9, 1, "/b2.srt")); !errors.Is(err, syncjobs.ErrCapacity) {
		t.Errorf("overflow batch error = %v, want ErrCapacity", err)
	}
}

func TestDispatchBatch_same_file_single_dispatch_dedupes_to_the_item(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, release := h.exec.blockOn("/e1.srt")
	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	<-started

	single, err := h.d.Dispatch(input("/e1.srt"))
	if err != nil {
		t.Fatalf("same-file single dispatch error = %v", err)
	}
	if !single.Existing || single.ActivityID != acc.ActivityID {
		t.Errorf("same-file single = %+v, want the batch item's ids (batch %q)", single, acc.ActivityID)
	}
	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
}

func TestDispatchBatch_live_single_skips_the_overlapping_item(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, release := h.exec.blockOn("/e1.srt")
	if _, err := h.d.Dispatch(input("/e1.srt")); err != nil {
		t.Fatal(err)
	}
	<-started

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	if acc.SkippedLive != 1 {
		t.Errorf("SkippedLive = %d, want the in-flight single's file skipped", acc.SkippedLive)
	}
	jobs := h.d.Jobs(acc.ActivityID)
	if len(jobs) != 1 || jobs[0].Ordinal != 1 {
		t.Errorf("Jobs(batch) = %+v, want only the free file as ordinal 1", jobs)
	}
	release <- syncjobs.ExecResult{Outcome: subflux.JobResult}
	jobByID(t, h.d, jobs[0].JobID, syncjobs.StateDone)
}

func TestDispatchBatch_shutdown_settles_queued_batch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	started, _ := h.exec.blockOn("/hold.srt")
	if _, err := h.d.Dispatch(input("/hold.srt")); err != nil {
		t.Fatal(err)
	}
	<-started
	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}

	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after lifetime cancellation")
	}

	for _, j := range h.d.Jobs(acc.ActivityID) {
		if j.State != syncjobs.StateDone || j.Outcome != subflux.JobCancelled || j.StartedAt != nil {
			t.Errorf("item %d = %+v, want settled cancelled never executed at shutdown", j.Ordinal, j)
		}
	}
	entry, _ := h.log.Get(acc.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("batch entry = %+v, want terminal cancelled at shutdown", entry)
	}
	if _, err := h.d.DispatchBatch(batchInput(9, 1, "/late.srt")); !errors.Is(err, syncjobs.ErrShuttingDown) {
		t.Errorf("post-drain batch error = %v, want ErrShuttingDown", err)
	}
}

func TestDispatchBatch_aggregate_progress_advances_per_item(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, release1 := h.exec.blockOn("/e1.srt")
	started2, release2 := h.exec.blockOn("/e2.srt")

	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	release1 <- syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true}
	<-started2

	// After item 1 settles the aggregate reads 1/2 — no per-item shape,
	// just Current/Total.
	entry := awaitEntry(t, h.log, acc.ActivityID, "1/2", func(e activity.Entry) bool { return e.Current == 1 })
	if entry.Total != 2 || entry.Done {
		t.Errorf("mid-batch aggregate = %+v, want a live 1/2", entry)
	}
	release2 <- syncjobs.ExecResult{Outcome: subflux.JobResult, Applied: true}
	entry = awaitEntry(t, h.log, acc.ActivityID, "done", func(e activity.Entry) bool { return e.Done })
	if entry.Current != 2 || entry.Total != 2 {
		t.Errorf("terminal aggregate = %d/%d, want 2/2", entry.Current, entry.Total)
	}
}

func TestDispatchBatch_completed_batch_releases_exactly_one_slot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	acc, err := h.d.DispatchBatch(batchInput(7, 1, "/e1.srt", "/e2.srt", "/e3.srt"))
	if err != nil {
		t.Fatalf("DispatchBatch() error = %v", err)
	}
	awaitEntry(t, h.log, acc.ActivityID, "done", func(e activity.Entry) bool { return e.Done })

	// The lease is whole again — and not OVER-released by the three item
	// settlements: exactly MaxJobs slots, not more.
	started, _ := h.exec.blockOn("/hold0.srt")
	if _, err := h.d.Dispatch(input("/hold0.srt")); err != nil {
		t.Fatal(err)
	}
	<-started
	for i := 1; i < syncjobs.MaxJobs; i++ {
		if _, err := h.d.Dispatch(input(fmt.Sprintf("/post%d.srt", i))); err != nil {
			t.Fatalf("dispatch %d error = %v, want a whole lease after the batch", i, err)
		}
	}
	if _, err := h.d.Dispatch(input("/post-overflow.srt")); !errors.Is(err, syncjobs.ErrCapacity) {
		t.Errorf("overflow error = %v, want ErrCapacity — the item settlements must not each release a slot", err)
	}
}
