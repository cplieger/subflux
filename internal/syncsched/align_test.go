package syncsched_test

import (
	"errors"
	"log/slog"
	"math"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

var (
	byReference = timing.Decision{Action: timing.ActionReference}
	thenAudio   = timing.Decision{Action: timing.ReferenceThenAudio}
	rigid       = timing.Decision{Action: timing.RigidReference}
	actions     = map[string]timing.Decision{"Reference": byReference, "ReferenceThenAudio": thenAudio, "RigidReference": rigid}
)

func (r *rig) align(t *testing.T, payload []byte, d timing.Decision) (syncsched.Aligned, error) {
	t.Helper()
	return r.a.Align(t.Context(), r.inst, payload, tag(t, "en"), d)
}

func TestAlignVerifiesByReference(t *testing.T) {
	video := episode(1, 200)
	for name, d := range actions {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			withReference(t, map[int][]byte{2: srt(video)}, japanese)
			got, err := r.align(t, srt(shifted(video, -2_000)), d)
			if err != nil {
				t.Fatal(err)
			}
			o := got.Outcome
			if got.Timing != (timing.Shift{Ms: 2_000}) || o.Method != timing.MethodReference || o.Verdict != timing.Verified ||
				o.OffsetMs != 2_000 || o.Decision != d || o.Confidence < 0.6 || o.Confidence > 1 {
				t.Errorf("Align = %#v, outcome %+v; want Shift{2000} verified by the reference", got.Timing, o)
			}
			if len(got.Cues.Events()) != len(video) {
				t.Errorf("Aligned holds %d cues, want the payload's %d", len(got.Cues.Events()), len(video))
			}
			if n := r.calls(ffmpegtest.PCM); n != 0 {
				t.Errorf("%d PCM calls after a verified reference fit, want 0", n)
			}
		})
	}
}

func TestAlignSkipTrusted(t *testing.T) {
	r := newRig(t)
	d := timing.Decision{Action: timing.Skip, Skip: timing.Hash}
	got, err := r.align(t, srt(episode(1, 10)), d)
	if err != nil || got.Timing != (timing.Identity{}) || got.Outcome != (timing.Outcome{Decision: d, Verdict: timing.Trusted}) || len(got.Cues.Events()) != 10 {
		t.Errorf("Align under Skip = %#v %+v, %v; want Identity, Trusted", got.Timing, got.Outcome, err)
	}
	for _, c := range []ffmpegtest.Call{ffmpegtest.Probe, ffmpegtest.Extract, ffmpegtest.PCM} {
		if n := r.calls(c); n != 0 {
			t.Errorf("%d %s calls under Skip, want 0", n, c)
		}
	}
}

func TestAlignRefusesBadInput(t *testing.T) {
	r := newRig(t)
	if _, err := r.align(t, []byte("not a subtitle"), byReference); !errors.Is(err, timing.ErrFormatUnsupported) {
		t.Errorf("an unreadable payload: %v, want timing.ErrFormatUnsupported", err)
	}
	if _, err := r.align(t, srt(episode(1, 10)), timing.Decision{}); err == nil || errors.Is(err, syncsched.ErrSyncFailed) {
		t.Errorf("no action: %v, want a caller error", err)
	}
}

func TestAlignWithoutReferenceUnverified(t *testing.T) {
	r := newRig(t)
	withReference(t, nil, track{codec: "hdmv_pgs_subtitle", lang: "jpn", index: 2}, track{codec: "subrip", lang: "eng", index: 3})
	got, err := r.align(t, srt(episode(1, 100)), byReference)
	if err != nil || got.Timing != (timing.Identity{}) || got.Outcome != (timing.Outcome{Decision: byReference, Method: timing.MethodNone, Verdict: timing.Unverified}) {
		t.Errorf("Align with no reference track = %#v %+v, %v; want Identity, Method None, Unverified", got.Timing, got.Outcome, err)
	}
	if n := r.calls(ffmpegtest.Extract); n != 0 {
		t.Errorf("%d extractions with no text track in another language, want 0", n)
	}
}

func TestReferenceFailureRunsAudio(t *testing.T) {
	video := episode(2, 200)
	payload := srt(shifted(video, -2_000))
	dur := end(video) + 30_000
	t.Run("audio verifies after a failed extraction", func(t *testing.T) {
		r := newRig(t)
		withReference(t, nil, japanese)
		ffmpegtest.UseSpeech(t, video, dur)
		got, err := r.align(t, payload, thenAudio)
		if err != nil {
			t.Fatal(err)
		}
		// The detector's overhang holds speech past each line's end, so the
		// peak sits up to 100 ms late.
		s, ok := got.Timing.(timing.Shift)
		if !ok || s.Ms < 2_000 || s.Ms > 2_150 || got.Outcome.Method != timing.Audio || got.Outcome.Verdict != timing.Verified || got.Outcome.Confidence < 0.5 {
			t.Errorf("Align = %#v %+v, want an audio Shift of about 2 s, verified at the audio gate", got.Timing, got.Outcome)
		}
		if n := r.calls(ffmpegtest.PCM); n != 1 {
			t.Errorf("%d PCM calls, want 1", n)
		}
	})
	t.Run("audio unverified after a failed extraction", func(t *testing.T) {
		r := newRig(t)
		withReference(t, nil, japanese)
		ffmpegtest.UseSpeech(t, nil, dur)
		got, err := r.align(t, payload, thenAudio)
		if !errors.Is(err, syncsched.ErrSyncFailed) || got.Timing != (timing.Identity{}) || got.Outcome.Verdict != timing.Unverified || got.Outcome.Method != timing.Audio {
			t.Errorf("Align = %#v %+v, %v; want an unverified audio pass beside ErrSyncFailed", got.Timing, got.Outcome, err)
		}
	})
	noAudio := []struct {
		name  string
		d     timing.Decision
		setup func(t *testing.T, r *rig)
		cause error
	}{
		{"ffmpeg missing", thenAudio, func(t *testing.T, _ *rig) { ffmpegtest.OnPath(t, "ffprobe") }, ffmpeg.ErrUnavailable},
		{"ffprobe missing", thenAudio, func(t *testing.T, _ *rig) { ffmpegtest.OnPath(t, "ffmpeg") }, ffmpeg.ErrUnavailable},
		{"ffmpeg and ffprobe missing", thenAudio, func(t *testing.T, _ *rig) { ffmpegtest.OnPath(t) }, ffmpeg.ErrUnavailable},
		{"video unopenable", thenAudio, func(t *testing.T, r *rig) {
			if err := os.Remove(r.video); err != nil {
				t.Fatal(err)
			}
		}, os.ErrNotExist},
		{"RigidReference", rigid, func(*testing.T, *rig) {}, nil},
		{"Reference", byReference, func(*testing.T, *rig) {}, nil},
	}
	for _, tc := range noAudio {
		t.Run(tc.name+" runs no audio", func(t *testing.T) {
			r := newRig(t)
			withReference(t, nil, japanese)
			ffmpegtest.UseSpeech(t, video, dur)
			tc.setup(t, r)
			got, err := r.align(t, payload, tc.d)
			if !errors.Is(err, syncsched.ErrSyncFailed) || tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Errorf("error %v, want ErrSyncFailed with cause %v", err, tc.cause)
			}
			if got.Timing != (timing.Identity{}) || got.Outcome.Verdict != timing.Unverified || got.Outcome.Decision != tc.d {
				t.Errorf("Align = %#v %+v, want Identity, Unverified", got.Timing, got.Outcome)
			}
			if n := r.calls(ffmpegtest.PCM); n != 0 {
				t.Errorf("%d PCM calls, want 0", n)
			}
		})
	}
}

func TestAudioPlacingNothingKeepsTheReference(t *testing.T) {
	video := episode(47, 200)
	ref := thinnedTo(t, video, video, 0.58)
	if fit := alignOf(t, ref, video); fit.Shape.EdgeFit < 0.49 {
		t.Fatalf("fixture: end fit %.3f, want a fit between the end-fit gate and the gate", fit.Shape.EdgeFit)
	}
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(ref)}, japanese)
	// Speech 400 s late sits past every offset limit.
	ffmpegtest.UseSpeech(t, shifted(video, 400_000), end(video)+430_000)
	got, err := r.align(t, srt(video), thenAudio)
	o := got.Outcome
	if err != nil || got.Timing != (timing.Identity{}) || o.Method != timing.MethodReference || o.Verdict != timing.Unverified || o.Confidence < 0.5 {
		t.Errorf("Align = %#v %+v, %v; want the reference's Unverified pass at Identity", got.Timing, o, err)
	}
	if n := r.calls(ffmpegtest.PCM); n != 1 {
		t.Errorf("%d PCM calls, want 1", n)
	}
}

func TestDifferentCutRunsNoAudio(t *testing.T) {
	video := episode(3, 260)
	extra := episode(4, 60)
	cases := map[string][]subsync.Span{
		"missing a 90 s opening":   cut(video, 0, 90_000),
		"missing a 90 s ending":    cut(video, end(video)-90_000, 90_000),
		"holding 60 s more at 8%":  insert(video, end(video)*8/100, 60_000, extra),
		"holding 60 s more at 92%": insert(video, end(video)*92/100, 60_000, extra),
		"holding 60 s more at 50%": insert(video, end(video)/2, 60_000, extra),
	}
	for name, inc := range cases {
		t.Run(name, func(t *testing.T) {
			ref := thinnedTo(t, video, inc, 0.5)
			r := newRig(t)
			withReference(t, map[int][]byte{2: srt(ref)}, japanese)
			ffmpegtest.UseSpeech(t, video, end(video)+30_000)
			got, err := r.align(t, srt(inc), thenAudio)
			if !errors.Is(err, syncsched.ErrDifferentCut) || errors.Is(err, syncsched.ErrSyncFailed) || got.Timing != nil || got.Outcome != (timing.Outcome{}) {
				t.Errorf("Align = %#v %+v, %v; want ErrDifferentCut alone", got.Timing, got.Outcome, err)
			}
			if n := r.calls(ffmpegtest.PCM); n != 0 {
				t.Errorf("%d PCM calls after a refusal, want 0", n)
			}
		})
	}
}

func TestAlignNeverPastMaxModel(t *testing.T) {
	video := episode(5, 240)
	// The second half sits 2.5 s later than the first: one offset cannot
	// place both, and a piecewise fit could.
	half := len(video) / 2
	inc := append(shifted(video[:half], -2_000), shifted(video[half:], -4_500)...)
	for name, d := range actions {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			withReference(t, map[int][]byte{2: srt(video)}, japanese)
			ffmpegtest.UseSpeech(t, video, end(video)+30_000)
			got, err := r.align(t, srt(inc), d)
			if err != nil && !errors.Is(err, syncsched.ErrDifferentCut) {
				t.Fatal(err)
			}
			if _, ok := got.Timing.(timing.Piecewise); ok {
				t.Errorf("Align = %#v, want no model past Linear", got.Timing)
			}
		})
	}
}

// cut is the candidate for a cut of video without [at, at+lenMs).
func cut(video []subsync.Span, at, lenMs int64) []subsync.Span {
	var out []subsync.Span
	for _, s := range video {
		switch {
		case s.EndMs <= at:
			out = append(out, s)
		case s.StartMs >= at+lenMs:
			out = append(out, subsync.Span{StartMs: s.StartMs - lenMs, EndMs: s.EndMs - lenMs})
		}
	}
	return out
}

// insert is the candidate holding lenMs of extra's material at at, which
// video lacks.
func insert(video []subsync.Span, at, lenMs int64, extra []subsync.Span) []subsync.Span {
	var out []subsync.Span
	for _, s := range video {
		if s.StartMs < at {
			out = append(out, s)
		}
	}
	for _, s := range extra {
		if s.EndMs-extra[0].StartMs <= lenMs {
			out = append(out, subsync.Span{StartMs: s.StartMs - extra[0].StartMs + at, EndMs: s.EndMs - extra[0].StartMs + at})
		}
	}
	for _, s := range video {
		if s.StartMs >= at {
			out = append(out, subsync.Span{StartMs: s.StartMs + lenMs, EndMs: s.EndMs + lenMs})
		}
	}
	return out
}

// displaced moves the share f of ref's inner cues, spread evenly, into the
// gap after each: still dialogue, no longer under the candidate's lines.
func displaced(ref []subsync.Span, f float64) []subsync.Span {
	out := slices.Clone(ref)
	for i := 1; i+1 < len(out); i++ {
		if math.Mod(float64(i)*0.6180339887, 1) >= f {
			continue
		}
		gap := ref[i+1].StartMs - ref[i].EndMs
		l := min(ref[i].EndMs-ref[i].StartMs, max(gap-400, 1))
		mid := ref[i].EndMs + gap/2
		out[i] = subsync.Span{StartMs: mid - l/2, EndMs: mid - l/2 + l}
	}
	return out
}

// thinnedTo is video displaced until inc's reference fit scores about
// target, below the gate.
func thinnedTo(t *testing.T, video, inc []subsync.Span, target float64) []subsync.Span {
	t.Helper()
	score := func(f float64) float64 {
		res, err := subsync.AlignReference(t.Context(), displaced(video, f), inc, subsync.MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		return res.Confidence
	}
	lo, hi := 0.0, 1.0
	for range 20 {
		if mid := (lo + hi) / 2; score(mid) > target {
			lo = mid
		} else {
			hi = mid
		}
	}
	if c := score(lo); c < target-0.1 || c >= 0.6 {
		t.Fatalf("fixture: reference e2 %.3f, want about %.2f", c, target)
	}
	return displaced(video, lo)
}

// logBuffer collects a logger's text output.
type logBuffer struct {
	b  strings.Builder
	mu sync.Mutex
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestPanicFailsJob(t *testing.T) {
	video := episode(10, 150)
	var logs logBuffer
	var calls atomic.Int32
	r := newRig(t, withDeps(func(d *syncsched.Deps) {
		d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		policy := d.Policy
		d.Policy = func() language.Policy {
			if calls.Add(1) == 1 {
				panic("policy read failed")
			}
			return policy()
		}
	}))
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	payload := srt(shifted(video, -1_500))
	got, err := r.align(t, payload, byReference)
	if !errors.Is(err, syncsched.ErrSyncFailed) || got.Timing != (timing.Identity{}) || got.Outcome.Verdict != timing.Unverified {
		t.Errorf("Align through a panic = %#v %+v, %v; want Identity, Unverified beside ErrSyncFailed", got.Timing, got.Outcome, err)
	}
	if out := logs.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "policy read failed") {
		t.Errorf("logs %q, want the panic at ERROR", out)
	}
	if got, err := r.align(t, payload, byReference); err != nil || got.Timing != (timing.Shift{Ms: 1_500}) {
		t.Errorf("the next Align = %#v, %v; want the slot free and Shift{1500}", got.Timing, err)
	}
}

func TestCollectorsLabelsClosed(t *testing.T) {
	video := episode(11, 150)
	r := newRig(t)
	reg := measure.NewRegistry("subflux")
	for _, m := range r.a.Collectors() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	if _, err := r.align(t, srt(shifted(video, -1_500)), byReference); err != nil {
		t.Fatal(err)
	}
	if _, err := r.align(t, srt(episode(12, 150)), timing.Decision{Action: timing.Skip, Skip: timing.Hash}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.video, []byte("another video"), 0o600); err != nil {
		t.Fatal(err)
	}
	withReference(t, nil, japanese)
	if _, err := r.align(t, srt(episode(12, 150)), byReference); !errors.Is(err, syncsched.ErrSyncFailed) {
		t.Fatalf("a failed extraction: %v", err)
	}
	rec := httptest.NewRecorder()
	reg.Handler()(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`subflux_sync_runs_total{method="reference",origin="search",verdict="verified"} 1`,
		`subflux_sync_runs_total{method="none",origin="search",verdict="failed"} 1`,
		`subflux_sync_failures_total{stage="extract"} 1`,
		`subflux_sync_slot_wait_seconds_count 2`,
		`subflux_sync_run_seconds_count{method="reference"} 1`,
		`subflux_sync_queue_depth{class="interactive"} 0`,
		`subflux_sync_queue_depth{class="automatic"} 0`,
		`subflux_sync_queue_depth{class="batch"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %s", want)
		}
	}
	closed := map[string][]string{
		"origin":  {"search", "proposal", "batch"},
		"method":  {"none", "reference", "audio"},
		"verdict": {"verified", "unverified", "different_cut", "failed", "canceled"},
		"stage":   {"open", "probe", "extract", "pcm", "panic"},
		"class":   {"interactive", "automatic", "batch"},
		"le":      nil,
	}
	label := regexp.MustCompile(`(\w+)="([^"]*)"`)
	for line := range strings.Lines(body) {
		if !strings.HasPrefix(line, "subflux_sync_") {
			continue
		}
		for _, m := range label.FindAllStringSubmatch(line, -1) {
			values, known := closed[m[1]]
			if !known || values != nil && !slices.Contains(values, m[2]) {
				t.Errorf("series %s: label %s=%q is outside its closed set", strings.TrimSpace(line), m[1], m[2])
			}
		}
	}
}
