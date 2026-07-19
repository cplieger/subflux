// transform.go describes the timing correction a sync candidate applies
// (transform descriptor) and the stable identity of the strategy that
// generated it (candidate source). Both feed the arbitration layer in
// vote.go: validity checks, canonical ordering, logging, and the golden
// corpus digest.

package subsync

import "fmt"

// TransformKind identifies the shape of the timing correction a sync
// candidate applies to the incorrect cues.
type TransformKind uint8

// Transform kinds.
const (
	// TransformNone marks a result that carries no transform descriptor
	// (results that never enter the vote, and no-op placeholders).
	TransformNone TransformKind = iota
	// TransformShift is a single constant time shift of all cues.
	TransformShift
	// TransformFramerate is a linear rescale of all cue times by a ratio.
	TransformFramerate
	// TransformSegments is a set of per-segment constant shifts
	// (split-aware alignment).
	TransformSegments
)

// Segment describes one contiguous cue range and the constant shift a
// split-aware correction applies to it. Indexes address the corrected cue
// slice; EndIdx is exclusive.
type Segment struct {
	StartIdx int
	EndIdx   int
	ShiftMs  int64
}

// Transform describes the timing correction a SyncResult applies to the
// incorrect cues. It exists for candidate validation, logging, and the
// corpus digest. It is deliberately NOT usable as a map key (Segments is a
// slice, and Ratio would need canonical float equality): cluster membership
// is decided by comparing corrected cues, never by comparing transforms.
type Transform struct {
	Segments []Segment // per-segment shifts (Kind == TransformSegments)
	Shift    int64     // shift in milliseconds (Kind == TransformShift)
	Ratio    float64   // framerate ratio (Kind == TransformFramerate)
	Kind     TransformKind
}

// Digest renders the transform as a compact "kind(parameter)" string for
// the voting log line and the corpus artifacts: the shift in milliseconds,
// the framerate ratio, or the segment count.
func (t Transform) Digest() string {
	switch t.Kind {
	case TransformShift:
		return fmt.Sprintf("shift(%dms)", t.Shift)
	case TransformFramerate:
		return fmt.Sprintf("framerate(%.6g)", t.Ratio)
	case TransformSegments:
		return fmt.Sprintf("segments(%d)", len(t.Segments))
	default:
		return "none"
	}
}

// CandidateSource is the stable identity of the strategy that generated a
// candidate. It is distinct from Transform.Kind (crosslang and the constant
// offset strategy both apply shift transforms) and from SyncMethod (a
// string with no ordering). The declared order is the canonical arbitration
// order: clustering input is sorted by it, and rating ties resolve to the
// earliest source.
type CandidateSource int

// Candidate sources in canonical arbitration order.
const (
	// SourceNone marks a result that did not come from a voted reference
	// strategy (audio results and no-result placeholders).
	SourceNone CandidateSource = iota
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
func (s CandidateSource) String() string {
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

// Compile-time assertion: CandidateSource satisfies fmt.Stringer.
var _ fmt.Stringer = SourceNone
