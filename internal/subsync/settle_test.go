package subsync

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// cutCase is a candidate for another cut of video, and how it is placed.
type cutCase struct {
	name       string
	video, inc []Span
}

// thinLevels are the global e2 the reference is thinned to: none, about
// half, just under the gate, and at it.
var thinLevels = []float64{0, 0.5, 0.59, 0.6}

// settleThinned aligns c's candidate to its video thinned to each level and
// settles it unforced.
func settleThinned(t *testing.T, c cutCase, upTo Model, check func(t *testing.T, level float64, r Result, tm timing.Timing, v timing.Verdict, err error)) {
	t.Helper()
	whole, err := AlignReference(t.Context(), c.video, c.inc, upTo)
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range thinLevels {
		ref := c.video
		if level < 0.6 || whole.Confidence < 0.6 {
			ref = thinned(c.video, c.inc, whole.Timing, level)
		}
		r, err := AlignReference(t.Context(), ref, c.inc, upTo)
		if err != nil {
			t.Fatal(err)
		}
		tm, v, err := Settle(r, false)
		t.Run(fmt.Sprintf("e2 %.2f", level), func(t *testing.T) { check(t, level, r, tm, v, err) })
	}
}

func refused(t *testing.T, level float64, r Result, tm timing.Timing, v timing.Verdict, err error) {
	t.Helper()
	if !errors.Is(err, ErrDifferentCut) || tm != nil || v != 0 {
		t.Errorf("thinned to %.2f: e2 %.3f shape %+v settled %#v %v %v, want ErrDifferentCut alone", level, r.Confidence, r.Shape, tm, v, err)
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
			cutCase{kind + " missing a 90 s opening", video, cut(video, 0, 90_000)},
			cutCase{kind + " missing a 90 s ending", video, cut(video, end(video)-90_000, 90_000)},
			cutCase{kind + " missing a 90 s scene at 8 minutes", video, cut(video, 480_000, 90_000)},
		)
		for _, l := range []int64{20_000, 60_000} {
			for _, p := range []float64{0.08, 0.92} {
				at := int64(p * float64(end(video)))
				cases = append(cases, cutCase{fmt.Sprintf("%s missing %d s at %.0f%%", kind, l/1000, p*100), video, cut(video, at, l)})
			}
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			settleThinned(t, c, MaxModel, refused)
		})
	}
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
	tm, v, err := Settle(r, true)
	if err != nil || tm != (timing.Shift{Ms: 4_200}) || v != timing.Verified {
		t.Errorf("forced: %#v %v %v (shape %+v), want Shift{4200} Verified", tm, v, err, r.Shape)
	}
	if _, _, err := Settle(r, false); !errors.Is(err, ErrDifferentCut) {
		t.Errorf("the same fit unforced: %v, want ErrDifferentCut (its gaps hold the other 90%%)", err)
	}
}

func TestMirrorCasesRefused(t *testing.T) {
	t.Parallel()
	var cases []cutCase
	for kind, video := range dialogueKinds(t, 8, 260) {
		extra := dialogueKinds(t, 9, 60)[kind]
		for _, l := range []int64{20_000, 60_000} {
			for _, p := range []float64{0.08, 0.92} {
				at := int64(p * float64(end(video)))
				cases = append(cases, cutCase{fmt.Sprintf("%s holding %d s more at %.0f%%", kind, l/1000, p*100), video, insert(video, at, l, extra)})
			}
		}
	}
	// The round-4 reproduction: the last 32 sparse cues 20 s late.
	video := episode(t, 8, 260)
	late := slices.Clone(video)
	for i := len(late) - 32; i < len(late); i++ {
		late[i] = Span{late[i].StartMs + 20_000, late[i].EndMs + 20_000}
	}
	cases = append(cases, cutCase{"32 sparse tail cues 20 s late", video, late})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			settleThinned(t, c, MaxModel, refused)
		})
	}
	t.Run("32 tail lines 20 s late inside the video's dialogue", func(t *testing.T) {
		t.Parallel()
		video := episode(t, 15, 300)
		// No line past the video's end and none far from dialogue: only the
		// end fit sees it.
		var inc []Span
		tail := len(video) - 32
		for i, s := range video {
			switch {
			case i < tail:
				inc = append(inc, s)
			case s.EndMs+20_000 <= end(video):
				inc = append(inc, Span{s.StartMs + 20_000, s.EndMs + 20_000})
			}
		}
		r, err := AlignReference(t.Context(), video, inc, MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		if r.Confidence < referenceGate || uncovered(r.Shape, false) {
			t.Fatalf("fixture: e2 %.3f shape %+v, want the gate passed and the coverage guard clear", r.Confidence, r.Shape)
		}
		refused(t, r.Confidence, r, nil, 0, settleErr(r))
	})
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
	if tm, v, err := Settle(r, false); err != nil || tm != (timing.Shift{Ms: 1_700}) || v != timing.Verified {
		t.Errorf("advertising lines at 0:00:01 and after the last dialogue: %#v %v %v (shape %+v)", tm, v, err, r.Shape)
	}
}

func TestBelowGateExtraMaterialRefused(t *testing.T) {
	t.Parallel()
	video := dense(t, 10, 600)
	extra := dense(t, 11, 60)
	one := insert(video, end(video)/2, 60_000, extra)
	two := insert(insert(video, end(video)*3/10, 60_000, extra), end(video)*7/10+60_000, 60_000, extra)
	for name, inc := range map[string][]Span{"one 60 s stretch at 50%": one, "two at 30% and 70%": two} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, err := AlignReference(t.Context(), video, shiftSpans(inc, -2_000), MaxModel)
			if err != nil {
				t.Fatal(err)
			}
			if r.Confidence < 0.1 || r.Confidence >= referenceGate {
				t.Fatalf("fixture: global e2 %.3f, want a poor fit below the gate", r.Confidence)
			}
			refused(t, r.Confidence, r, nil, 0, settleErr(r))
		})
	}
	t.Run("a uniformly poor fit stays unverified", func(t *testing.T) {
		t.Parallel()
		inc := shiftSpans(video, -2_000)
		ref := thinned(video, inc, timing.Shift{Ms: 2_000}, 0.4)
		r, err := AlignReference(t.Context(), ref, inc, MaxModel)
		if err != nil {
			t.Fatal(err)
		}
		if tm, v, err := Settle(r, false); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("e2 %.3f shape %+v: %#v %v %v, want Identity/Unverified", r.Confidence, r.Shape, tm, v, err)
		}
	})
}

func settleErr(r Result) error {
	_, _, err := Settle(r, false)
	return err
}

func TestSettlePerGround(t *testing.T) {
	t.Parallel()
	clean := Shape{EdgeFit: 0.9}
	over := map[string]Shape{
		"InsertedMs":   {InsertedMs: maxRiseMs + 1, EdgeFit: 0.9},
		"GapSpeechMs":  {GapSpeechMs: maxGapSpeechMs + 1, EdgeFit: 0.9},
		"EdgeSpeechMs": {EdgeSpeechMs: maxEdgeSpeechMs + 1, EdgeFit: 0.9},
		"ExtraLinesMs": {ExtraLinesMs: maxExtraLinesMs + 1, EdgeFit: 0.9},
	}
	at := map[string]Shape{
		"InsertedMs":   {InsertedMs: maxRiseMs, EdgeFit: 0.9},
		"GapSpeechMs":  {GapSpeechMs: maxGapSpeechMs, EdgeFit: 0.9},
		"EdgeSpeechMs": {EdgeSpeechMs: maxEdgeSpeechMs, EdgeFit: 0.9},
		"ExtraLinesMs": {ExtraLinesMs: maxExtraLinesMs, EdgeFit: 0.9},
	}
	fit := func(conf float64, sh Shape) Result {
		return Result{Timing: timing.Shift{Ms: 500}, Method: timing.MethodReference, Confidence: conf, Shape: sh, Measured: true}
	}
	for ground, sh := range over {
		for _, conf := range []float64{0.2, referenceGate} {
			t.Run(fmt.Sprintf("%s over at %.1f", ground, conf), func(t *testing.T) {
				t.Parallel()
				if _, _, err := Settle(fit(conf, sh), false); !errors.Is(err, ErrDifferentCut) {
					t.Errorf("err = %v, want ErrDifferentCut", err)
				}
				_, _, err := Settle(fit(conf, sh), true)
				if wantRefused := ground == "InsertedMs"; errors.Is(err, ErrDifferentCut) != wantRefused {
					t.Errorf("forced: err = %v, want refused %v", err, wantRefused)
				}
			})
		}
		t.Run(ground+" at its bound", func(t *testing.T) {
			t.Parallel()
			if tm, v, err := Settle(fit(referenceGate, at[ground]), false); err != nil || tm != (timing.Shift{Ms: 500}) || v != timing.Verified {
				t.Errorf("%#v %v %v, want the fit Verified", tm, v, err)
			}
		})
	}
	t.Run("EdgeFit below edgeGate refuses once the gate passes", func(t *testing.T) {
		t.Parallel()
		low := Shape{EdgeFit: edgeGate - 0.01}
		if _, _, err := Settle(fit(referenceGate, low), false); !errors.Is(err, ErrDifferentCut) {
			t.Errorf("at the gate: err = %v, want ErrDifferentCut", err)
		}
		if tm, v, err := Settle(fit(referenceGate-0.01, low), false); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("below the gate: %#v %v %v, want Identity/Unverified", tm, v, err)
		}
	})
	t.Run("below the gate is Identity and Unverified", func(t *testing.T) {
		t.Parallel()
		if tm, v, err := Settle(fit(referenceGate-0.01, clean), false); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("%#v %v %v", tm, v, err)
		}
	})
	t.Run("ModelNone is Identity and Unverified", func(t *testing.T) {
		t.Parallel()
		if tm, v, err := Settle(noReference, false); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
			t.Errorf("%#v %v %v", tm, v, err)
		}
	})
}

func TestAudioSettledAtItsGate(t *testing.T) {
	t.Parallel()
	audio := func(conf float64, sh Shape) Result {
		return Result{Timing: timing.Shift{Ms: 900}, Method: timing.Audio, Confidence: conf, Shape: sh, Measured: true}
	}
	gap := Shape{GapSpeechMs: maxGapSpeechMs + 1, EdgeFit: 0.9}
	if tm, v, err := Settle(audio(audioGate-0.01, gap), false); err != nil || tm != (timing.Identity{}) || v != timing.Unverified {
		t.Errorf("below audioGate: %#v %v %v, want Identity/Unverified, the guard not read", tm, v, err)
	}
	if _, _, err := Settle(audio(audioGate, gap), false); !errors.Is(err, ErrDifferentCut) {
		t.Errorf("at audioGate with a gap: %v, want ErrDifferentCut", err)
	}
	if tm, v, err := Settle(audio(audioGate, Shape{EdgeFit: 0.9}), false); err != nil || tm != (timing.Shift{Ms: 900}) || v != timing.Verified {
		t.Errorf("at audioGate: %#v %v %v, want the shift Verified", tm, v, err)
	}
	unmeasured := Result{Timing: timing.Shift{Ms: 900}, Method: timing.Audio, Confidence: audioGate}
	if tm, v, err := Settle(unmeasured, false); err != nil || tm != (timing.Shift{Ms: 900}) || v != timing.Verified {
		t.Errorf("no reference: %#v %v %v, want the shift Verified", tm, v, err)
	}
}

func TestInteriorSceneRefused(t *testing.T) {
	t.Parallel()
	for _, lenMs := range []int64{7_000, 4_000} {
		t.Run(fmt.Sprintf("%d ms lines every 7 s", lenMs), func(t *testing.T) {
			t.Parallel()
			video := without(episode(t, 12, 260), 700_000, 790_000)
			for at := int64(700_000); at+lenMs <= 790_000; at += 7_000 {
				video = append(video, Span{at, at + lenMs})
			}
			video = sortSpans(video)
			settleThinned(t, cutCase{"interior", video, cut(video, 700_000, 90_000)}, MaxModel, refused)
		})
	}
}

func TestInsertedSceneRefused(t *testing.T) {
	t.Parallel()
	base := episode(t, 13, 260)
	// The video holds a 120 s scene at 10 minutes with under 30 s of dialogue
	// that the subtitle's cut lacks.
	video := insert(base, 600_000, 120_000, []Span{{0, 4_000}, {30_000, 36_000}, {70_000, 78_000}, {100_000, 106_000}})
	settleThinned(t, cutCase{"inserted", video, base}, MaxModel, refused)
}

func TestThreeBreaksRefusedAtMaxModel(t *testing.T) {
	t.Parallel()
	video := episode(t, 14, 300)
	inc := video
	for _, p := range []float64{0.25, 0.5, 0.75} {
		inc = insert(inc, int64(p*float64(end(video))), 120_000, nil)
	}
	r, err := AlignReference(t.Context(), video, inc, MaxModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Timing.(timing.Piecewise); ok {
		t.Fatalf("MaxModel fit %#v", r.Timing)
	}
	refused(t, r.Confidence, r, nil, 0, settleErr(r))
}
