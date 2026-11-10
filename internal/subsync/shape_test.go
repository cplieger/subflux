package subsync

import (
	"slices"
	"testing"
)

// shapeOf measures kept cues, already in video time, against ref.
func shapeOf(ref, kept []Span) (float64, Shape) {
	return measure(fit{kept: kept, displayMs: displayMs(kept)}, newRefIndex(ref))
}

// speechIn is the reference dialogue inside [from, to), summed span by span.
func speechIn(ref []Span, from, to int64) int64 {
	var ms int64
	for _, r := range ref {
		if s, e := max(r.StartMs, from), min(r.EndMs, to); e > s {
			ms += e - s
		}
	}
	return ms
}

func without(spans []Span, from, to int64) []Span {
	return slices.DeleteFunc(slices.Clone(spans), func(s Span) bool { return s.EndMs > from && s.StartMs < to })
}

func TestShapeMeasures(t *testing.T) {
	t.Parallel()
	t.Run("e2 corrects for chance", func(t *testing.T) {
		t.Parallel()
		ref := []Span{{0, 1_000}, {3_000, 4_000}} // c = 2,000 / 4,000
		if got, _ := shapeOf(ref, []Span{{0, 1_000}}); got != 1 {
			t.Errorf("a cue on dialogue: e2 %v, want 1", got)
		}
		if got, _ := shapeOf(ref, []Span{{500, 1_500}}); got != 0 {
			t.Errorf("half on dialogue, the chance level: e2 %v, want 0", got)
		}
		if got, _ := shapeOf(ref, []Span{{0, 1_000}, {0, 1_000}, {3_000, 4_000}, {1_000, 2_000}}); got != 0.5 {
			t.Errorf("three quarters of the display time on dialogue: e2 %v, want 0.5", got)
		}
	})
	video := episode(t, 4, 300)
	t.Run("EdgeSpeechMs past maxRiseMs at either end", func(t *testing.T) {
		t.Parallel()
		kept := without(video, 0, 90_000)
		want := speechIn(video, minTime, kept[0].StartMs-maxRiseMs)
		if _, sh := shapeOf(video, kept); sh.EdgeSpeechMs != want || want == 0 {
			t.Errorf("missing opening: EdgeSpeechMs = %d, want %d", sh.EdgeSpeechMs, want)
		}
		end := video[len(video)-1].EndMs
		kept = without(video, end-90_000, end)
		want = speechIn(video, kept[len(kept)-1].EndMs+maxRiseMs, maxTime)
		if _, sh := shapeOf(video, kept); sh.EdgeSpeechMs != want || want == 0 {
			t.Errorf("missing ending: EdgeSpeechMs = %d, want %d", sh.EdgeSpeechMs, want)
		}
		near := []Span{{video[0].StartMs + 2_000, video[0].EndMs + 2_000}}
		near = append(near, video[1:]...)
		if _, sh := shapeOf(video, near); sh.EdgeSpeechMs != 0 {
			t.Errorf("first cue within maxRiseMs: EdgeSpeechMs = %d, want 0", sh.EdgeSpeechMs)
		}
	})
	t.Run("InsertedMs is 0 for a global fit", func(t *testing.T) {
		t.Parallel()
		r, err := AlignReference(t.Context(), video, unmap(video, 25/23.976, 4_000), ModelLinear)
		if err != nil || r.Shape.InsertedMs != 0 {
			t.Errorf("linear fit: InsertedMs = %d, %v, want 0", r.Shape.InsertedMs, err)
		}
	})
	t.Run("EdgeFit reads the worst end run", func(t *testing.T) {
		t.Parallel()
		kept := slices.Clone(video)
		for i := len(kept) - 32; i < len(kept); i++ {
			kept[i] = Span{kept[i].StartMs + 20_000, kept[i].EndMs + 20_000}
		}
		whole, sh := shapeOf(video, kept)
		tail, _ := shapeOf(video, kept[len(kept)-32:])
		if sh.EdgeFit > tail || sh.EdgeFit >= whole {
			t.Errorf("EdgeFit = %v, want at most the late tail's %v and under the whole fit's %v", sh.EdgeFit, tail, whole)
		}
		few := slices.Clone(video[:2*edgeCues-1])
		for i := edgeCues; i < len(few); i++ {
			few[i] = Span{few[i].StartMs + 20_000, few[i].EndMs + 20_000}
		}
		if w, sh := shapeOf(video, few); sh.EdgeFit != w {
			t.Errorf("%d cues: EdgeFit = %v, want the whole fit's %v", len(few), sh.EdgeFit, w)
		}
	})
	t.Run("IdentityShape is the candidate where it lies", func(t *testing.T) {
		t.Parallel()
		late := shiftSpans(video, 62_000)
		r, err := AlignReference(t.Context(), video, late, ModelLinear)
		if err != nil {
			t.Fatal(err)
		}
		_, want := shapeOf(video, late)
		if r.IdentityShape != want || r.Shape == want {
			t.Errorf("IdentityShape %+v, Shape %+v; want IdentityShape %+v, the shifted fit's Shape apart from it", r.IdentityShape, r.Shape, want)
		}
		if want.EdgeSpeechMs == 0 {
			t.Fatalf("fixture: a candidate 62 s late leaves no edge dialogue: %+v", want)
		}
	})
}
