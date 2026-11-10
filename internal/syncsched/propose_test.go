package syncsched_test

import (
	"errors"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

func TestProposeHoldsVerifiedFit(t *testing.T) {
	video := episode(20, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	job := r.propose(t, f, timing.MethodReference)
	if !ended(&job, status.Succeeded, "") || job.Kind != status.Sync || job.Owner != owner || job.Scope != f.File.Name() || job.Done != 1 || job.Total != 1 {
		t.Fatalf("job %+v, want a succeeded Sync job of one file", job)
	}
	p, ok := r.a.Proposal(owner, job.ID)
	if !ok {
		t.Fatal("no proposal held")
	}
	o := p.Outcome
	if p.Job != job.ID || p.File != f.File || p.Expect != f.Expect || p.Timing != (timing.Shift{Ms: 1_500}) ||
		o.Method != timing.MethodReference || o.Verdict != timing.Verified || o.OffsetMs != 1_500 || o.Confidence < 0.6 || p.Ended.IsZero() {
		t.Errorf("Proposal = %+v, want Shift{1500} verified by the reference for the file as listed", p)
	}
	if _, ok := r.a.Proposal(owner+1, job.ID); ok {
		t.Error("another owner reads the proposal")
	}
	if n := r.calls(ffmpegtest.PCM); n != 0 {
		t.Errorf("%d PCM calls for a reference proposal, want 0", n)
	}
}

func TestProposeAudio(t *testing.T) {
	video := episode(21, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	ffmpegtest.UseSpeech(t, video, end(video)+30_000)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -2_000)))
	job := r.propose(t, f, timing.Audio)
	p, ok := r.a.Proposal(owner, job.ID)
	if !ended(&job, status.Succeeded, "") || !ok {
		t.Fatalf("job %+v, proposal held %v; want a succeeded audio proposal", job, ok)
	}
	s, isShift := p.Timing.(timing.Shift)
	if !isShift || s.Ms < 2_000 || s.Ms > 2_150 || p.Outcome.Method != timing.Audio || p.Outcome.Verdict != timing.Verified {
		t.Errorf("Proposal = %#v %+v, want an audio Shift of about 2 s, verified", p.Timing, p.Outcome)
	}
	if n := r.calls(ffmpegtest.PCM); n != 1 {
		t.Errorf("%d PCM calls, want 1", n)
	}
}

func TestBelowGateProposalHoldsOutcome(t *testing.T) {
	video := episode(22, 200)
	inc := shifted(video, -1_500)
	ref := thinnedTo(t, video, inc, 0.55)
	if fit := alignOf(t, ref, inc); fit.Shape.EdgeFit < 0.49 {
		t.Fatalf("fixture: end fit %.3f, want a fit between the end-fit gate and the gate", fit.Shape.EdgeFit)
	}
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(ref)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(inc))
	job := r.propose(t, f, timing.MethodReference)
	p, ok := r.a.Proposal(owner, job.ID)
	if !ended(&job, status.Succeeded, "") || !ok {
		t.Fatalf("job %+v, proposal held %v; want a held below-gate proposal", job, ok)
	}
	o := p.Outcome
	if p.Timing != (timing.Shift{Ms: 1_500}) || o.Verdict != timing.Unverified || o.Method != timing.MethodReference ||
		o.OffsetMs != 1_500 || o.Confidence < 0.4 || o.Confidence >= 0.6 {
		t.Errorf("Proposal = %#v %+v; want the raw Shift{1500} with its Unverified reference outcome and its confidence", p.Timing, o)
	}
}

func TestStaleProposalRefusedBeforeJob(t *testing.T) {
	r := newRig(t)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(episode(23, 50)))
	r.subtitle(t, "Show S01E01.en.srt", srt(episode(24, 50)))
	_, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
	if !errors.Is(err, mediawrite.ErrExpectation) {
		t.Errorf("Propose over a changed file: %v, want ErrExpectation", err)
	}
	if snap, _ := r.status.Snapshot(); len(snap.Jobs) != 0 {
		t.Errorf("%d jobs visible after a refused Propose, want 0", len(snap.Jobs))
	}
}

func TestProposeRefusesBadMethod(t *testing.T) {
	r := newRig(t)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(episode(23, 50)))
	for _, m := range []timing.Method{timing.MethodNone, timing.Audio + 1} {
		if _, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: m}); err == nil {
			t.Errorf("Propose by method %d: nil error", m)
		}
		if _, err := r.a.Resync(t.Context(), syncsched.ResyncRequest{Owner: owner, Files: []syncsched.Target{f}, Method: m}); err == nil {
			t.Errorf("Resync by method %d: nil error", m)
		}
	}
}

func TestLiveProposalReturned(t *testing.T) {
	video := episode(25, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	first := r.propose(t, f, timing.MethodReference)
	again, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
	if err != nil || again.ID != first.ID {
		t.Errorf("a second proposal of the same file and method = %s, %v; want job %s", again.ID, err, first.ID)
	}
	other := []struct {
		name  string
		owner int64
		m     timing.Method
	}{{"another owner", owner + 1, timing.MethodReference}, {"another method", owner, timing.Audio}}
	for _, o := range other {
		job, err := r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: o.owner, File: f.File, Expect: f.Expect, Method: o.m})
		if err != nil || job.ID == first.ID {
			t.Errorf("%s: job %s, %v; want a new job", o.name, job.ID, err)
		}
		r.wait(t, job.ID)
	}
	if n := r.calls(ffmpegtest.Extract); n != 1 {
		t.Errorf("%d extractions, want the span cache's 1", n)
	}
}

func TestKeyedProposeReplays(t *testing.T) {
	video := episode(26, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.srt", srt(shifted(video, -1_500)))
	key := &status.JobKey{Key: "k1", Fingerprint: [32]byte{1}}
	req := syncsched.ProposeRequest{Owner: owner, File: f.File, Expect: f.Expect, Method: timing.MethodReference, Key: key}
	first, err := r.a.Propose(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	r.wait(t, first.ID)
	// The applied proposal changes the file, so only the key finds the job.
	p, _ := r.a.Proposal(owner, first.ID)
	if _, err := r.writer.Retime(t.Context(), p.File, p.Expect, p.Timing, &p.Outcome); err != nil {
		t.Fatal(err)
	}
	again, err := r.a.Propose(t.Context(), req)
	if err != nil || again.ID != first.ID {
		t.Errorf("a keyed resend = %s, %v; want job %s", again.ID, err, first.ID)
	}
	req.Key = &status.JobKey{Key: "k1", Fingerprint: [32]byte{2}}
	if _, err := r.a.Propose(t.Context(), req); !errors.Is(err, status.ErrKeyReused) {
		t.Errorf("the key with another request: %v, want status.ErrKeyReused", err)
	}
}

// TestRigidFileJobs delivers a flagged-cut pick under each Decision that
// can carry one: every one is Rigid, whatever its Action.
func TestRigidFileJobs(t *testing.T) {
	video := episode(27, 200)
	decisions := map[string]timing.Decision{
		"RigidReference": rigid,
		"Skip/Disabled":  {Action: timing.Skip, Skip: timing.Disabled},
		"Skip/Hash":      {Action: timing.Skip, Skip: timing.Hash},
		"Skip/Forced":    {Action: timing.Skip, Skip: timing.Forced},
	}
	for name, d := range decisions {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			withReference(t, map[int][]byte{2: srt(video)}, japanese)
			ffmpegtest.UseSpeech(t, video, end(video)+30_000)
			f := r.deliver(t, srt(shifted(video, -1_500)), timing.Identity{}, rank.Facts{Cut: rank.Differs}, d)
			audio := r.propose(t, f, timing.Audio)
			if !ended(&audio, status.Failed, "different_cut") {
				t.Errorf("an audio proposal on a rigid file: %+v, want Failed, different_cut", audio)
			}
			if _, ok := r.a.Proposal(owner, audio.ID); ok {
				t.Error("a refused audio proposal holds a result")
			}
			audioBatch := r.resync(t, timing.Audio, f)
			if !ended(&audioBatch, status.Failed, "different_cut") {
				t.Errorf("an audio batch over a rigid file: %+v, want Failed, different_cut", audioBatch)
			}
			if n := r.calls(ffmpegtest.PCM); n != 0 {
				t.Errorf("%d PCM calls for a rigid file, want 0", n)
			}
			ref := r.propose(t, f, timing.MethodReference)
			p, ok := r.a.Proposal(owner, ref.ID)
			if !ok || p.Timing != (timing.Shift{Ms: 1_500}) {
				t.Fatalf("a reference proposal on a rigid file: %+v, %#v; want Shift{1500}", ref, p.Timing)
			}
			if _, err := r.writer.Retime(t.Context(), p.File, p.Expect, p.Timing, &p.Outcome); err != nil {
				t.Fatal(err)
			}
			timed, now := r.current(t, f)
			if !timed.Rigid || timed.Timing != (timing.Shift{Ms: 1_500}) {
				t.Errorf("the applied proposal: timing %#v, rigid %v; want Shift{1500} with the record's facts kept", timed.Timing, timed.Rigid)
			}
			batch := r.resync(t, timing.MethodReference, now)
			after, _ := r.current(t, f)
			if !ended(&batch, status.Succeeded, "") || after.Timing != (timing.Shift{Ms: 1_500}) || !after.Rigid {
				t.Errorf("a batch over a rigid file: %+v, timing %#v, rigid %v; want it applied with the facts kept", batch, after.Timing, after.Rigid)
			}
		})
	}
}

// forcedLines is the forced subtitle of video, shifted by ms: only two
// stretches of its dialogue, the foreign-language parts.
func forcedLines(video []subsync.Span, ms int64) []subsync.Span {
	n := len(video)
	return shifted(append(append([]subsync.Span(nil), video[n/8:n/8+20]...), video[n*5/8:n*5/8+20]...), ms)
}

func TestForcedUnmanagedSettlesForced(t *testing.T) {
	video := episode(28, 200)
	inc := forcedLines(video, -1_500)
	fit := alignOf(t, video, inc)
	if _, _, err := subsync.Settle(&fit, false, subsync.AtIdentity); !errors.Is(err, subsync.ErrDifferentCut) {
		t.Fatalf("fixture: unforced Settle %v, want the coverage guard's refusal", err)
	}
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	f := r.subtitle(t, "Show S01E01.en.forced.srt", srt(inc))
	job := r.propose(t, f, timing.MethodReference)
	p, ok := r.a.Proposal(owner, job.ID)
	if !ended(&job, status.Succeeded, "") || !ok || p.Timing != (timing.Shift{Ms: 1_500}) || p.Outcome.Verdict != timing.Verified {
		t.Errorf("a forced file's proposal: %+v, %#v %+v; want Shift{1500} verified", job, p.Timing, p.Outcome)
	}
	batch := r.resync(t, timing.MethodReference, f)
	after, _ := r.current(t, f)
	if !ended(&batch, status.Succeeded, "") || after.Timing != (timing.Shift{Ms: 1_500}) || !after.Flags.Forced {
		t.Errorf("a batch over the forced file: %+v, timing %#v; want it applied at Shift{1500}", batch, after.Timing)
	}
}

func alignOf(t *testing.T, ref, inc []subsync.Span) subsync.Result {
	t.Helper()
	res, err := subsync.AlignReference(t.Context(), ref, inc, subsync.MaxModel)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMissingFFmpegCause(t *testing.T) {
	for name, m := range map[string]timing.Method{"reference": timing.MethodReference, "audio": timing.Audio} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			ffmpegtest.OnPath(t)
			f := r.subtitle(t, "Show S01E01.en.srt", srt(episode(29, 100)))
			job := r.propose(t, f, m)
			if !ended(&job, status.Failed, "ffmpeg_unavailable") {
				t.Errorf("a proposal with no ffmpeg: %+v, want Failed, ffmpeg_unavailable", job)
			}
		})
	}
}

func TestProposalWithNothingToMatch(t *testing.T) {
	video := episode(21, 150)
	noText := func(t *testing.T) {
		withReference(t, nil, track{codec: "hdmv_pgs_subtitle", lang: "jpn", index: 2})
	}
	// Speech 400 s late sits past every offset limit, so audio places nothing.
	late := func(t *testing.T) { ffmpegtest.UseSpeech(t, shifted(video, 400_000), end(video)+430_000) }
	cases := map[string]struct {
		setup func(t *testing.T)
		cause string
		m     timing.Method
	}{
		"reference": {noText, "no_reference", timing.MethodReference},
		"audio without frames": {func(t *testing.T) {
			noText(t)
			ffmpegtest.UseFor(t, ffmpegtest.PCM, "pcm:0")
		}, "no_audio", timing.Audio},
		"audio past the offset limit": {func(t *testing.T) { noText(t); late(t) }, "no_audio", timing.Audio},
		"audio past the offset limit, beside a guarded reference": {func(t *testing.T) {
			withReference(t, map[int][]byte{2: srt(video)}, japanese)
			late(t)
		}, "no_audio", timing.Audio},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			tc.setup(t)
			f := r.subtitle(t, "Show S01E01.en.srt", srt(video))
			job := r.propose(t, f, tc.m)
			if !ended(&job, status.Failed, tc.cause) {
				t.Errorf("job %+v, want Failed, %s", job, tc.cause)
			}
			if _, ok := r.a.Proposal(owner, job.ID); ok {
				t.Error("a proposal with no fit holds a result")
			}
		})
	}
}

func TestProposalBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		video := episode(32, 60)
		r := newRig(t)
		withReference(t, map[int][]byte{2: srt(video)}, japanese)
		var files []syncsched.Target
		for i := range syncsched.MaxProposalsPerOwner + 1 {
			name := "Show S01E01.en.srt"
			if i > 0 {
				name = "Show S01E01.en." + strconv.Itoa(i) + ".srt"
			}
			files = append(files, r.subtitle(t, name, srt(shifted(video, -int64(100*(i+1))))))
		}
		propose := func(o int64, f syncsched.Target) (status.Job, error) {
			return r.a.Propose(t.Context(), syncsched.ProposeRequest{Owner: o, File: f.File, Expect: f.Expect, Method: timing.MethodReference})
		}
		var first status.JobID
		owners := syncsched.MaxProposals / syncsched.MaxProposalsPerOwner
		for o := range int64(owners) {
			for _, f := range files[:syncsched.MaxProposalsPerOwner] {
				job, err := propose(o+1, f)
				if err != nil {
					t.Fatalf("owner %d, %s: %v", o+1, f.File.Name(), err)
				}
				if j := r.wait(t, job.ID); !ended(&j, status.Succeeded, "") {
					t.Fatalf("job %+v", j)
				}
				if first == "" {
					first = job.ID
				}
			}
			if o == 0 {
				if _, err := propose(1, files[syncsched.MaxProposalsPerOwner]); !errors.Is(err, syncsched.ErrSyncCapacity) {
					t.Errorf("one user's 17th proposal: %v, want ErrSyncCapacity", err)
				}
			}
		}
		if _, err := propose(int64(owners)+1, files[0]); !errors.Is(err, syncsched.ErrSyncCapacity) {
			t.Errorf("the 257th proposal: %v, want ErrSyncCapacity", err)
		}
		time.Sleep(syncsched.ProposalTTL - time.Nanosecond)
		if _, ok := r.a.Proposal(1, first); !ok {
			t.Error("a proposal is gone before its 15 minutes")
		}
		time.Sleep(time.Nanosecond)
		if _, ok := r.a.Proposal(1, first); ok {
			t.Error("a proposal is readable past its 15 minutes")
		}
		job, err := propose(int64(owners)+1, files[0])
		if err != nil {
			t.Fatalf("a proposal once the others expired: %v", err)
		}
		r.wait(t, job.ID)
	})
}
