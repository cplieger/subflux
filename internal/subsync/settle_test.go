package subsync

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// cutCase is a candidate for another cut of video and, per candidate span
// in order, the video time it stands for: noTruth where the video lacks it.
type cutCase struct {
	name       string
	video, inc []Span
	truth      []int64
}

const noTruth = -1

// The research's fit (reference-sync-research.md section 6): a mapping
// places a candidate when 90% of its truth-bearing spans land within
// 600 ms.
const (
	placedTolMs = 600
	placedShare = 0.9
)

// truthOf is f of each span's start.
func truthOf(inc []Span, f func(int64) int64) []int64 {
	out := make([]int64, len(inc))
	for i, s := range inc {
		out[i] = f(s.StartMs)
	}
	return out
}

// cutTruth is the truth of cut(video, at, lenMs).
func cutTruth(at, lenMs int64) func(int64) int64 {
	return func(ms int64) int64 {
		if ms < at {
			return ms
		}
		return ms + lenMs
	}
}

// insertTruth is the truth of insert(video, at, lenMs, extra).
func insertTruth(at, lenMs int64) func(int64) int64 {
	return func(ms int64) int64 {
		switch {
		case ms < at:
			return ms
		case ms < at+lenMs:
			return noTruth
		}
		return ms - lenMs
	}
}

// placed reports whether tm places c's candidate.
func placed(c cutCase, tm timing.Timing) bool {
	n, ok := 0, 0
	for i, s := range mapSpans(c.inc, tm) {
		if c.truth[i] == noTruth {
			continue
		}
		n++
		if d := s.StartMs - c.truth[i]; s.StartMs != droppedOffset && d <= placedTolMs && d >= -placedTolMs {
			ok++
		}
	}
	return n > 0 && float64(ok) >= placedShare*float64(n)
}

// thinLevels are the global e2 the reference is thinned to: none, about
// half, and just under the gate.
var thinLevels = []float64{0, 0.5, 0.59}

// guardCut settles c unforced at the full reference, where it must be
// refused, or placed unless mustRefuse, and at the reference thinned to each
// of thinLevels, where a Verified delivery must be placed and anything else
// is refused or Identity, Unverified: a reference too weak to judge the
// candidate, G4-23's residual.
func guardCut(t *testing.T, c cutCase, mustRefuse bool) {
	t.Helper()
	whole, err := AlignReference(t.Context(), c.video, c.inc, MaxModel)
	if err != nil {
		t.Fatal(err)
	}
	tm, v, err := Settle(&whole, false, AtIdentity)
	if !errors.Is(err, ErrDifferentCut) && (mustRefuse || !placed(c, tm)) {
		t.Errorf("full reference: e2 %.3f shape %+v settled %#v %v %v, want ErrDifferentCut%s", whole.Confidence, whole.Shape, tm, v, err,
			map[bool]string{false: " or the candidate placed", true: ""}[mustRefuse])
	}
	for _, level := range thinLevels {
		r, err := AlignReference(t.Context(), thinned(c.video, c.inc, whole.Timing, level), c.inc, MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		tm, v, err := Settle(&r, false, AtIdentity)
		switch {
		case errors.Is(err, ErrDifferentCut):
		case err != nil:
			t.Errorf("thinned to %.2f: %v", level, err)
		case v == timing.Verified && !placed(c, tm):
			t.Errorf("thinned to %.2f: e2 %.3f shape %+v Verified %#v, misplaced", level, r.Confidence, r.Shape, tm)
		case v == timing.Unverified && tm != (timing.Identity{}):
			t.Errorf("thinned to %.2f: Unverified at %#v, want Identity", level, tm)
		}
	}
}

// dialogueKinds are an episode's sparse and dense forms.
func dialogueKinds(t *testing.T, k, n int) map[string][]Span {
	return map[string][]Span{"sparse": episode(t, k, n), "dense": dense(t, k, n)}
}

func end(spans []Span) int64 { return spans[len(spans)-1].EndMs }

func TestCoverageGuardAtAnyConfidence(t *testing.T) {
	t.Parallel()
	var cases []cutCase
	for kind, video := range dialogueKinds(t, 7, 260) {
		cases = append(cases,
			cutOf(kind+" missing a 90 s opening", video, 0, 90_000),
			cutOf(kind+" missing a 90 s ending", video, end(video)-90_000, 90_000),
			cutOf(kind+" missing a 90 s scene at 8 minutes", video, 480_000, 90_000),
		)
		for _, l := range []int64{20_000, 60_000} {
			for _, p := range []float64{0.08, 0.92} {
				at := int64(p * float64(end(video)))
				cases = append(cases, cutOf(fmt.Sprintf("%s missing %d s at %.0f%%", kind, l/1000, p*100), video, at, l))
			}
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			guardCut(t, c, false)
		})
	}
}

// cutOf is the case of a candidate for video without [at, at+lenMs).
func cutOf(name string, video []Span, at, lenMs int64) cutCase {
	inc := cut(video, at, lenMs)
	return cutCase{name, video, inc, truthOf(inc, cutTruth(at, lenMs))}
}

func TestForcedSubtitleVerifiesAtItsShift(t *testing.T) {
	t.Parallel()
	video := episode(t, 7, 260)
	var forced []Span
	for i, s := range video {
		if i%10 == 3 {
			forced = append(forced, Span{s.StartMs - 4_200, s.EndMs - 4_200})
		}
	}
	r, err := AlignReference(t.Context(), video, forced, MaxModel)
	if err != nil {
		t.Fatal(err)
	}
	tm, v, err := Settle(&r, true, AtIdentity)
	if err != nil || tm != (timing.Shift{Ms: 4_200}) || v != timing.Verified {
		t.Errorf("forced: %#v %v %v (shape %+v), want Shift{4200} Verified", tm, v, err, r.Shape)
	}
	if _, _, err := Settle(&r, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
		t.Errorf("the same fit unforced: %v, want ErrDifferentCut (its gaps hold the other 90%%)", err)
	}
}

func TestMirrorCasesGuarded(t *testing.T) {
	t.Parallel()
	var cases []cutCase
	for kind, video := range dialogueKinds(t, 8, 260) {
		extra := dialogueKinds(t, 9, 60)[kind]
		for _, l := range []int64{20_000, 60_000} {
			for _, p := range []float64{0.08, 0.92} {
				at := int64(p * float64(end(video)))
				inc := insert(video, at, l, extra)
				cases = append(cases, cutCase{fmt.Sprintf("%s holding %d s more at %.0f%%", kind, l/1000, p*100), video, inc, truthOf(inc, insertTruth(at, l))})
			}
		}
	}
	// The round-4 reproduction: the last 32 sparse cues 20 s late.
	video := episode(t, 8, 260)
	late := slices.Clone(video)
	for i := len(late) - 32; i < len(late); i++ {
		late[i] = Span{late[i].StartMs + 20_000, late[i].EndMs + 20_000}
	}
	cases = append(cases, cutCase{"32 sparse tail cues 20 s late", video, late, truthOf(video, func(ms int64) int64 { return ms })})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			guardCut(t, c, false)
		})
	}
}

func TestAdvertisingLinesVerify(t *testing.T) {
	t.Parallel()
	video := episode(t, 8, 260)
	inc := append([]Span{{1_000, 3_500}}, shiftSpans(video[1:], -1_700)...)
	inc = append(inc, Span{end(video) - 1_700 + 40_000, end(video) - 1_700 + 43_000})
	r, err := AlignReference(t.Context(), video, inc, MaxModel)
	if err != nil {
		t.Fatal(err)
	}
	if tm, v, err := Settle(&r, false, AtIdentity); err != nil || tm != (timing.Shift{Ms: 1_700}) || v != timing.Verified {
		t.Errorf("advertising lines at 0:00:01 and after the last dialogue: %#v %v %v (shape %+v)", tm, v, err, r.Shape)
	}
}

func TestExtraMaterialGuarded(t *testing.T) {
	t.Parallel()
	video := dense(t, 10, 600)
	extra := dense(t, 11, 60)
	mid, early, late := end(video)/2, end(video)*3/10, end(video)*7/10+60_000
	one := insert(video, mid, 60_000, extra)
	two := insert(insert(video, early, 60_000, extra), late, 60_000, extra)
	for name, c := range map[string]struct {
		inc   []Span
		truth func(int64) int64
	}{
		"one 60 s stretch at 50%": {one, insertTruth(mid, 60_000)},
		"two at 30% and 70%": {two, func(ms int64) int64 {
			if v := insertTruth(late, 60_000)(ms); v != noTruth {
				return insertTruth(early, 60_000)(v)
			}
			return noTruth
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inc := shiftSpans(c.inc, -2_000)
			guardCut(t, cutCase{name, video, inc, truthOf(inc, func(ms int64) int64 { return c.truth(ms + 2_000) })}, false)
		})
	}
	t.Run("a uniformly poor fit under edgeGate is refused", func(t *testing.T) {
		t.Parallel()
		inc := shiftSpans(video, -2_000)
		ref := thinned(video, inc, timing.Shift{Ms: 2_000}, 0.4)
		r, err := AlignReference(t.Context(), ref, inc, MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		if tm, v, err := Settle(&r, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("e2 %.3f shape %+v: %#v %v %v, want ErrDifferentCut", r.Confidence, r.Shape, tm, v, err)
		}
	})
	t.Run("an aligned candidate between edgeGate and the gate stays unverified", func(t *testing.T) {
		t.Parallel()
		aligned := episode(t, 8, 260)
		ref := thinned(aligned, aligned, timing.Identity{}, 0.56)
		r, err := AlignReference(t.Context(), ref, aligned, MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		if r.Confidence < edgeGate || r.Confidence >= referenceGate || r.Shape.EdgeFit < edgeGate {
			t.Fatalf("fixture: e2 %.3f shape %+v, want e2 and EdgeFit between edgeGate and the gate", r.Confidence, r.Shape)
		}
		if tm, v, err := Settle(&r, false, AtIdentity); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("e2 %.3f shape %+v: %#v %v %v, want Identity/Unverified", r.Confidence, r.Shape, tm, v, err)
		}
	})
}

func TestSettlePerGround(t *testing.T) {
	t.Parallel()
	clean := Shape{EdgeFit: 0.9}
	over := map[string]Shape{
		"InsertedMs":   {InsertedMs: maxRiseMs + 1, EdgeFit: 0.9},
		"EdgeSpeechMs": {EdgeSpeechMs: maxEdgeSpeechMs + 1, EdgeFit: 0.9},
		"EdgeFit":      {EdgeFit: edgeGate - 0.01},
	}
	at := map[string]Shape{
		"InsertedMs":   {InsertedMs: maxRiseMs, EdgeFit: 0.9},
		"EdgeSpeechMs": {EdgeSpeechMs: maxEdgeSpeechMs, EdgeFit: 0.9},
		"EdgeFit":      {EdgeFit: edgeGate},
	}
	// fit is a measured reference fit whose two mappings both have sh.
	fit := func(conf float64, sh Shape) Result {
		return Result{Timing: timing.Shift{Ms: 500}, Method: timing.MethodReference, Confidence: conf, Shape: sh, IdentityShape: sh, Measured: true}
	}
	for ground, sh := range over {
		for _, conf := range []float64{0.2, referenceGate} {
			t.Run(fmt.Sprintf("%s over at %.1f", ground, conf), func(t *testing.T) {
				t.Parallel()
				if _, _, err := Settle(new(fit(conf, sh)), false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
					t.Errorf("err = %v, want ErrDifferentCut", err)
				}
				_, _, err := Settle(new(fit(conf, sh)), true, AtIdentity)
				if wantRefused := ground != "EdgeSpeechMs"; errors.Is(err, ErrDifferentCut) != wantRefused {
					t.Errorf("forced: err = %v, want refused %v", err, wantRefused)
				}
			})
		}
		t.Run(ground+" at its bound", func(t *testing.T) {
			t.Parallel()
			if tm, v, err := Settle(new(fit(referenceGate, at[ground])), false, AtIdentity); err != nil || tm != (timing.Shift{Ms: 500}) || v != timing.Verified {
				t.Errorf("%#v %v %v, want the fit Verified", tm, v, err)
			}
		})
	}
	cut := Shape{EdgeSpeechMs: maxEdgeSpeechMs + 1, EdgeFit: 0.9}
	t.Run("below the gate the guard judges the Identity it delivers", func(t *testing.T) {
		t.Parallel()
		r := fit(referenceGate-0.01, clean)
		r.IdentityShape = cut
		if tm, v, err := Settle(&r, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("Identity uncovering an ending: %#v %v %v, want ErrDifferentCut", tm, v, err)
		}
		r.IdentityShape = Shape{EdgeFit: edgeGate - 0.01}
		if tm, v, err := Settle(&r, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("Identity fitting under edgeGate: %#v %v %v, want ErrDifferentCut", tm, v, err)
		}
	})
	t.Run("below the gate the guard judges the fit too", func(t *testing.T) {
		t.Parallel()
		r := fit(referenceGate-0.01, cut)
		r.IdentityShape = clean
		if tm, v, err := Settle(&r, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("a fit uncovering an ending: %#v %v %v, want ErrDifferentCut", tm, v, err)
		}
	})
	t.Run("between edgeGate and the gate a clean fit is Identity and Unverified", func(t *testing.T) {
		t.Parallel()
		if tm, v, err := Settle(new(fit(referenceGate-0.01, clean)), false, AtIdentity); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("%#v %v %v, want Identity/Unverified", tm, v, err)
		}
	})
	t.Run("AtFit offers the fit below the gate, judging it and not Identity", func(t *testing.T) {
		t.Parallel()
		r := fit(referenceGate-0.01, clean)
		r.IdentityShape = cut
		if tm, v, err := Settle(&r, false, AtFit); err != nil || tm != (timing.Shift{Ms: 500}) || v != timing.Unverified {
			t.Errorf("%#v %v %v, want the fit's Shift Unverified", tm, v, err)
		}
		r.Shape = Shape{EdgeFit: edgeGate - 0.01}
		if tm, v, err := Settle(&r, false, AtFit); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("a fit under edgeGate: %#v %v %v, want ErrDifferentCut", tm, v, err)
		}
	})
	t.Run("at the gate the Identity it does not deliver is not judged", func(t *testing.T) {
		t.Parallel()
		r := fit(referenceGate, clean)
		r.IdentityShape = cut
		if tm, v, err := Settle(&r, false, AtIdentity); err != nil || tm != (timing.Shift{Ms: 500}) || v != timing.Verified {
			t.Errorf("%#v %v %v, want the fit Verified", tm, v, err)
		}
	})
	t.Run("ModelNone is Identity and Unverified", func(t *testing.T) {
		t.Parallel()
		if tm, v, err := Settle(new(noFit), false, AtIdentity); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("%#v %v %v", tm, v, err)
		}
	})
}

func TestAudioSettledAtItsGate(t *testing.T) {
	t.Parallel()
	// audio is a measured audio fit whose Identity is clean.
	audio := func(conf float64, sh Shape) Result {
		return Result{Timing: timing.Shift{Ms: 900}, Method: timing.Audio, Confidence: conf, Shape: sh, IdentityShape: Shape{EdgeFit: 0.9}, Measured: true}
	}
	gap := Shape{EdgeSpeechMs: maxEdgeSpeechMs + 1, EdgeFit: 0.9}
	if tm, v, err := Settle(new(audio(audioGate-0.01, gap)), false, AtIdentity); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
		t.Errorf("below audioGate: %#v %v %v, want Identity/Unverified, the shift not delivered", tm, v, err)
	}
	below := audio(audioGate-0.01, Shape{EdgeFit: 0.9})
	below.IdentityShape = gap
	if _, _, err := Settle(&below, false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
		t.Errorf("below audioGate with Identity uncovering a scene: %v, want ErrDifferentCut", err)
	}
	if _, _, err := Settle(new(audio(audioGate, gap)), false, AtIdentity); !errors.Is(err, ErrDifferentCut) {
		t.Errorf("at audioGate with a gap: %v, want ErrDifferentCut", err)
	}
	if tm, v, err := Settle(new(audio(audioGate, Shape{EdgeFit: 0.9})), false, AtIdentity); err != nil || tm != (timing.Shift{Ms: 900}) || v != timing.Verified {
		t.Errorf("at audioGate: %#v %v %v, want the shift Verified", tm, v, err)
	}
	unmeasured := Result{Timing: timing.Shift{Ms: 900}, Method: timing.Audio, Confidence: audioGate}
	if tm, v, err := Settle(&unmeasured, false, AtIdentity); err != nil || tm != (timing.Shift{Ms: 900}) || v != timing.Verified {
		t.Errorf("no reference: %#v %v %v, want the shift Verified", tm, v, err)
	}
}

func TestInteriorSceneGuarded(t *testing.T) {
	t.Parallel()
	for _, lenMs := range []int64{7_000, 4_000} {
		t.Run(fmt.Sprintf("%d ms lines every 7 s", lenMs), func(t *testing.T) {
			t.Parallel()
			video := without(episode(t, 12, 260), 700_000, 790_000)
			for at := int64(700_000); at+lenMs <= 790_000; at += 7_000 {
				video = append(video, Span{at, at + lenMs})
			}
			guardCut(t, cutOf("interior", sortSpans(video), 700_000, 90_000), false)
		})
	}
}

func TestInsertedSceneRefused(t *testing.T) {
	t.Parallel()
	base := episode(t, 13, 260)
	// The video holds a 120 s scene at 10 minutes with under 30 s of dialogue
	// that the subtitle's cut lacks: placing it would insert time.
	video := insert(base, 600_000, 120_000, []Span{{0, 4_000}, {30_000, 36_000}, {70_000, 78_000}, {100_000, 106_000}})
	guardCut(t, cutCase{"inserted", video, base, truthOf(base, cutTruth(600_000, 120_000))}, true)
}

func TestThreeBreaksGuarded(t *testing.T) {
	t.Parallel()
	video := episode(t, 14, 300)
	inc := video
	var ats []int64
	for _, p := range []float64{0.25, 0.5, 0.75} {
		at := int64(p * float64(end(video)))
		ats = append(ats, at)
		inc = insert(inc, at, 120_000, nil)
	}
	guardCut(t, cutCase{"three breaks", video, inc, truthOf(inc, func(ms int64) int64 {
		v := ms
		for _, at := range ats {
			if ms >= at+120_000 {
				v -= 120_000
			}
		}
		return v
	})}, false)
}
