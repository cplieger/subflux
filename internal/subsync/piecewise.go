package subsync

import (
	"context"
	"math"
	"slices"

	"github.com/cplieger/subflux/internal/timing"
)

// grid is the piecewise DP's offset axis: d offsets from lo to hi, step ms
// apart. A kept cue's offset may rise by at most up steps from the previous
// kept cue's; every is the traceback block, in cues.
type grid struct {
	lo, hi, step int64
	d, up, every int
}

func newGrid(window, step int64, every int) grid {
	return grid{lo: -window, hi: window, step: step, d: int(2*window/step) + 1, up: int(maxRiseMs / step), every: every}
}

var productionGrid = newGrid(windowMs, gridMs, checkpointEvery)

// droppedOffset marks a cue the DP dropped.
const droppedOffset = math.MinInt64

// Back-pointer codes; a code at or below leaveBase left a drop run whose
// entry is leaveBase-code-1 (t×d+k of the cue kept before it, -1 for none).
const (
	fromStay  = -1
	fromStart = -2
	leaveBase = -10
)

// dropRing holds the drop runs open at the current cue, by the position
// (on the step grid from pLo) of their last kept cue's end in video time.
// A run's position is at most 2 × hi past the current cue's start once each
// cue's end is clamped to its start plus hi, and every later query reaches
// past lo before it, so positions below base fold into one maximum.
type dropRing struct {
	val       []float64
	entry     []int32
	base      int64
	fold      float64
	foldEntry int32
}

func (r *dropRing) reset() {
	for i := range r.val {
		r.val[i], r.entry[i] = math.Inf(-1), -1
	}
	r.base, r.fold, r.foldEntry = 1, math.Inf(-1), -1
}

func (r *dropRing) copyFrom(o *dropRing) {
	copy(r.val, o.val)
	copy(r.entry, o.entry)
	r.base, r.fold, r.foldEntry = o.base, o.fold, o.foldEntry
}

func (r *dropRing) slot(p int64) int { return int(p % int64(len(r.val))) }

// foldTo folds every position below nb into the running maximum.
func (r *dropRing) foldTo(nb int64) {
	if nb <= r.base {
		return
	}
	from := max(r.base, nb-int64(len(r.val)))
	for p := from; p < nb; p++ {
		i := r.slot(p)
		if r.val[i] > r.fold {
			r.fold, r.foldEntry = r.val[i], r.entry[i]
		}
		r.val[i], r.entry[i] = math.Inf(-1), -1
	}
	r.base = nb
}

// raise records a drop run at position p worth v; a tie keeps the earlier.
func (r *dropRing) raise(p int64, v float64, entry int32) {
	if p < r.base {
		if v > r.fold {
			r.fold, r.foldEntry = v, entry
		}
		return
	}
	if i := r.slot(p); v > r.val[i] {
		r.val[i], r.entry[i] = v, entry
	}
}

// top is the best open drop run.
func (r *dropRing) top() (best float64, entry int32) {
	best, entry = r.fold, r.foldEntry
	for i, v := range r.val {
		if v > best {
			best, entry = v, r.entry[i]
		}
	}
	return best, entry
}

// mark is the DP's state before a traceback block's first cue.
type mark struct {
	main    []float64
	ring    dropRing
	prevEnd int64
}

// dp is the cut study's monoDP over a grid. Its memory depends on the grid
// and the cue count only: one block of back-pointers, recomputed from its
// mark at traceback, and a drop ring instead of an axis of cue times. One
// dp's buffers serve every fit of an alignment.
type dp struct {
	ref                 []Span
	inc                 []Span
	main, next, a, w, v []float64
	dq                  []int
	back                []int32
	marks               []mark
	ring                dropRing
	g                   grid
	penalty             float64
	pLo                 int64
	loaded              int
}

// newDP takes ref sorted by start.
func newDP(g grid, ref []Span) *dp {
	ringLen := int((2*g.hi-g.lo)/g.step) + 2
	return &dp{
		g: g, ref: ref,
		main: make([]float64, g.d), next: make([]float64, g.d),
		a: make([]float64, g.d), w: make([]float64, g.d), v: make([]float64, g.d),
		dq:   make([]int, g.d),
		back: make([]int32, g.every*g.d),
		ring: dropRing{val: make([]float64, ringLen), entry: make([]int32, ringLen)},
	}
}

// run returns each cue's grid offset or droppedOffset, maximising the sum
// over kept cues of the rating less its chance level, less penalty per
// offset change and dropEnter and dropLeave × penalty per drop run. inc is
// sorted by start and holds at least one cue, and len(inc)×d fits an int32,
// as maxSpans cues on the production grid do.
func (p *dp) run(ctx context.Context, inc []Span, penalty float64) (offs []int64, obj float64, err error) {
	g := p.g
	p.inc, p.penalty = inc, penalty
	p.pLo = inc[0].StartMs + g.lo - g.step
	p.ring.reset()
	p.marks = p.marks[:0]
	prevEnd := int64(math.MinInt64)
	for t := range inc {
		if t%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		if t%g.every == 0 {
			p.save(prevEnd)
		}
		prevEnd = p.step(t, prevEnd)
	}
	p.loaded = (len(inc) - 1) / g.every
	offs, obj = p.trace()
	return offs, obj, nil
}

func (p *dp) save(prevEnd int64) {
	b := len(p.marks)
	if b < cap(p.marks) {
		p.marks = p.marks[:b+1]
	} else {
		p.marks = append(p.marks, mark{})
	}
	m := &p.marks[b]
	if m.main == nil {
		m.main = make([]float64, p.g.d)
		m.ring = dropRing{val: make([]float64, len(p.ring.val)), entry: make([]int32, len(p.ring.entry))}
	}
	copy(m.main, p.main)
	m.ring.copyFrom(&p.ring)
	m.prevEnd = prevEnd
}

// step computes cue t's row from cue t-1's, opens the drop runs starting at
// t, and returns the cues' latest end so far, each clamped to its start
// plus hi.
func (p *dp) step(t int, prevEnd int64) int64 {
	g := p.g
	back := p.back[(t%g.every)*g.d : (t%g.every+1)*g.d]
	p.row(t, prevEnd, back)
	if t == 0 {
		p.ring.raise(0, -p.penalty*dropEnter, -1)
	} else {
		for k := range g.d {
			p.ring.raise(p.pos(prevEnd+g.lo+int64(k)*g.step), p.main[k]-p.penalty*dropEnter, g.entry(t-1, k))
		}
	}
	p.main, p.next = p.next, p.main
	s := p.inc[t]
	return max(prevEnd, min(s.EndMs, s.StartMs+g.hi))
}

// entry is the code of cue t kept at offset index k.
func (g grid) entry(t, k int) int32 { return code(t*g.d + k) }

// code narrows a back-pointer or an entry to int32, which run's cue bound
// keeps it inside.
func code(c int) int32 {
	if c < 0 || c > math.MaxInt32 {
		panic("subsync: a DP code past int32; run's cue bound was broken")
	}
	return int32(c)
}

// decode is entry's inverse: -1, before any cue, is cue -1.
func (g grid) decode(entry int32) (t, k int) {
	if entry < 0 {
		return -1, 0
	}
	return int(entry) / g.d, int(entry) % g.d
}

func (p *dp) pos(x int64) int64 { return max((x-p.pLo)/p.g.step, 1) }

func (p *dp) row(t int, prevEnd int64, back []int32) {
	g := p.g
	s := p.inc[t]
	chance := p.ratings(s)
	if t == 0 {
		for k := range g.d {
			p.next[k], back[k] = p.v[k]-chance, fromStart
		}
		return
	}
	gk := int(max(s.StartMs-prevEnd, 0) / g.step)
	p.ring.foldTo(p.pos(s.StartMs + g.lo))
	win := window{dq: p.dq, main: p.main}
	open := ringScan{best: p.ring.fold, entry: p.ring.foldEntry, at: p.ring.base}
	for k := range g.d {
		win.slide(min(k+gk, g.d-1), k-g.up)
		best, from := p.main[k], int32(fromStay)
		if j, ok := win.top(); ok && j != k && p.main[j]-p.penalty > best {
			best, from = p.main[j]-p.penalty, code(j)
		}
		run, entry := open.upTo(&p.ring, p.pos(s.StartMs+g.lo+int64(k)*g.step))
		if lv := run - p.penalty*dropLeave; lv > best {
			best, from = lv, leaveBase-entry-1
		}
		p.next[k], back[k] = p.v[k]-chance+best, from
	}
}

// window is the sliding maximum of main over the offsets a cue may come
// from: a deque of offset indexes whose main values fall from the front.
type window struct {
	dq               []int
	main             []float64
	head, tail, next int
}

// slide admits every offset up to hi and expires those below lo.
func (w *window) slide(hi, lo int) {
	for ; w.next <= hi; w.next++ {
		for w.tail > w.head && w.main[w.dq[w.tail-1]] <= w.main[w.next] {
			w.tail--
		}
		w.dq[w.tail] = w.next
		w.tail++
	}
	for w.tail > w.head && w.dq[w.head] < lo {
		w.head++
	}
}

func (w *window) top() (int, bool) {
	if w.tail == w.head {
		return 0, false
	}
	return w.dq[w.head], true
}

// ringScan is the best drop run at or below a rising position.
type ringScan struct {
	best  float64
	entry int32
	at    int64
}

func (s *ringScan) upTo(r *dropRing, q int64) (best float64, entry int32) {
	for ; s.at <= q; s.at++ {
		if i := r.slot(s.at); r.val[i] > s.best {
			s.best, s.entry = r.val[i], r.entry[i]
		}
	}
	return s.best, s.entry
}

// ratings fills v with s's rating at every grid offset (alass's overlap
// over the longer span, plus the onset kernel) and returns its chance
// level, lambda × the mean over the grid.
func (p *dp) ratings(s Span) float64 {
	g := p.g
	clear(p.a)
	clear(p.w)
	if sl := s.EndMs - s.StartMs; sl > 0 {
		const tau = int64(onsetMs)
		from, _ := slices.BinarySearchFunc(p.ref, s.StartMs+g.lo-tau+1, byStart)
		limit := g.hi + min(s.EndMs, s.StartMs+tau)
		for _, r := range p.ref[from:] {
			if r.StartMs >= limit {
				break
			}
			a, d := r.StartMs-s.EndMs, r.EndMs-s.StartMs
			o := r.StartMs - s.StartMs
			if min(d, o+tau) <= g.lo || max(a, o-tau) >= g.hi {
				continue
			}
			w := 1.0 / float64(max(r.EndMs-r.StartMs, sl))
			b := onsetWeight / float64(max(tau, 1))
			for _, q := range [7]struct {
				x int64
				w float64
			}{{a, w}, {o, -w}, {r.EndMs - s.EndMs, -w}, {d, w}, {o - tau, b}, {o, -2 * b}, {o + tau, b}} {
				p.addBreak(q.x, q.w)
			}
		}
	}
	var sa, sw, mean float64
	for k := range g.d {
		sa += p.a[k]
		sw += p.w[k]
		p.v[k] = sa + sw*float64(k)
		mean += p.v[k]
	}
	return lambda * (mean / float64(g.d))
}

// addBreak adds a slope change of w at offset x.
func (p *dp) addBreak(x int64, w float64) {
	g := p.g
	if w == 0 {
		return
	}
	k0 := int64(0)
	if x > g.lo {
		k0 = (x - g.lo + g.step - 1) / g.step
	}
	if k0 >= int64(g.d) {
		return
	}
	xk := g.lo + k0*g.step
	p.a[k0] += w*float64(xk-x) - w*float64(g.step)*float64(k0)
	p.w[k0] += w * float64(g.step)
}

// trace walks the back-pointers from the best end, replaying each earlier
// block from its mark when the walk enters it.
func (p *dp) trace() (offs []int64, obj float64) {
	g := p.g
	offs = make([]int64, len(p.inc))
	k := 0
	obj = math.Inf(-1)
	for j, v := range p.main[:g.d] {
		if v > obj {
			k, obj = j, v
		}
	}
	t := len(p.inc) - 1
	if endVal, entry := p.ring.top(); endVal > obj {
		obj = endVal
		t, k = g.dropTo(offs, t, entry)
	}
	for t >= 0 {
		offs[t] = g.lo + int64(k)*g.step
		switch f := p.backAt(t, k); {
		case f <= leaveBase:
			t, k = g.dropTo(offs, t-1, leaveBase-f-1)
		case f >= 0:
			t, k = t-1, int(f)
		default:
			t--
		}
	}
	return offs, obj
}

// dropTo marks cues from t down to the drop run's entry as dropped and
// returns the entry's cue and offset index. A run holds at least one cue,
// so its entry precedes t; one that does not would stall the walk.
func (g grid) dropTo(offs []int64, t int, entry int32) (cue, k int) {
	cue, k = g.decode(entry)
	if cue >= t {
		panic("subsync: a drop run entered at or after its last cue")
	}
	for ; t > cue; t-- {
		offs[t] = droppedOffset
	}
	return cue, k
}

func (p *dp) backAt(t, k int) int32 {
	g := p.g
	if b := t / g.every; b != p.loaded {
		p.replay(b)
	}
	return p.back[(t%g.every)*g.d+k]
}

func (p *dp) replay(b int) {
	g := p.g
	m := &p.marks[b]
	copy(p.main, m.main)
	p.ring.copyFrom(&m.ring)
	prevEnd := m.prevEnd
	for t := b * g.every; t < min(len(p.inc), (b+1)*g.every); t++ {
		prevEnd = p.step(t, prevEnd)
	}
	p.loaded = b
}

// fit runs the DP on inc, sorted by start, and refines each run of kept
// cues sharing an offset to refineMs.
func (p *dp) fit(ctx context.Context, inc []Span, penalty float64) ([]int64, error) {
	offs, _, err := p.run(ctx, inc, penalty)
	if err != nil {
		return nil, err
	}
	refine(offs, inc, p.ref, p.g.step)
	return offs, nil
}

// refine moves each run of kept cues sharing a grid offset to the offset
// within step of it, on a refineMs grid, that rates best against ref. A run
// whose grid offset rises at most maxRiseMs over every earlier run's keeps
// that bound after refinement, so refining never inserts time Settle would
// refuse.
func refine(offs []int64, inc, ref []Span, step int64) {
	maxRef := maxDuration(ref)
	gridMin, refinedMin := int64(math.MaxInt64), int64(math.MaxInt64)
	for i := 0; i < len(offs); {
		j := i + 1
		for j < len(offs) && offs[j] == offs[i] {
			j++
		}
		if off := offs[i]; off != droppedOffset {
			ceiling := int64(math.MaxInt64)
			if gridMin != math.MaxInt64 && off-gridMin <= maxRiseMs {
				ceiling = refinedMin + maxRiseMs
			}
			best := bestNear(ref, maxRef, inc[i:j], off, step, ceiling)
			for k := i; k < j; k++ {
				offs[k] = best
			}
			gridMin, refinedMin = min(gridMin, off), min(refinedMin, best)
		}
		i = j
	}
}

// bestNear is the offset in [off-step, min(off+step, ceiling)], on a
// refineMs grid, at which spans rate best against ref; the lowest on a tie.
// ceiling is at least off-step.
func bestNear(ref []Span, maxRef int64, spans []Span, off, step, ceiling int64) int64 {
	best, bestRating := off, -1.0
	for d := off - step; d <= min(off+step, ceiling); d += refineMs {
		if r := appliedRating(ref, maxRef, spans, d); r > bestRating {
			best, bestRating = d, r
		}
	}
	return best
}

// appliedRating is alass's rating of spans shifted by d against ref, sorted
// by start with maxRef its longest span: the sum over overlapping pairs of
// the overlap over the longer span.
func appliedRating(ref []Span, maxRef int64, spans []Span, d int64) float64 {
	var total float64
	for _, s := range spans {
		start, end := s.StartMs+d, s.EndMs+d
		if end <= start {
			continue
		}
		from, _ := slices.BinarySearchFunc(ref, start-maxRef, byStart)
		for _, r := range ref[from:] {
			if r.StartMs >= end {
				break
			}
			if ov := min(r.EndMs, end) - max(r.StartMs, start); ov > 0 {
				total += float64(ov) / float64(max(r.EndMs-r.StartMs, end-start))
			}
		}
	}
	return total
}

// piecewiseOf builds the Timing of per-cue offsets over base, the baseline
// cues sorted by start: a segment starts at the first cue whose offset or
// drop differs from the segment's, unless that cue starts where the segment
// does, when it joins it. ok is false for a mapping Validate refuses, which
// bounds the segment count; the mapping is still returned.
func piecewiseOf(base []Span, offs []int64, scale float64) (timing.Piecewise, bool) {
	var segs []timing.Segment
	for i, s := range base {
		drop, off := offs[i] == droppedOffset, offs[i]
		if drop {
			off = 0
		}
		if n := len(segs); n > 0 && (segs[n-1].Drop == drop && segs[n-1].OffsetMs == off || s.StartMs <= segs[n-1].FromMs) {
			continue
		}
		from := int64(0)
		if n := len(segs); n > 0 {
			from = s.StartMs
			segs[n-1].ToMs = from
		}
		segs = append(segs, timing.Segment{FromMs: from, OffsetMs: off, Drop: drop})
	}
	pw := timing.Piecewise{Segments: segs, Scale: scale}
	return pw, timing.Validate(pw) == nil
}

// segmentOf is the segment holding baseline time ms.
func segmentOf(segs []timing.Segment, ms int64) timing.Segment {
	i, found := slices.BinarySearchFunc(segs, ms, func(s timing.Segment, ms int64) int {
		switch {
		case s.FromMs < ms:
			return -1
		case s.FromMs > ms:
			return 1
		}
		return 0
	})
	if !found {
		i--
	}
	return segs[max(i, 0)]
}

// applyPiecewise maps base's cues under pw as Render does and returns the
// kept ones in video time.
func applyPiecewise(base []Span, pw timing.Piecewise) []Span {
	var kept []Span
	for _, s := range base {
		seg := segmentOf(pw.Segments, s.StartMs)
		if seg.Drop {
			continue
		}
		kept = append(kept, Span{
			int64(math.Round(float64(s.StartMs)*pw.Scale)) + seg.OffsetMs,
			int64(math.Round(float64(s.EndMs)*pw.Scale)) + seg.OffsetMs,
		})
	}
	return kept
}

// insertedMs is Shape.InsertedMs of pw over base, sorted by start: the
// largest rise of a kept cue's offset over the smallest offset of an
// earlier kept cue.
func insertedMs(base []Span, pw timing.Piecewise) int64 {
	var rise int64
	low := int64(math.MaxInt64)
	for _, s := range base {
		seg := segmentOf(pw.Segments, s.StartMs)
		if seg.Drop {
			continue
		}
		if low != math.MaxInt64 {
			rise = max(rise, seg.OffsetMs-low)
		}
		low = min(low, seg.OffsetMs)
	}
	return rise
}
