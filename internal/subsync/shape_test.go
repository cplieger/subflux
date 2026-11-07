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
	for _, lenMs := range []int64{7_000, 4_000} {
		name := "GapSpeechMs counts a missing scene"
		if lenMs < 7_000 {
			name += " broken by 3 s pauses"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ref := without(video, 400_000, 490_000)
			for at := int64(400_000); at+lenMs <= 490_000; at += 7_000 {
				ref = append(ref, Span{at, at + lenMs})
			}
			kept := without(video, 400_000, 490_000)
			want := speechIn(ref, 400_000, 490_000)
			if _, sh := shapeOf(ref, kept); sh.GapSpeechMs != want || want < 30_000 {
				t.Errorf("GapSpeechMs = %d, want the scene's %d", sh.GapSpeechMs, want)
			}
		})
	}
	t.Run("GapSpeechMs starts a gap at the furthest end so far", func(t *testing.T) {
		t.Parallel()
		ref := []Span{{0, 2_000}, {10_000, 20_000}, {32_000, 39_000}, {45_000, 46_000}}
		kept := []Span{{0, 30_000}, {5_000, 6_000}, {40_000, 41_000}}
		if _, sh := shapeOf(ref, kept); sh.GapSpeechMs != 7_000 {
			t.Errorf("GapSpeechMs = %d, want 7,000 (the 30 s cue covers the speech at 10 s)", sh.GapSpeechMs)
		}
	})
	t.Run("GapSpeechMs reads only interior gaps", func(t *testing.T) {
		t.Parallel()
		if _, sh := shapeOf(video, video); sh.GapSpeechMs != 0 {
			t.Errorf("exact fit: GapSpeechMs = %d, want 0", sh.GapSpeechMs)
		}
		if _, sh := shapeOf(video, without(video, 0, 90_000)); sh.GapSpeechMs != 0 {
			t.Errorf("missing opening: GapSpeechMs = %d, want 0 (an edge, not a gap)", sh.GapSpeechMs)
		}
	})
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
	t.Run("ExtraLinesMs on a 60 s tail past the last dialogue", func(t *testing.T) {
		t.Parallel()
		end := video[len(video)-1].EndMs
		tail := []Span{{end + 10_000, end + 25_000}, {end + 30_000, end + 50_000}, {end + 55_000, end + 70_000}}
		if _, sh := shapeOf(video, append(slices.Clone(video), tail...)); sh.ExtraLinesMs != 60_000 {
			t.Errorf("ExtraLinesMs = %d, want 60,000", sh.ExtraLinesMs)
		}
	})
	t.Run("ExtraLinesMs on one advertising line", func(t *testing.T) {
		t.Parallel()
		end := video[len(video)-1].EndMs
		kept := append(slices.Clone(video), Span{end + 30_000, end + 33_500})
		if _, sh := shapeOf(video, kept); sh.ExtraLinesMs != 3_500 {
			t.Errorf("ExtraLinesMs = %d, want 3,500", sh.ExtraLinesMs)
		}
		if _, sh := shapeOf(video, video); sh.ExtraLinesMs != 0 {
			t.Errorf("exact fit: ExtraLinesMs = %d, want 0", sh.ExtraLinesMs)
		}
		near := append(slices.Clone(video), Span{end + 2_000, end + 3_500})
		if _, sh := shapeOf(video, near); sh.ExtraLinesMs != 0 {
			t.Errorf("a line 2 s past the last dialogue: ExtraLinesMs = %d, want 0 (within maxRiseMs)", sh.ExtraLinesMs)
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
}
