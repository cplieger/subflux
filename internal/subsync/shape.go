package subsync

import "slices"

// Shape is what Settle reads of one mapping, in video time after its scale.
type Shape struct {
	// EdgeSpeechMs is the reference dialogue lying more than maxRiseMs before
	// the first kept cue's start or after the last kept cue's end.
	EdgeSpeechMs int64
	// InsertedMs is the most the mapping lengthens a kept-to-kept gap beyond
	// Scale times its candidate gap; 0 for Shift and Linear.
	InsertedMs int64
	// EdgeFit is the lowest e2 of any run of at least edgeCues consecutive
	// kept cues that starts at the first kept cue or ends at the last; the
	// whole mapping's e2 when fewer than 2 × edgeCues cues are kept.
	EdgeFit float64
}

// fit is one mapping's kept cues in video time and the display time of every
// scored cue, dropped ones included.
type fit struct {
	kept      []Span
	displayMs int64
}

// measure returns the fit's e2 and its Shape against x; InsertedMs is the
// mapping's own and left 0 here.
func measure(f fit, x refIndex) (float64, Shape) {
	kept := slices.Clone(f.kept)
	slices.SortStableFunc(kept, func(a, b Span) int { return byStart(a, b.StartMs) })
	c := x.coverage()
	overlap := make([]int64, len(kept))
	var total int64
	for i, s := range kept {
		overlap[i] = x.speech(s.StartMs, s.EndMs)
		total += overlap[i]
	}
	conf := e2(total, f.displayMs, c)
	if len(kept) == 0 {
		return conf, Shape{EdgeSpeechMs: x.speech(minTime, maxTime)}
	}
	end := kept[0].EndMs
	for _, s := range kept[1:] {
		end = max(end, s.EndMs)
	}
	return conf, Shape{
		EdgeSpeechMs: x.speech(minTime, kept[0].StartMs-maxRiseMs) + x.speech(end+maxRiseMs, maxTime),
		EdgeFit:      edgeFit(kept, overlap, conf, c),
	}
}

// The ends of the timeline EdgeSpeechMs reads to.
const (
	minTime = -1 << 62
	maxTime = 1 << 62
)

// edgeFit is Shape.EdgeFit over kept, sorted by start, whose cue i overlaps
// overlap[i] ms of reference dialogue; whole is the fit's own e2.
func edgeFit(kept []Span, overlap []int64, whole, c float64) float64 {
	n := len(kept)
	if n < 2*edgeCues {
		return whole
	}
	lowest := whole
	var ov, disp int64
	for i := range n {
		ov, disp = ov+overlap[i], disp+kept[i].EndMs-kept[i].StartMs
		if i+1 >= edgeCues {
			lowest = min(lowest, e2(ov, disp, c))
		}
	}
	ov, disp = 0, 0
	for i := n - 1; i >= 0; i-- {
		ov, disp = ov+overlap[i], disp+kept[i].EndMs-kept[i].StartMs
		if n-i >= edgeCues {
			lowest = min(lowest, e2(ov, disp, c))
		}
	}
	return lowest
}
