package subsync

import (
	"context"

	"github.com/cplieger/subflux/internal/subsync/crosslang"
)

// crossLangAlign delegates to the crosslang sub-package. Cue and
// crosslang.Cue are converted with plain copies: a subtitle carries ~1.5k
// cues and alignment costs seconds of CPU, so the microseconds saved by the
// former unsafe zero-copy reinterpretation never justified its layout-drift
// footgun (same-size field changes would corrupt silently).
func crossLangAlign(ctx context.Context, reference, incorrect []Cue) SyncResult {
	toCL := func(in []Cue) []crosslang.Cue {
		out := make([]crosslang.Cue, len(in))
		for i, c := range in {
			out[i] = crosslang.Cue{Text: c.Text, Start: c.Start, End: c.End}
		}
		return out
	}

	result := crosslang.Align(ctx, toCL(reference), toCL(incorrect))

	var cues []Cue
	if len(result.Cues) > 0 {
		cues = make([]Cue, len(result.Cues))
		for i, c := range result.Cues {
			cues[i] = Cue{Text: c.Text, Start: c.Start, End: c.End}
		}
	}

	return SyncResult{
		Cues:       cues,
		Offset:     result.Offset,
		Rate:       result.Rate,
		Confidence: Confidence(result.Confidence),
		Method:     MethodCrosslang,
		Source:     SourceCrosslang,
		Transform:  Transform{Kind: TransformShift, Shift: result.Offset},
	}
}
