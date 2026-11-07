package subsync

import (
	"math"
	"slices"
	"sort"
)

// The cut study's monoDP and refGapSpeech (cut-study/zz_cut_mono_test.go,
// lines 40 and 403), ported from the study's cue type to Span: the oracle the
// production DP must equal. monoDP indexes drop runs by absolute end time,
// so its memory grows with cue time; its 10 ms refinement and reorder count
// are left out, since the production DP's own refinement is tested apart.

type monoParams struct {
	Step      int64
	Penalty   float64
	DropEnter float64
	DropLeave float64
	Lambda    float64
	UpMs      int64
	Overlap   float64
	Onset     float64
	OnsetMs   int64
}

// productionMono is monoDP configured as the production DP.
func productionMono(penalty float64) monoParams {
	return monoParams{
		Step: gridMs, Penalty: penalty, DropEnter: dropEnter, DropLeave: dropLeave, Lambda: lambda,
		UpMs: maxRiseMs, Overlap: 1, Onset: onsetWeight, OnsetMs: onsetMs,
	}
}

// monoDP returns each cue's grid offset in [lo, hi] or droppedOffset, in
// inc's order, and the objective.
func monoDP(ref, inc []Span, lo, hi int64, p monoParams) ([]int64, float64) {
	rs := ref
	n := len(inc)
	step := p.Step
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return inc[order[a]].StartMs < inc[order[b]].StartMs })
	D := int((hi-lo)/step) + 1
	hi = lo + int64(D-1)*step
	up := int(p.UpMs / step)
	pLo := inc[order[0]].StartMs + lo - step
	var maxEnd int64
	for _, c := range inc {
		maxEnd = max(maxEnd, c.EndMs)
	}
	NP := int((maxEnd+hi-pLo)/step) + 2
	fen := newMaxFenwick(NP)
	entry := make([]int64, NP)
	main := make([]float64, D)
	nmain := make([]float64, D)
	A := make([]float64, D)
	W := make([]float64, D)
	v := make([]float64, D)
	dq := make([]int32, 0, D)
	mainFrom := make([][]int64, n)
	const fromStay, fromStart = -1, -2
	var prevEnd int64 = math.MinInt64
	pIdx := func(x int64) int {
		return min(max(int((x-pLo)/step), 1), NP-1)
	}
	for t, i := range order {
		s := inc[i]
		chn := monoRatings(rs, s, lo, hi, D, p, A, W, v)
		mf := make([]int64, D)
		if t == 0 {
			for k := range D {
				nmain[k], mf[k] = v[k]-chn, fromStart
			}
		} else {
			gk := int(max(s.StartMs-prevEnd, 0) / step)
			dq = dq[:0]
			next := 0
			for k := range D {
				for ; next <= min(k+gk, D-1); next++ {
					for len(dq) > 0 && main[dq[len(dq)-1]] <= main[next] {
						dq = dq[:len(dq)-1]
					}
					dq = append(dq, int32(next))
				}
				for len(dq) > 0 && int(dq[0]) < k-up {
					dq = dq[1:]
				}
				best, from := main[k], int64(fromStay)
				if len(dq) > 0 && int(dq[0]) != k && main[dq[0]]-p.Penalty > best {
					best, from = main[dq[0]]-p.Penalty, int64(dq[0])
				}
				if val, at := fen.prefix(pIdx(s.StartMs + lo + int64(k)*step)); at >= 0 {
					if lv := val - p.Penalty*p.DropLeave; lv > best {
						best, from = lv, -10-(entry[at]+1)
					}
				}
				nmain[k], mf[k] = v[k]-chn+best, from
			}
		}
		if t == 0 {
			fen.update(0, -p.Penalty*p.DropEnter)
			entry[0] = -1
		} else {
			for k := range D {
				at := pIdx(prevEnd + lo + int64(k)*step)
				if fen.update(at, main[k]-p.Penalty*p.DropEnter) {
					entry[at] = int64(t-1)*int64(D) + int64(k)
				}
			}
		}
		mainFrom[t] = mf
		main, nmain = nmain, main
		prevEnd = max(prevEnd, s.EndMs)
	}
	k, obj := 0, math.Inf(-1)
	for j := range D {
		if main[j] > obj {
			k, obj = j, main[j]
		}
	}
	endVal, endAt := fen.prefix(NP - 1)
	offs := make([]int64, n)
	t := n - 1
	if endAt >= 0 && endVal > obj {
		obj = endVal
		e := entry[endAt]
		et := -1
		if e >= 0 {
			et, k = int(e/int64(D)), int(e%int64(D))
		}
		for ; t > et; t-- {
			offs[order[t]] = droppedOffset
		}
	}
	for t >= 0 {
		offs[order[t]] = lo + int64(k)*step
		f := mainFrom[t][k]
		switch {
		case f <= -10:
			e := -10 - f - 1
			et := -1
			if e >= 0 {
				et, k = int(e/int64(D)), int(e%int64(D))
			}
			for t--; t > et; t-- {
				offs[order[t]] = droppedOffset
			}
			continue
		case f >= 0:
			k = int(f)
		}
		t--
	}
	return offs, obj
}

// monoRatings fills v with s's rating at each of the D offsets from lo and
// returns its chance level, using A and W as scratch.
func monoRatings(rs []Span, s Span, lo, hi int64, D int, p monoParams, A, W, v []float64) float64 {
	step := p.Step
	clear(A)
	clear(W)
	if sl := s.EndMs - s.StartMs; sl > 0 {
		tau := p.OnsetMs
		for _, r := range rs {
			a, d := r.StartMs-s.EndMs, r.EndMs-s.StartMs
			if min(d, r.StartMs-s.StartMs+tau) <= lo || max(a, r.StartMs-s.StartMs-tau) >= hi {
				continue
			}
			w := p.Overlap / float64(max(r.EndMs-r.StartMs, sl))
			o, b := r.StartMs-s.StartMs, p.Onset/float64(max(tau, 1))
			for _, q := range [7]struct {
				x int64
				w float64
			}{{a, w}, {r.StartMs - s.StartMs, -w}, {r.EndMs - s.EndMs, -w}, {d, w}, {o - tau, b}, {o, -2 * b}, {o + tau, b}} {
				if q.w == 0 {
					continue
				}
				k0 := int64(0)
				if q.x > lo {
					k0 = (q.x - lo + step - 1) / step
				}
				if k0 >= int64(D) {
					continue
				}
				xk := lo + k0*step
				A[k0] += q.w*float64(xk-q.x) - q.w*float64(step)*float64(k0)
				W[k0] += q.w * float64(step)
			}
		}
	}
	var sa, sw, mean float64
	for k := range D {
		sa += A[k]
		sw += W[k]
		v[k] = sa + sw*float64(k)
		mean += v[k]
	}
	return p.Lambda * (mean / float64(D))
}

// maxFenwick is a prefix-maximum Fenwick tree whose values only grow.
type maxFenwick struct {
	val []float64
	at  []int
	pt  []float64
}

func newMaxFenwick(n int) *maxFenwick {
	f := &maxFenwick{val: make([]float64, n+1), at: make([]int, n+1), pt: make([]float64, n)}
	for i := range f.val {
		f.val[i], f.at[i] = math.Inf(-1), -1
	}
	for i := range f.pt {
		f.pt[i] = math.Inf(-1)
	}
	return f
}

func (f *maxFenwick) update(i int, x float64) bool {
	if x <= f.pt[i] {
		return false
	}
	f.pt[i] = x
	for j := i + 1; j < len(f.val); j += j & -j {
		if x > f.val[j] {
			f.val[j], f.at[j] = x, i
		}
	}
	return true
}

func (f *maxFenwick) prefix(i int) (float64, int) {
	best, at := math.Inf(-1), -1
	for j := i + 1; j > 0; j -= j & -j {
		if f.val[j] > best {
			best, at = f.val[j], f.at[j]
		}
	}
	return best, at
}

// refGapSpeech is the largest amount of reference speech inside one gap
// between consecutive kept cues.
func refGapSpeech(ref, kept []Span) int64 {
	cs := slices.Clone(kept)
	if len(cs) < 2 {
		return 0
	}
	slices.SortFunc(cs, func(a, b Span) int { return int(a.StartMs - b.StartMs) })
	var maxMs int64
	end := cs[0].EndMs
	for i := 1; i < len(cs); i++ {
		gs, ge := end, cs[i].StartMs
		if ge > gs {
			var sp int64
			for _, r := range ref {
				if s, e := max(r.StartMs, gs), min(r.EndMs, ge); e > s {
					sp += e - s
				}
			}
			maxMs = max(maxMs, sp)
		}
		end = max(end, cs[i].EndMs)
	}
	return maxMs
}
