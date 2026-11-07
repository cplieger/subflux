package subsync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

// bruteRating is the rating at offset o with no window: each pair's score
// times its overlap in ms, summed from the pair's four slope changes.
func bruteRating(ref, inc []Span, o int64) float64 {
	var r float64
	for _, a := range ref {
		for _, b := range inc {
			score := spanScore(a, b)
			if score == 0 {
				continue
			}
			for _, d := range []struct {
				at int64
				v  float64
			}{{a.StartMs - b.EndMs, score}, {a.EndMs - b.EndMs, -score}, {a.StartMs - b.StartMs, -score}, {a.EndMs - b.StartMs, score}} {
				if d.at < o {
					r += d.v * float64(o-d.at)
				}
			}
		}
	}
	return r
}

func sortedSpans(t *rapid.T, label string, n int, maxStart int64) []Span {
	spans := make([]Span, n)
	for i := range spans {
		start := rapid.Int64Range(0, maxStart).Draw(t, label+"_start")
		spans[i] = Span{start, start + rapid.Int64Range(0, 3_000).Draw(t, label+"_dur")}
	}
	slices.SortFunc(spans, func(a, b Span) int { return byStart(a, b.StartMs) })
	return spans
}

func TestSweepMatchesBruteForce(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		window := rapid.Int64Range(1, 400).Draw(t, "window")
		ref := sortedSpans(t, "ref", rapid.IntRange(1, 6).Draw(t, "nref"), 6_000)
		inc := sortedSpans(t, "inc", rapid.IntRange(1, 6).Draw(t, "ninc"), 6_000)
		got, rating, err := newSweeper(window).best(t.Context(), ref, inc)
		if err != nil {
			t.Fatal(err)
		}
		var want int64
		var wantRating float64
		for o := -window; o <= window; o++ {
			if r := bruteRating(ref, inc, o); r > wantRating+1e-9 {
				want, wantRating = o, r
			}
		}
		if got != want || rating < wantRating-1e-6 || rating > wantRating+1e-6 {
			t.Fatalf("best = %d (rating %v), brute force %d (rating %v)", got, rating, want, wantRating)
		}
	})
}

func TestSweepRecoversShift(t *testing.T) {
	t.Parallel()
	ref := []Span{{1_000, 3_000}, {5_000, 6_500}, {9_000, 12_000}, {15_000, 15_800}, {20_000, 24_000}}
	for _, shift := range []int64{-600_000, -7_250, 0, 3_200, 599_999, 600_000} {
		inc := make([]Span, len(ref))
		for i, r := range ref {
			inc[i] = Span{r.StartMs - shift, r.EndMs - shift}
		}
		if got, _, err := newSweeper(windowMs).best(t.Context(), ref, inc); err != nil || got != shift {
			t.Errorf("shift %d: best = %d, %v", shift, got, err)
		}
	}
}

func TestSweepIgnoresOffsetsPastTheWindow(t *testing.T) {
	t.Parallel()
	ref := []Span{{700_000, 702_000}, {705_000, 706_000}}
	inc := []Span{{0, 2_000}, {5_000, 6_000}}
	got, _, err := newSweeper(windowMs).best(t.Context(), ref, inc)
	if err != nil {
		t.Fatal(err)
	}
	if got < -windowMs || got > windowMs || got == 700_000 {
		t.Errorf("best = %d, want an offset inside ±%d", got, windowMs)
	}
}

func TestSweepWithNothingAboveZeroIsZero(t *testing.T) {
	t.Parallel()
	w := newSweeper(50)
	if got, rating, _ := w.best(t.Context(), nil, []Span{{0, 10}}); got != 0 || rating != 0 {
		t.Errorf("empty reference: best = %d, %v, want 0, 0", got, rating)
	}
	if got, rating, _ := w.best(t.Context(), []Span{{0, 10}}, []Span{{5_000, 5_010}}); got != 0 || rating != 0 {
		t.Errorf("no pair inside the window: best = %d, %v, want 0, 0", got, rating)
	}
}

func TestSweepCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := newSweeper(10).best(ctx, []Span{{0, 1}}, []Span{{0, 1}}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestSpanScore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r, s Span
		want float64
	}{
		{"equal length spans", Span{0, 2000}, Span{0, 2000}, 1.0},
		{"ref shorter than inc", Span{0, 1000}, Span{0, 2000}, 0.5},
		{"inc shorter than ref", Span{0, 4000}, Span{0, 1000}, 0.25},
		{"zero-length ref", Span{500, 500}, Span{0, 1000}, 0},
		{"zero-length inc", Span{0, 1000}, Span{500, 500}, 0},
		{"inverted ref", Span{2000, 1000}, Span{0, 1000}, 0},
		{"inverted inc", Span{0, 1000}, Span{2000, 1000}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := spanScore(tt.r, tt.s); got != tt.want {
				t.Errorf("spanScore(%v, %v) = %v, want %v", tt.r, tt.s, got, tt.want)
			}
		})
	}
}

func BenchmarkSweep(b *testing.B) {
	spans := make([]Span, 2_000)
	for i := range spans {
		spans[i] = Span{int64(i) * 3_100, int64(i)*3_100 + 2_000}
	}
	w := newSweeper(windowMs)
	for b.Loop() {
		_, _, _ = w.best(b.Context(), spans, spans)
	}
}
