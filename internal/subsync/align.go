package subsync

import (
	"context"
	"slices"
)

// sweeper is alass's align_constant_delta over the offsets [-window, window]
// ms: each span pair's rating, its score times the pair's overlap at the
// offset, is a trapezoid written as four slope changes into a dense array
// that two running sums integrate, reused by every sweep of an alignment.
// Scores are whole numbers of scoreUnit, so every sum below 2^53 is exact in
// any order: a fractional score leaves residue (5.6e-17) where the true
// rating is 0, which outranks an offset that truly ties.
type sweeper struct {
	deltas []float64
	window int64
	// A pair's slope changes before the window still shape the rating inside
	// it: they enter as the sweep's starting slope and rating.
	slope0, rating0 float64
}

// scoreUnit is a score of 1 in spanScore's fixed point; 2^-16 resolves
// scores far finer than any threshold reads them.
const scoreUnit = 1 << 16

func newSweeper(window int64) *sweeper {
	return &sweeper{window: window, deltas: make([]float64, 2*window+1)}
}

// best returns the offset in [-window, window] with the highest rating and
// that rating in score-milliseconds; the lowest such offset on a tie, and 0
// with rating 0 when no offset rates above 0. ref and inc are sorted by
// StartMs.
func (w *sweeper) best(ctx context.Context, ref, inc []Span) (offset int64, rating float64, err error) {
	clear(w.deltas)
	w.slope0, w.rating0 = 0, 0
	maxInc := maxDuration(inc)
	for i, r := range ref {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		w.addPairs(r, inc, maxInc)
	}
	slope, cur, top := w.slope0, w.rating0, 0.0
	for i, d := range w.deltas {
		cur += slope
		slope += d
		if cur > top {
			top, offset = cur, int64(i)-w.window
		}
	}
	return offset, top / scoreUnit, nil
}

// addPairs writes the slope changes of r paired with every span of inc
// whose rating can reach the window; maxInc is inc's longest span.
func (w *sweeper) addPairs(r Span, inc []Span, maxInc int64) {
	// A pair rates above 0 only for offsets in (r.StartMs-s.EndMs,
	// r.EndMs-s.StartMs).
	lo, _ := slices.BinarySearchFunc(inc, r.StartMs-w.window-maxInc, byStart)
	for _, s := range inc[lo:] {
		if s.StartMs > r.EndMs+w.window {
			return
		}
		score := float64(spanScore(r, s))
		if score == 0 || r.StartMs-s.EndMs > w.window || r.EndMs-s.StartMs < -w.window {
			continue
		}
		w.add(r.StartMs-s.EndMs, score)
		w.add(r.EndMs-s.EndMs, -score)
		w.add(r.StartMs-s.StartMs, -score)
		w.add(r.EndMs-s.StartMs, score)
	}
}

// add changes the rating's slope by v from offset at on.
func (w *sweeper) add(at int64, v float64) {
	switch i := at + w.window; {
	case i < 0:
		w.slope0 += v
		w.rating0 -= v * float64(i+1)
	case i < int64(len(w.deltas)):
		w.deltas[i] += v
	}
}

func byStart(s Span, start int64) int {
	switch {
	case s.StartMs < start:
		return -1
	case s.StartMs > start:
		return 1
	}
	return 0
}

func maxDuration(spans []Span) int64 {
	var d int64
	for _, s := range spans {
		d = max(d, s.EndMs-s.StartMs)
	}
	return d
}

// spanScore is the overlap quality of a pair in scoreUnit: the shorter
// length over the longer, rounded down, 0 for an empty or inverted span.
func spanScore(r, s Span) int64 {
	rLen, sLen := r.EndMs-r.StartMs, s.EndMs-s.StartMs
	if rLen <= 0 || sLen <= 0 {
		return 0
	}
	return min(rLen, sLen) * scoreUnit / max(rLen, sLen)
}
