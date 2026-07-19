package subsync

import "fmt"

// Confidence represents the quality of a sync operation (0.0 to 1.0).
type Confidence float64

// SyncMethod is a typed string identifying the sync algorithm used.
type SyncMethod string

// Sync method identifiers.
const (
	MethodNone      SyncMethod = "none"
	MethodOffset    SyncMethod = "offset"
	MethodFramerate SyncMethod = "framerate"
	MethodSplit     SyncMethod = "split"
	MethodAudio     SyncMethod = "audio"
	MethodCrosslang SyncMethod = "crosslang"
)

// String implements fmt.Stringer.
func (m SyncMethod) String() string { return string(m) }

// Compile-time assertion: SyncMethod satisfies fmt.Stringer.
var _ fmt.Stringer = SyncMethod("")

const (
	// ConfidenceNone means no sync was performed or the result is unusable.
	ConfidenceNone Confidence = 0

	// ConfidenceWeak means the sync may be wrong; caller should consider
	// keeping the original timing.
	ConfidenceWeak Confidence = 0.3

	// ConfidenceModerate means the sync is likely correct but may have issues.
	ConfidenceModerate Confidence = 0.6

	// ConfidenceStrong means high confidence in the sync result.
	ConfidenceStrong Confidence = 0.8

	// ConfidencePerfect means hash match or perfect correlation.
	ConfidencePerfect Confidence = 1.0
)

// ConfidenceCaps holds per-strategy confidence ceilings. Each strategy has a
// different cap reflecting its inherent reliability. The relative ordering is
// intentional: framerate with FPS confirmation > known ratio > audio/offset > GSS/split.
type ConfidenceCaps struct {
	Audio                  Confidence // audio-based sync
	Offset                 Confidence // constant-offset sync
	FramerateGSS           Confidence // golden-section framerate search
	FramerateKnown         Confidence // known-ratio framerate match
	FramerateFPS           Confidence // video FPS confirms the ratio
	Crosslang              Confidence // cross-language alignment
	SplitBase              Confidence // split-aware alignment (reduced by segment penalty)
	SplitPenaltyPerSegment Confidence // confidence penalty per additional segment
	SplitMinConf           Confidence // minimum confidence floor for split alignment
}

// DefaultConfidenceCaps is the production confidence cap table.
var DefaultConfidenceCaps = ConfidenceCaps{
	Audio:                  0.9,
	Offset:                 0.9,
	FramerateGSS:           0.85,
	FramerateKnown:         0.95,
	FramerateFPS:           0.98,
	Crosslang:              0.92,
	SplitBase:              0.85,
	SplitPenaltyPerSegment: 0.05,
	SplitMinConf:           0.4,
}

// ForMethod returns the confidence cap for the given sync method.
// For framerate methods, returns FramerateGSS as the conservative default;
// callers with FPS confirmation should use FramerateFPS directly.
func (c ConfidenceCaps) ForMethod(m SyncMethod) Confidence {
	switch m {
	case MethodAudio:
		return c.Audio
	case MethodOffset:
		return c.Offset
	case MethodFramerate:
		return c.FramerateGSS
	case MethodSplit:
		return c.SplitBase
	case MethodCrosslang:
		return c.Crosslang
	default:
		return 0
	}
}

// ShouldApplyThreshold is the exported minimum confidence for a sync result
// to be considered applicable. Consumers comparing Result.Confidence against
// a threshold should use this constant instead of a magic 0.5 literal.
// This is the audio/fallback threshold; reference-based sync uses
// DefaultMinConfidence (0.5) as the engine default, while the API layer
// may apply a stricter threshold (e.g. 0.6) for user-facing auto-sync.
const ShouldApplyThreshold Confidence = 0.5

// DefaultMinConfidence is the default minimum confidence used by the sync
// engine when no explicit threshold is provided. This is the single source
// of truth for the engine's default; the API layer's DefaultSyncMinConfidence
// (0.6) is intentionally stricter for user-facing auto-apply decisions.
const DefaultMinConfidence Confidence = 0.5

// MinCuesForSync is the minimum number of subtitle cues required for any
// timing-based sync strategy to produce meaningful results. Fewer than this
// provides insufficient signal for correlation or alignment.
const MinCuesForSync = 5

// SyncResult holds the output of any sync operation.
type SyncResult struct {
	Method     SyncMethod // MethodNone, MethodOffset, MethodFramerate, MethodSplit, MethodAudio, MethodCrosslang
	Cues       []Cue
	Transform  Transform       // descriptor of the correction applied to Cues (voted candidates)
	Offset     int64           // milliseconds (constant offset applied)
	Confidence Confidence      // quality of the sync (calibrated; gates read this)
	Rate       float64         // framerate ratio applied (1.0 = no change)
	Source     CandidateSource // generating strategy identity (voted candidates)
}

// Applied returns true if the sync actually changed the subtitle timing.
// Checks offset (constant shift), rate (framerate correction), and split
// method (multi-segment alignment where no single offset/rate captures the change).
func (r *SyncResult) Applied() bool {
	if r.Offset != 0 {
		return true
	}
	if r.Rate != 0 && r.Rate != 1.0 {
		return true
	}
	// Split-aware alignment applies per-segment offsets that aren't captured
	// by the single Offset field. Detect via method + non-zero confidence.
	return r.Method == MethodSplit && r.Confidence > ConfidenceNone
}

// ShouldApply returns true if the confidence is high enough to use the result.
// Threshold: ShouldApplyThreshold (moderate confidence or better). This is the
// post-hoc check used by callers after sync completes.
func (r *SyncResult) ShouldApply() bool {
	return r.Confidence >= ShouldApplyThreshold
}
