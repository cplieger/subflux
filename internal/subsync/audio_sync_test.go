package subsync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// speechOf is a stream whose LLR clears both deciders on every frame spans
// cover and neither elsewhere, over durationMs.
func speechOf(spans []Span, durationMs int64) Speech {
	llr := make([]float64, durationMs/frameMs)
	for _, s := range spans {
		for f := max(s.StartMs/frameMs, 0); f < min(s.EndMs/frameMs, int64(len(llr))); f++ {
			llr[f] = 1_000
		}
	}
	return Speech{LLR: llr}
}

func TestAudioCleanShift(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 200)
	dur := video[len(video)-1].EndMs + 30_000
	r, err := AlignAudio(t.Context(), speechOf(video, dur), nil, unmap(video, 1, 2_000), dur)
	if err != nil {
		t.Fatal(err)
	}
	// The deciders' overhang holds speech past each line's end, so the
	// peak sits up to the precise overhang (100 ms) late.
	if s, ok := r.Timing.(timing.Shift); !ok || s.Ms < 2_000 || s.Ms > 2_150 || r.Method != timing.Audio || modelOf(r) != ModelShift {
		t.Errorf("result %+v, want an audio shift of 2 s", r)
	}
	if r.Confidence < audioGate || r.Confidence > 0.9 {
		t.Errorf("confidence %v, want peak × 0.9 at least %v", r.Confidence, audioGate)
	}
	if r.Measured {
		t.Error("no reference: Measured true")
	}
}

func TestAudioFramesPastBoundIgnored(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 200)
	dur := video[len(video)-1].EndMs + 30_000
	bounded := speechOf(video, dur)
	bounded.LLR = append(bounded.LLR, make([]float64, MaxAudioFrames-len(bounded.LLR))...)
	long := Speech{LLR: append(slices.Clone(bounded.LLR), slices.Repeat([]float64{1_000}, 100_000)...)}
	inc := unmap(video, 1, 2_000)
	want, err := AlignAudio(t.Context(), bounded, nil, inc, dur)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AlignAudio(t.Context(), long, nil, inc, dur)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("speech past MaxAudioFrames changed the fit: %+v, want %+v", got, want)
	}
}

func TestAudioOffsetBeyondLimit(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 200)
	dur := video[len(video)-1].EndMs + 30_000
	// 10% of the duration, inside 30 s to 5 minutes.
	limit := dur / 10
	if limit < 30_000 || limit > 300_000 {
		t.Fatalf("fixture: %d ms video, want its 10%% inside the clamp", dur)
	}
	for _, c := range []struct {
		shift int64
		want  Model
	}{{limit - 20_000, ModelShift}, {-(limit - 20_000), ModelShift}, {limit + 20_000, ModelNone}, {-(limit + 20_000), ModelNone}} {
		r, err := AlignAudio(t.Context(), speechOf(video, dur), nil, unmap(video, 1, c.shift), dur)
		if err != nil {
			t.Fatal(err)
		}
		if modelOf(r) != c.want {
			t.Errorf("shift %d: model %d (%+v), want %d", c.shift, modelOf(r), r.Timing, c.want)
		}
		if c.want == ModelNone && r != noFit {
			t.Errorf("shift %d: refused fit %+v, want the MethodNone no-fit result", c.shift, r)
		}
	}
}

func TestAudioShapeMeasuredAgainstReference(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 200)
	dur := video[len(video)-1].EndMs + 30_000
	inc := unmap(without(video, 400_000, 490_000), 1, 2_000)
	r, err := AlignAudio(t.Context(), speechOf(video, dur), video, inc, dur)
	if err != nil {
		t.Fatal(err)
	}
	if _, want := shapeOf(video, shiftSpans(inc, timing.OffsetMs(r.Timing))); !r.Measured || r.Shape != want {
		t.Errorf("result %+v, want measured with the shift's shape %+v against the reference", r, want)
	}
	if _, want := shapeOf(video, inc); r.IdentityShape != want {
		t.Errorf("IdentityShape %+v, want the candidate's own %+v", r.IdentityShape, want)
	}
	if r.Confidence < audioGate {
		t.Errorf("confidence %v: the audio measure stays peak × 0.9, not e2", r.Confidence)
	}
}

func TestAudioPlacingNothingIsNoFit(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 200)
	dur := end(video) + 30_000
	for name, c := range map[string]struct {
		speech Speech
		inc    []Span
	}{
		"no frames":            {Speech{}, video},
		"an offset past limit": {speechOf(video, dur), unmap(video, 1, dur/10+20_000)},
	} {
		r, err := AlignAudio(t.Context(), c.speech, video, c.inc, dur)
		if err != nil || r != noFit {
			t.Errorf("%s against a guarded reference: %+v, %v, want the MethodNone no-fit result", name, r, err)
		}
	}
}

func TestAudioTooLittleInput(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 50)
	dur := video[len(video)-1].EndMs
	for name, c := range map[string]struct {
		speech Speech
		inc    []Span
	}{
		"four spans": {speechOf(video, dur), video[:4]},
		"no frames":  {Speech{}, video},
	} {
		r, err := AlignAudio(t.Context(), c.speech, nil, c.inc, dur)
		if err != nil || r != noFit {
			t.Errorf("%s: %+v, %v, want the MethodNone no-fit result", name, r, err)
		}
	}
	if _, err := AlignAudio(t.Context(), speechOf(video, dur), nil, video, 0); err == nil {
		t.Error("zero duration: no error")
	}
}

func TestAudioCancelled(t *testing.T) {
	t.Parallel()
	video := episode(t, 5, 100)
	dur := video[len(video)-1].EndMs
	for n := range 2 {
		r, err := AlignAudio(&countdown{Context: t.Context(), n: n}, speechOf(video, dur), nil, video, dur)
		if !errors.Is(err, context.Canceled) || r.Timing != nil {
			t.Errorf("cancelled after %d checks: %+v, %v, want only context.Canceled", n, r, err)
		}
	}
}

func TestDialogueSignalOverlaps(t *testing.T) {
	t.Parallel()
	got := dialogueSignal([]Span{{30, 60}, {0, 50}, {40, 45}, {90, 500}}, 10)
	want := []float64{1, 1, 1, 1, 1, 1, -1, -1, -1, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d = %v, want %v (%v)", i, got[i], want[i], got)
		}
	}
}

func TestAudioOffsetLimitClamped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		cues         int
		within, past int64
	}{{"a short video keeps 30 s", 30, 25_000, 40_000}, {"a long video stops at 5 minutes", 700, 280_000, 320_000}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			video := episode(t, 6, c.cues)
			dur := video[len(video)-1].EndMs + 10_000
			for shift, want := range map[int64]Model{c.within: ModelShift, c.past: ModelNone} {
				r, err := AlignAudio(t.Context(), speechOf(video, dur), nil, unmap(video, 1, shift), dur)
				if err != nil || modelOf(r) != want {
					t.Errorf("%d ms video, shift %d: model %d (%+v), %v, want %d", dur, shift, modelOf(r), r.Timing, err, want)
				}
			}
		})
	}
}
