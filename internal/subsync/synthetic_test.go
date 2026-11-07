package subsync

import (
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// referenceCues reads testdata/reference.srt's dialogue: 60 cues over six
// minutes with uneven lengths and gaps.
func referenceCues(t testing.TB) []Span {
	t.Helper()
	b, err := os.ReadFile("testdata/reference.srt")
	if err != nil {
		t.Fatal(err)
	}
	c, err := timing.ParseCues(b)
	if err != nil {
		t.Fatal(err)
	}
	spans := DialogueSpans(c)
	if len(spans) != 60 {
		t.Fatalf("reference.srt: %d dialogue spans, want 60", len(spans))
	}
	return spans
}

// episode builds n cues whose lengths and gaps are drawn, seeded by k, from
// reference.srt's: no stretch of one episode repeats inside it or in
// another, so an aligner cannot lock onto a period.
func episode(t testing.TB, k, n int) []Span {
	t.Helper()
	ref := referenceCues(t)
	lens := make([]int64, len(ref))
	gaps := make([]int64, len(ref))
	for i, s := range ref {
		lens[i] = s.EndMs - s.StartMs
		if i+1 < len(ref) {
			gaps[i] = ref[i+1].StartMs - s.EndMs
		} else {
			gaps[i] = 2_000
		}
	}
	r := rand.New(rand.NewPCG(uint64(k), 1))
	out := make([]Span, n)
	at := int64(1_000)
	for j := range out {
		l, g := lens[r.IntN(len(lens))], gaps[r.IntN(len(gaps))]
		out[j] = Span{at, at + l}
		at += l + g
	}
	return out
}

// mapSpans applies t to every span the way Render maps a cue's times; a
// span a Piecewise drops maps to droppedOffset at both ends.
func mapSpans(spans []Span, t timing.Timing) []Span {
	out := make([]Span, len(spans))
	for i, s := range spans {
		switch t := t.(type) {
		case timing.Shift:
			out[i] = Span{s.StartMs + t.Ms, s.EndMs + t.Ms}
		case timing.Linear:
			out[i] = Span{scaleMs(s.StartMs, t.Scale) + t.OffsetMs, scaleMs(s.EndMs, t.Scale) + t.OffsetMs}
		case timing.Piecewise:
			out[i] = Span{droppedOffset, droppedOffset}
			if seg := holding(t.Segments, s.StartMs); !seg.Drop {
				out[i] = Span{scaleMs(s.StartMs, t.Scale) + seg.OffsetMs, scaleMs(s.EndMs, t.Scale) + seg.OffsetMs}
			}
		default:
			out[i] = s
		}
	}
	return out
}

// holding is the last segment starting at or before ms.
func holding(segs []timing.Segment, ms int64) timing.Segment {
	seg := segs[0]
	for _, s := range segs {
		if s.FromMs <= ms {
			seg = s
		}
	}
	return seg
}

func scaleMs(ms int64, scale float64) int64 { return int64(math.Round(float64(ms) * scale)) }

// unmap is a candidate whose cues sit where video cues land after the
// inverse of t: aligning it to video must find t.
func unmap(video []Span, scale float64, offsetMs int64) []Span {
	out := make([]Span, len(video))
	for i, s := range video {
		out[i] = Span{int64(math.Round(float64(s.StartMs-offsetMs) / scale)), int64(math.Round(float64(s.EndMs-offsetMs) / scale))}
	}
	return out
}

// assertPlaced fails unless t maps every cue of inc within 100 ms of the
// video cue it stands for.
func assertPlaced(t *testing.T, video, inc []Span, got timing.Timing) {
	t.Helper()
	for i, s := range mapSpans(inc, got) {
		if d := s.StartMs - video[i].StartMs; d > 100 || d < -100 {
			t.Fatalf("cue %d placed at %d, video %d (%+d ms) under %#v", i, s.StartMs, video[i].StartMs, d, got)
		}
	}
}

// dense is episode k with its gaps quartered: about four fifths dialogue.
func dense(t testing.TB, k, n int) []Span {
	t.Helper()
	sparse := episode(t, k, n)
	out := make([]Span, len(sparse))
	at := sparse[0].StartMs
	for i, s := range sparse {
		out[i] = Span{at, at + s.EndMs - s.StartMs}
		if i+1 < len(sparse) {
			at = out[i].EndMs + (sparse[i+1].StartMs-s.EndMs)/4
		}
	}
	return out
}

// cut is the candidate for a cut of video without [at, at+lenMs): those
// cues are gone and every later one is lenMs earlier.
func cut(video []Span, at, lenMs int64) []Span {
	var out []Span
	for _, s := range video {
		switch {
		case s.EndMs <= at:
			out = append(out, s)
		case s.StartMs >= at+lenMs:
			out = append(out, Span{s.StartMs - lenMs, s.EndMs - lenMs})
		}
	}
	return out
}

// insert is the candidate holding lenMs of material at at that video lacks:
// every later cue is lenMs later, and extra's cues, shifted to start at
// at, fill the stretch.
func insert(video []Span, at, lenMs int64, extra []Span) []Span {
	var out []Span
	for _, s := range video {
		if s.StartMs < at {
			out = append(out, s)
		}
	}
	for _, s := range extra {
		if s.EndMs-extra[0].StartMs <= lenMs {
			out = append(out, Span{s.StartMs - extra[0].StartMs + at, s.EndMs - extra[0].StartMs + at})
		}
	}
	for _, s := range video {
		if s.StartMs >= at {
			out = append(out, Span{s.StartMs + lenMs, s.EndMs + lenMs})
		}
	}
	return out
}

// displaced moves the share f of ref's inner cues, spread evenly, into the
// gap after each: still dialogue, no longer under the candidate's lines.
// The first and last cues stay, so the reference keeps its extent.
func displaced(ref []Span, f float64) []Span {
	out := slices.Clone(ref)
	for i := 1; i+1 < len(out); i++ {
		if math.Mod(float64(i)*0.6180339887, 1) >= f {
			continue
		}
		next := ref[i+1].StartMs
		gap := next - ref[i].EndMs
		l := min(ref[i].EndMs-ref[i].StartMs, max(gap-400, 1))
		mid := ref[i].EndMs + gap/2
		out[i] = Span{mid - l/2, mid - l/2 + l}
	}
	return out
}

// thinned is ref displaced until the fit at t, applied to inc, scores e2
// target; 0 displaces every cue.
func thinned(ref, inc []Span, tm timing.Timing, target float64) []Span {
	kept := mapSpans(inc, tm)
	score := func(f float64) float64 {
		conf, _ := measure(fit{kept: kept, displayMs: displayMs(kept)}, newRefIndex(displaced(ref, f)))
		return conf
	}
	if target <= 0 {
		return displaced(ref, 1)
	}
	lo, hi := 0.0, 1.0
	for range 30 {
		if mid := (lo + hi) / 2; score(mid) > target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return displaced(ref, lo)
}

// modelOf reads the model a result's timing shows: a refusal is an
// unmeasured Identity at confidence 0, and a fit at offset 0 is a shift
// that Canonical made Identity.
func modelOf(r Result) Model {
	switch r.Timing.(type) {
	case timing.Shift:
		return ModelShift
	case timing.Linear:
		return ModelLinear
	case timing.Piecewise:
		return ModelPiecewise
	}
	if !r.Measured && r.Confidence == 0 {
		return ModelNone
	}
	return ModelShift
}
