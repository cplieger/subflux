// transform.go describes the timing correction a sync candidate applies
// (transform descriptor) and the stable identity of the strategy that
// generated it (candidate source). Both feed the arbitration layer in
// vote.go: validity checks, canonical ordering, logging, and the golden
// corpus digest.

package subsync

import "fmt"

// transformKind identifies the shape of the timing correction a sync
// candidate applies to the incorrect cues.
type transformKind uint8

// Transform kinds.
const (
	// transformNone marks a result that carries no transform descriptor
	// (results that never enter the vote, and no-op placeholders).
	//deadset:ignore DS1302 -- The zero value every unset result carries; without it transformShift becomes the zero value.
	transformNone transformKind = iota
	// transformShift is a single constant time shift of all cues.
	transformShift
	// transformFramerate is a linear rescale of all cue times by a ratio.
	transformFramerate
	// transformSegments is a set of per-segment constant shifts
	// (split-aware alignment).
	transformSegments
)

// transform describes the timing correction a SyncResult applies to the
// incorrect cues. It exists for candidate validation, logging, and the
// corpus digest. It is deliberately NOT used as a map key (Ratio would need
// canonical float equality): cluster membership is decided by comparing
// corrected cues, never by comparing transforms.
type transform struct {
	Shift        int64   // shift in milliseconds (Kind == transformShift)
	Ratio        float64 // framerate ratio (Kind == transformFramerate)
	SegmentCount int     // per-segment shifts applied (Kind == transformSegments)
	Kind         transformKind
}

// digest renders the transform as a compact "kind(parameter)" string for
// the voting log line and the corpus artifacts: the shift in milliseconds,
// the framerate ratio, or the segment count.
func (t transform) digest() string {
	switch t.Kind {
	case transformShift:
		return fmt.Sprintf("shift(%dms)", t.Shift)
	case transformFramerate:
		return fmt.Sprintf("framerate(%.6g)", t.Ratio)
	case transformSegments:
		return fmt.Sprintf("segments(%d)", t.SegmentCount)
	default:
		return "none"
	}
}

// candidateSource is the stable identity of the strategy that generated a
// candidate. It is distinct from Transform.Kind (crosslang and the constant
// offset strategy both apply shift transforms) and from SyncMethod (a
// string with no ordering). The declared order is the canonical arbitration
// order: clustering input is sorted by it, and rating ties resolve to the
// earliest source.
type candidateSource int

// Candidate sources in canonical arbitration order.
const (
	// SourceNone marks a result that did not come from a voted reference
	// strategy (audio results and no-result placeholders).
	SourceNone candidateSource = iota
	// SourceCrosslang is the cross-language anchor alignment strategy.
	SourceCrosslang
	// SourceFramerate is the framerate correction strategy.
	SourceFramerate
	// SourceOffset is the constant-offset (alass) strategy.
	SourceOffset
	// SourceSplit is the split-aware DP alignment strategy.
	SourceSplit
)

// String implements fmt.Stringer. The zero value renders as "none", which
// the corpus artifacts admit as a winner source.
func (s candidateSource) String() string {
	switch s {
	case SourceCrosslang:
		return "crosslang"
	case SourceFramerate:
		return "framerate"
	case SourceOffset:
		return "offset"
	case SourceSplit:
		return "split"
	default:
		return "none"
	}
}

// Compile-time assertion: candidateSource satisfies fmt.Stringer.
var _ fmt.Stringer = SourceNone
