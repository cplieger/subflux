package subsync

import (
	"context"
	"slices"
)

// sweeper is alass's align_constant_delta over the offsets [-window, window]
// ms: each span pair's rating, its score times the pair's overlap at the
// offset, is a trapezoid written as four slope changes into a dense array
// that two running sums integrate. One sweeper's array is reused by every
// sweep of an alignment.
type sweeper struct {
	deltas []float64
	window int64
	// A pair's slope changes before the window still shape the rating inside
	// it: they enter as the sweep's starting slope and rating.
	slope0, rating0 float64
}

func newSweeper(window int64) *sweeper {
	return &sweeper{window: window, deltas: make([]float64, 2*window+1)}
}

// best returns the offset in [-window, window] with the highest rating and
// that rating; the lowest such offset on a tie, and 0 with rating 0 when no
// offset rates above 0. ref and inc are sorted by StartMs.
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
	slope, cur := w.slope0, w.rating0
	for i, d := range w.deltas {
		cur += slope
		slope += d
		if cur > rating {
			rating, offset = cur, int64(i)-w.window
		}
	}
	return offset, rating, nil
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
		score := spanScore(r, s)
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

// spanScore is the overlap quality of a pair: the shorter length over the
// longer, 0 for an empty or inverted span.
func spanScore(r, s Span) float64 {
	rLen := float64(r.EndMs - r.StartMs)
	sLen := float64(s.EndMs - s.StartMs)
	if rLen <= 0 || sLen <= 0 {
		return 0
	}
	return min(rLen, sLen) / max(rLen, sLen)
}
