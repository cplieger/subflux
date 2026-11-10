package subsync

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
	"pgregory.net/rapid"
)

// disjointSpans draws n non-overlapping spans in time order.
func disjointSpans(t *rapid.T, label string, n int) []Span {
	spans := make([]Span, n)
	at := rapid.Int64Range(0, 5_000).Draw(t, label+"_first")
	for i := range spans {
		l := rapid.Int64Range(300, 4_000).Draw(t, label+"_len")
		spans[i] = Span{at, at + l}
		at += l + rapid.Int64Range(100, 6_000).Draw(t, label+"_gap")
	}
	return spans
}

// cutCandidate draws a candidate from ref: runs of its cues moved by one
// offset each, some cues missing, unrelated cues among them, and blocks of
// lines ref lacks that push every later cue back by the block's length,
// sorted by start.
func cutCandidate(t *rapid.T, ref []Span, window int64) []Span {
	var inc []Span
	off := int64(0)
	for _, r := range ref {
		if rapid.IntRange(0, 5).Draw(t, "move") == 0 {
			off = rapid.Int64Range(-window/2, window/2).Draw(t, "offset")
		}
		if rapid.IntRange(0, 7).Draw(t, "block") == 0 {
			l := rapid.Int64Range(window/4, window).Draw(t, "block_len")
			for at := r.StartMs - off; at < r.StartMs-off+l-500; at += 600 {
				inc = append(inc, Span{at, at + 400})
			}
			off -= l
		}
		switch rapid.IntRange(0, 7).Draw(t, "fate") {
		case 0:
			continue
		case 1:
			s := rapid.Int64Range(r.StartMs-window, r.StartMs+window).Draw(t, "stray")
			inc = append(inc, Span{s, s + rapid.Int64Range(0, 4_000).Draw(t, "stray_len")})
		}
		inc = append(inc, Span{r.StartMs - off, r.EndMs - off})
	}
	if len(inc) == 0 {
		inc = append(inc, ref[0])
	}
	slices.SortStableFunc(inc, func(a, b Span) int { return byStart(a, b.StartMs) })
	return inc
}

// monoObjective is monoDP's objective of the assignment offs over inc,
// sorted by start, or an error naming a move the DP may not make: a rise
// past UpMs, a fall past the gap before the cue, or a drop run left before
// the last kept cue's end.
func monoObjective(ref, inc []Span, offs []int64, lo, hi int64, p monoParams) (float64, error) {
	step := p.Step
	D := int((hi-lo)/step) + 1
	A, W, v := make([]float64, D), make([]float64, D), make([]float64, D)
	pLo := inc[0].StartMs + lo - step
	pIdx := func(x int64) int64 { return max((x-pLo)/step, 1) }
	var total float64
	prevEnd := int64(math.MinInt64)
	last, lastK := -1, 0
	inDrop, dropEnd := false, int64(0)
	for t, s := range inc {
		if offs[t] == droppedOffset {
			if !inDrop {
				inDrop, dropEnd = true, prevEnd
				total -= p.Penalty * p.DropEnter
			}
		} else {
			k := int((offs[t] - lo) / step)
			if lo+int64(k)*step != offs[t] || k < 0 || k >= D {
				return 0, fmt.Errorf("cue %d: offset %d off the grid", t, offs[t])
			}
			chance := monoRatings(ref, s, lo, hi, D, p, A, W, v)
			total += v[k] - chance
			switch {
			case inDrop:
				total -= p.Penalty * p.DropLeave
				if last >= 0 && pIdx(dropEnd+lo+int64(lastK)*step) > pIdx(s.StartMs+lo+int64(k)*step) {
					return 0, fmt.Errorf("cue %d leaves a drop run before cue %d's end", t, last)
				}
			case last >= 0:
				gk := int(max(s.StartMs-prevEnd, 0) / step)
				if lastK < k-int(p.UpMs/step) || lastK > k+gk {
					return 0, fmt.Errorf("cue %d: offset step %d to %d", t, lastK, k)
				}
				if lastK != k {
					total -= p.Penalty
				}
			}
			inDrop, last, lastK = false, t, k
		}
		prevEnd = max(prevEnd, s.EndMs)
	}
	return total, nil
}

func closeTo(a, b float64) bool { return math.Abs(a-b) <= 1e-9*max(1, math.Abs(a), math.Abs(b)) }

func TestPiecewiseEqualsMonoDP(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		window := 100 * rapid.Int64Range(50, 150).Draw(t, "window")
		g := newGrid(window, gridMs, rapid.IntRange(1, 9).Draw(t, "every"))
		ref := disjointSpans(t, "ref", rapid.IntRange(1, 30).Draw(t, "nref"))
		inc := cutCandidate(t, ref, window)
		p := productionMono(rapid.Float64Range(0.01, 3).Draw(t, "penalty"))
		want, wantObj := monoDP(ref, inc, g.lo, g.hi, p)
		got, gotObj, err := newDP(g, ref).run(t.Context(), inc, p.Penalty)
		if err != nil {
			t.Fatal(err)
		}
		if !closeTo(gotObj, wantObj) {
			t.Fatalf("objective %v, monoDP %v", gotObj, wantObj)
		}
		for name, offs := range map[string][]int64{"dp": got, "monoDP": want} {
			score, err := monoObjective(ref, inc, offs, g.lo, g.hi, p)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !closeTo(score, wantObj) {
				t.Fatalf("%s's assignment scores %v, objective %v; inc %v offs %v ref %v window %d", name, score, wantObj, inc, offs, ref, window)
			}
		}
	})
}

func TestInsertedMsMatchesPairwise(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		base := sortedSpans(t, "cue", rapid.IntRange(1, 40).Draw(t, "n"), 600_000)
		scale := rapid.SampledFrom([]float64{1, 25 / 23.976}).Draw(t, "scale")
		segs := []timing.Segment{{}}
		for range rapid.IntRange(0, 6).Draw(t, "boundaries") {
			from := segs[len(segs)-1].FromMs + rapid.Int64Range(1, 200_000).Draw(t, "length")
			segs[len(segs)-1].ToMs = from
			segs = append(segs, timing.Segment{FromMs: from})
		}
		for i := range segs {
			segs[i].OffsetMs = rapid.Int64Range(-20_000, 20_000).Draw(t, "offset")
			segs[i].Drop = rapid.IntRange(0, 3).Draw(t, "drop") == 0
		}
		pw := timing.Piecewise{Segments: segs, Scale: scale}
		var want int64
		mapped := func(ms int64) (float64, bool) {
			seg := holding(segs, ms)
			return float64(ms)*scale + float64(seg.OffsetMs), !seg.Drop
		}
		for i, a := range base {
			for _, b := range base[i+1:] {
				ma, keptA := mapped(a.StartMs)
				mb, keptB := mapped(b.StartMs)
				if keptA && keptB {
					want = max(want, int64(math.Round((mb-ma)-scale*float64(b.StartMs-a.StartMs))))
				}
			}
		}
		if got := insertedMs(base, pw); got != want {
			t.Fatalf("insertedMs %d, pairwise %d", got, want)
		}
	})
}

func TestNonDropSegmentHoldsKeptStart(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		window := 100 * rapid.Int64Range(50, 150).Draw(t, "window")
		ref := disjointSpans(t, "ref", rapid.IntRange(1, 30).Draw(t, "nref"))
		inc := cutCandidate(t, ref, window)
		offs, err := newDP(newGrid(window, gridMs, 4), ref).fit(t.Context(), inc, rapid.Float64Range(0.01, 3).Draw(t, "penalty"))
		if err != nil {
			t.Fatal(err)
		}
		pw, _ := piecewiseOf(inc, offs, 1)
		for i, seg := range pw.Segments {
			if seg.Drop {
				continue
			}
			if !slices.ContainsFunc(inc, func(s Span) bool { return holding(pw.Segments, s.StartMs) == seg }) {
				t.Fatalf("segment %d %+v holds no cue's start", i, seg)
			}
		}
	})
}

func TestHalfDroppedScoresBelowGate(t *testing.T) {
	t.Parallel()
	// The candidate's second half is a dense run of lines over the 5 minutes
	// after the video's last dialogue, related to nothing: no offset that
	// keeps the lines after the first half moves one onto dialogue.
	video := episode(t, 30, 150)
	pause := end(video) + 1_000
	inc := slices.Clone(video)
	r := rand.New(rand.NewPCG(35, 1))
	for at := pause + 5_000; at < pause+295_000; {
		l := 1_500 + r.Int64N(1_500)
		inc = append(inc, Span{at, at + l})
		at += l + 300 + r.Int64N(300)
	}
	pw, err := piecewiseStage(t.Context(), newDP(productionGrid, video), inc, 1, 0, newRefIndex(video))
	if err != nil {
		t.Fatal(err)
	}
	stretch := inc[len(video):]
	dropped := 0
	for _, s := range mapSpans(stretch, pw.timing) {
		if s.StartMs == droppedOffset {
			dropped++
		}
	}
	if dropped != len(stretch) {
		t.Fatalf("fixture: the fit dropped %d of the %d unrelated cues", dropped, len(stretch))
	}
	t.Logf("held-out e2 %.3f", pw.heldOut)
	if pw.heldOut >= referenceGate {
		t.Errorf("held-out e2 %.3f with half the cues dropped, want under %v", pw.heldOut, referenceGate)
	}
}

func TestRefinementNeverPassesMaxRise(t *testing.T) {
	t.Parallel()
	// The last third of the candidate needs 3,050 ms more than the rest: the
	// DP's grid rise stops at 3,000, and refinement would take the 50 ms.
	video := episode(t, 32, 300)
	at := len(video) * 2 / 3
	inc := append(slices.Clone(video[:at]), shiftSpans(video[at:], -(maxRiseMs+50))...)
	r, err := AlignReference(t.Context(), video, inc, ModelPiecewise)
	if err != nil {
		t.Fatal(err)
	}
	if pw, ok := r.Timing.(timing.Piecewise); !ok || pw.Scale != 1 {
		t.Fatalf("fixture: %#v, want piecewise on scale 1", r.Timing)
	}
	if r.Shape.InsertedMs > maxRiseMs {
		t.Errorf("InsertedMs %d after refinement, want at most %d (%#v)", r.Shape.InsertedMs, maxRiseMs, r.Timing)
	}
	if uncovered(Shape{InsertedMs: r.Shape.InsertedMs, EdgeFit: 1}, true) {
		t.Errorf("Settle refuses InsertedMs %d", r.Shape.InsertedMs)
	}
}

func TestPiecewiseOnSelectedScale(t *testing.T) {
	t.Parallel()
	video := episode(t, 33, 400)
	const shift = 1_200
	inc := shiftSpans(video, -shift)
	x := newRefIndex(video)
	global, pw, err := stages(t.Context(), video, inc, x, ModelPiecewise)
	if err != nil {
		t.Fatal(err)
	}
	if global.Timing != (timing.Shift{Ms: shift}) {
		t.Fatalf("fixture: global fit %#v, want the shift", global.Timing)
	}
	if pw.timing.Scale != 1 {
		t.Errorf("piecewise stage on scale %v, want 1 when Shift won", pw.timing.Scale)
	}
	for _, seg := range pw.timing.Segments {
		if d := seg.OffsetMs - shift; !seg.Drop && (d > 100 || d < -100) {
			t.Errorf("segment %+v drifts %+d ms from the shift", seg, d)
		}
	}
}

func TestThreeBreaksSelectPiecewise(t *testing.T) {
	t.Parallel()
	video := episode(t, 14, 300)
	inc := video
	for _, p := range []float64{0.25, 0.5, 0.75} {
		inc = insert(inc, int64(p*float64(end(video))), 120_000, nil)
	}
	r, err := AlignReference(t.Context(), video, inc, ModelPiecewise)
	if err != nil {
		t.Fatal(err)
	}
	if modelOf(r) != ModelPiecewise {
		t.Fatalf("model %d (%#v), want piecewise", modelOf(r), r.Timing)
	}
	placed := 0
	for i, s := range mapSpans(inc, r.Timing) {
		if d := s.StartMs - video[i].StartMs; s.StartMs != droppedOffset && d <= 100 && d >= -100 {
			placed++
		}
	}
	if placed*100 < len(inc)*95 {
		t.Errorf("%d of %d cues within 100 ms, want 95%%", placed, len(inc))
	}
}

func TestPiecewiseSelection(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fit  pwFit
		want bool
	}{
		{"held out by the gain", pwFit{ok: true, heldOut: 0.75, global: 0.65}, true},
		{"held out by less", pwFit{ok: true, heldOut: 0.74, global: 0.65}, false},
		{"an inadmissible mapping", pwFit{heldOut: 0.95, global: 0.1}, false},
	} {
		if got := c.fit.accepted(); got != c.want {
			t.Errorf("%s: accepted %v, want %v", c.name, got, c.want)
		}
	}
	cues := func(n int) ([]Span, []int64) {
		base, offs := make([]Span, n), make([]int64, n)
		for i := range base {
			base[i], offs[i] = Span{int64(i+1) * 1_000, int64(i+1)*1_000 + 500}, int64(i%2)*100
		}
		return base, offs
	}
	for _, c := range []struct {
		n    int
		want bool
	}{{1, false}, {2, true}, {2_048, true}, {2_049, false}} {
		base, offs := cues(c.n)
		if pw, ok := piecewiseOf(base, offs, 1); ok != c.want {
			t.Errorf("%d alternating offsets: %d segments, ok %v, want %v", c.n, len(pw.Segments), ok, c.want)
		}
	}
	if _, ok := piecewiseOf([]Span{{1_000, 2_000}, {3_000, 4_000}}, []int64{droppedOffset, droppedOffset}, 1); ok {
		t.Error("every cue dropped: ok, want refused")
	}
	t.Run("a cue starting with the segment joins it", func(t *testing.T) {
		base := []Span{{1_000, 2_000}, {3_000, 4_000}, {3_000, 3_500}, {5_000, 6_000}}
		pw, ok := piecewiseOf(base, []int64{0, 200, 400, droppedOffset}, 1)
		want := []timing.Segment{{FromMs: 0, ToMs: 3_000}, {FromMs: 3_000, ToMs: 5_000, OffsetMs: 200}, {FromMs: 5_000, Drop: true}}
		if !ok || !slices.Equal(pw.Segments, want) {
			t.Errorf("segments %+v ok %v, want %+v", pw.Segments, ok, want)
		}
	})
}

// Two offsets both rate 4/3; summed in floats the higher reads
// 1.3333333333333335, and the lower still wins the tie.
func TestBestNearTiesGoLowest(t *testing.T) {
	t.Parallel()
	ref := []Span{{100, 140}, {340, 390}}
	spans := []Span{{160, 220}, {320, 380}, {380, 440}}
	if got := bestNear(ref, 50, spans, 0, 100, 100); got != -60 {
		t.Errorf("bestNear = %d, want -60, the lowest of the two offsets rating 4/3", got)
	}
}
