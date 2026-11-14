package syncsched_test

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

func TestResyncAppliesOnlyVerified(t *testing.T) {
	video := episode(40, 260)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	fits := r.deliver(t, srt(shifted(video, -1_500)), timing.Identity{}, rank.Facts{}, byReference)
	otherCut := srt(insert(video, end(video)/2, 60_000, episode(41, 60)))
	refused := r.subtitle(t, "Show S01E01.fr.srt", otherCut)
	alsoFits := r.subtitle(t, "Show S01E01.de.srt", srt(shifted(video, 800)))
	job := r.resync(t, timing.MethodReference, fits, refused, alsoFits)
	if !ended(&job, status.Partial, "different_cut") || job.Done != 3 || job.Total != 3 {
		t.Errorf("job %+v, want Partial with the refusal's cause, 3 of 3 done", job)
	}
	managed, _ := r.current(t, fits)
	if o := managed.Outcome; managed.Timing != (timing.Shift{Ms: 1_500}) || o == nil || o.Verdict != timing.Verified || o.Method != timing.MethodReference || o.Decision != byReference {
		t.Errorf("the managed file: timing %#v outcome %+v, want Shift{1500} verified by the reference, its decision kept", managed.Timing, o)
	}
	// An adopted file keeps no provenance (Retime), so only its timing shows.
	if adopted, _ := r.current(t, alsoFits); adopted.Timing != (timing.Shift{Ms: -800}) {
		t.Errorf("the unmanaged file: timing %#v, want Shift{-800}", adopted.Timing)
	}
	if got := r.read(t, refused); !bytes.Equal(got, otherCut) {
		t.Error("the refused file changed")
	}
	all := r.resync(t, timing.MethodReference, refused)
	if !ended(&all, status.Failed, "different_cut") {
		t.Errorf("a batch applying nothing: %+v, want Failed", all)
	}
}

func TestResyncSucceedsWhenAllApply(t *testing.T) {
	video := episode(42, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	a := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	b := r.subtitle(t, "Show S01E01.fr.srt", srt(shifted(video, 900)))
	if job := r.resync(t, timing.MethodReference, a, b); !ended(&job, status.Succeeded, "") || job.Done != 2 {
		t.Errorf("job %+v, want Succeeded, 2 done", job)
	}
}

func TestResyncSkipsUnverified(t *testing.T) {
	video := episode(47, 200)
	inc := shifted(video, -1_500)
	ref := thinnedTo(t, video, inc, 0.55)
	if fit := alignOf(t, ref, inc); fit.Shape.EdgeFit < 0.49 {
		t.Fatalf("fixture: end fit %.3f, want a fit between the end-fit gate and the gate", fit.Shape.EdgeFit)
	}
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(ref)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(inc))
	job := r.resync(t, timing.MethodReference, f)
	if !ended(&job, status.Failed, "unverified") || !bytes.Equal(r.read(t, f), srt(inc)) {
		t.Errorf("a below-gate batch file: %+v, want it skipped as unverified and unchanged", job)
	}
}

func TestResyncSkipsWhenAudioPlacesNothing(t *testing.T) {
	video := episode(48, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	// Speech 400 s late sits past every offset limit.
	ffmpegtest.UseSpeech(t, shifted(video, 400_000), end(video)+430_000)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	job := r.resync(t, timing.Audio, f)
	if !ended(&job, status.Failed, "no_audio") || !bytes.Equal(r.read(t, f), srt(shifted(video, -1_500))) {
		t.Errorf("an audio batch file audio places nothing in: %+v, want it skipped as no_audio and unchanged", job)
	}
}

func TestResyncReadsAtTurn(t *testing.T) {
	video := episode(44, 150)
	r := newRig(t, func(s *rigSetup) { s.noRun = true })
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	edited := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	untouched := r.subtitle(t, "Show S01E01.fr.srt", srt(shifted(video, -700)))
	started := make(chan status.Job)
	go func() {
		job, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: []syncsched.Target{edited, untouched}, Method: timing.MethodReference})
		if err != nil {
			t.Error(err)
		}
		started <- job
	}()
	// The job is recorded while Run has not taken it; the user edits the
	// first file before its turn.
	for {
		changed := r.status.Changed()
		if len(snapshotJobs(r)) > 0 {
			break
		}
		<-changed
	}
	users := srt(shifted(video, -9_000))
	r.subtitle(t, "Show S01E01.en.srt", users)
	r.startRun()
	job := r.wait(t, (<-started).ID)
	if !ended(&job, status.Partial, "file_changed") {
		t.Errorf("job %+v, want Partial, file_changed", job)
	}
	if !bytes.Equal(r.read(t, edited), users) {
		t.Error("the batch overwrote the user's edit")
	}
	if got, _ := r.current(t, untouched); got.Timing != (timing.Shift{Ms: 700}) {
		t.Errorf("the untouched file's timing %#v, want Shift{700}", got.Timing)
	}
}

func snapshotJobs(r *rig) []status.Job {
	snap, _ := r.status.Snapshot()
	return snap.Jobs
}

func TestBatchTooLarge(t *testing.T) {
	r := newRig(t)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(episode(45, 20)))
	over := slices.Repeat([]syncsched.Target{f}, syncsched.MaxBatch+1)
	if _, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: over, Method: timing.MethodReference}); !errors.Is(err, syncsched.ErrBatchTooLarge) {
		t.Errorf("a batch of %d: %v, want ErrBatchTooLarge", len(over), err)
	}
	if _, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Method: timing.MethodReference}); err == nil || errors.Is(err, syncsched.ErrBatchTooLarge) {
		t.Errorf("an empty batch: %v, want a caller error", err)
	}
	if n := len(snapshotJobs(r)); n != 0 {
		t.Errorf("%d jobs after refused batches, want 0", n)
	}
	withReference(t, nil)
	job := r.resync(t, timing.MethodReference, over[:syncsched.MaxBatch]...)
	if job.Total != syncsched.MaxBatch || job.Done != syncsched.MaxBatch {
		t.Errorf("a batch of %d: %+v, want every file run", syncsched.MaxBatch, job)
	}
}

// hung has every ffprobe hang, so the first job holds the slot and every
// later one queues.
func hung(t *testing.T, r *rig) syncsched.Target {
	t.Helper()
	ffmpegtest.UseFor(t, ffmpegtest.Probe, "hang")
	return r.subtitle(t, "Show S01E01.en.srt", srt(episode(46, 50)))
}

func TestJobBounds(t *testing.T) {
	r := newRig(t)
	f := hung(t, r)
	start := func(o int64) error {
		_, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: o, Files: []syncsched.Target{f}, Method: timing.MethodReference})
		return err
	}
	for range syncsched.MaxQueuedPerOwner {
		if err := start(1); err != nil {
			t.Fatal(err)
		}
	}
	if err := start(1); !errors.Is(err, syncsched.ErrSyncCapacity) {
		t.Errorf("the 9th job of one user: %v, want ErrSyncCapacity", err)
	}
	if _, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: 1, File: f.File, Expect: f.Expect, Method: timing.MethodReference}); !errors.Is(err, syncsched.ErrSyncCapacity) {
		t.Errorf("a proposal past the user's job bound: %v, want ErrSyncCapacity", err)
	}
	for o := int64(2); o <= syncsched.MaxQueued/syncsched.MaxQueuedPerOwner; o++ {
		for range syncsched.MaxQueuedPerOwner {
			if err := start(o); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := start(100); !errors.Is(err, syncsched.ErrSyncCapacity) {
		t.Errorf("the 65th job: %v, want ErrSyncCapacity", err)
	}
	if snap, _ := r.status.Snapshot(); snap.Unfinished != syncsched.MaxQueued {
		t.Errorf("%d jobs unfinished, want %d", snap.Unfinished, syncsched.MaxQueued)
	}
}

func TestShutdownMarksInterrupted(t *testing.T) {
	r := newRig(t)
	f := hung(t, r)
	running, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
	if err != nil {
		t.Fatal(err)
	}
	for {
		changed := r.status.Changed()
		if j, _, _ := r.status.Get(t.Context(), running.ID); j.State == status.Running {
			break
		}
		<-changed
	}
	// Jobs race for a free slot, so the batch starts once the proposal holds it.
	queued, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: []syncsched.Target{f}, Method: timing.MethodReference})
	if err != nil {
		t.Fatal(err)
	}
	waitQueued(t, r, "batch")
	if j, _, _ := r.status.Get(t.Context(), queued.ID); j.State != status.Queued {
		t.Errorf("a job waiting for the slot reads %v, want queued", j.State)
	}
	r.stopRun()
	for _, id := range []status.JobID{running.ID, queued.ID} {
		if j := r.wait(t, id); !ended(&j, status.Interrupted, "") {
			t.Errorf("job %+v after shutdown, want Interrupted", j)
		}
	}
	late, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: []syncsched.Target{f}, Method: timing.MethodReference})
	if err != nil {
		t.Fatal(err)
	}
	if j := r.wait(t, late.ID); !ended(&j, status.Interrupted, "") {
		t.Errorf("a job started after shutdown: %+v, want Interrupted", j)
	}
	if _, ok := r.a.Proposal(owner, running.ID); ok {
		t.Error("an interrupted proposal holds a result")
	}
}

func TestStopCancelsJob(t *testing.T) {
	r := newRig(t)
	f := hung(t, r)
	job, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.status.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if j := r.wait(t, job.ID); !ended(&j, status.Cancelled, "") {
		t.Errorf("a stopped job: %+v, want Cancelled", j)
	}
	again, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
	if err != nil || again.ID == job.ID {
		t.Errorf("a proposal after the stopped one = %s, %v; want a new job", again.ID, err)
	}
}

// waitQueued polls the rig's queue depth series until one alignment of
// class waits for the slot.
func waitQueued(t *testing.T, r *rig, class string) {
	t.Helper()
	reg := measure.NewRegistry("subflux")
	for _, m := range r.a.Collectors() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	want := `subflux_sync_queue_depth{class="` + class + `"} 1`
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		rec := httptest.NewRecorder()
		reg.Handler()(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
		if strings.Contains(rec.Body.String(), want) {
			return
		}
	}
	t.Fatalf("no %s alignment queued within a minute", class)
}
