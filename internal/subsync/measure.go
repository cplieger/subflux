package subsync

import (
	"math"
	"slices"
)

// refIndex is the reference's dialogue as sorted, disjoint intervals, with
// the dialogue time before each one.
type refIndex struct {
	iv     []Span
	before []int64
}

func newRefIndex(ref []Span) refIndex {
	sorted := slices.Clone(ref)
	slices.SortFunc(sorted, func(a, b Span) int { return byStart(a, b.StartMs) })
	var x refIndex
	var total int64
	for _, s := range sorted {
		if s.EndMs <= s.StartMs {
			continue
		}
		if n := len(x.iv); n > 0 && s.StartMs <= x.iv[n-1].EndMs {
			if s.EndMs > x.iv[n-1].EndMs {
				total += s.EndMs - x.iv[n-1].EndMs
				x.iv[n-1].EndMs = s.EndMs
			}
			continue
		}
		x.iv = append(x.iv, s)
		x.before = append(x.before, total)
		total += s.EndMs - s.StartMs
	}
	return x
}

// upTo is the reference dialogue before t.
func (x refIndex) upTo(t int64) int64 {
	i, _ := slices.BinarySearchFunc(x.iv, t, func(s Span, t int64) int {
		switch {
		case s.EndMs <= t:
			return -1
		case s.StartMs > t:
			return 1
		}
		return 0
	})
	if i == len(x.iv) {
		if i == 0 {
			return 0
		}
		return x.before[i-1] + x.iv[i-1].EndMs - x.iv[i-1].StartMs
	}
	return x.before[i] + max(0, t-x.iv[i].StartMs)
}

// speech is the reference dialogue inside [from, to).
func (x refIndex) speech(from, to int64) int64 {
	if to <= from {
		return 0
	}
	return x.upTo(to) - x.upTo(from)
}

// coverage is c, the chance level of a placement: the share of the
// reference's span, first start to last end, that is dialogue.
func (x refIndex) coverage() float64 {
	if len(x.iv) == 0 {
		return 0
	}
	n := len(x.iv) - 1
	width := x.iv[n].EndMs - x.iv[0].StartMs
	return float64(x.before[n]+x.iv[n].EndMs-x.iv[n].StartMs) / float64(width)
}

// near reports whether reference dialogue lies within slack of s.
func (x refIndex) near(s Span, slack int64) bool {
	return x.speech(s.StartMs-slack, s.EndMs+slack) > 0
}

// e2 is the chance-corrected coverage clamp((s2 − c) / (1 − c), 0, 1), where
// s2 is overlapMs over displayMs: the display time reference dialogue
// overlaps over the display time of every scored cue.
func e2(overlapMs, displayMs int64, c float64) float64 {
	if displayMs <= 0 || c >= 1 {
		return 0
	}
	s2 := float64(overlapMs) / float64(displayMs)
	return min(max((s2-c)/(1-c), 0), 1)
}

// scaleSpans maps each span's times t to round(t × scale), halves away
// from 0, as Render rounds.
func scaleSpans(spans []Span, scale float64) []Span {
	out := make([]Span, len(spans))
	for i, s := range spans {
		out[i] = Span{int64(math.Round(float64(s.StartMs) * scale)), int64(math.Round(float64(s.EndMs) * scale))}
	}
	return out
}

func shiftSpans(spans []Span, ms int64) []Span {
	out := make([]Span, len(spans))
	for i, s := range spans {
		out[i] = Span{s.StartMs + ms, s.EndMs + ms}
	}
	return out
}
