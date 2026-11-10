package subsync

import (
	"errors"

	"github.com/cplieger/subflux/internal/timing"
)

// ErrDifferentCut reports a mapping showing the candidate is for another cut
// of the video: reference dialogue it leaves uncovered past either end, time
// it inserts, or an end of the subtitle that fits the reference worse than
// edgeGate.
var ErrDifferentCut = errors.New("subsync: the subtitle is for a different cut")

// Fallback is what a caller delivers for a fit below its gate.
type Fallback uint8

const (
	// AtIdentity delivers the candidate where it lies, Unverified: search's
	// delivery.
	AtIdentity Fallback = iota
	// AtFit offers the fit's own timing, Unverified: a proposal a person
	// reviews before applying it, or a batch, which applies nothing below
	// the gate.
	AtFit
)

// Settle returns r's timing and Verified when its gate passes
// (referenceGate, or audioGate for audio), else, Unverified, the mapping
// below names. It refuses with ErrDifferentCut instead when the coverage
// guard finds a cut in the mapping it returns or, for a reference fit, in
// the fit's own Shape at any confidence. An unmeasured result has no
// reference to judge against. forced skips the EdgeSpeechMs ground.
func Settle(r *Result, forced bool, below Fallback) (timing.Timing, timing.Verdict, error) {
	gate := referenceGate
	if r.Method == timing.Audio {
		gate = audioGate
	}
	tm, v, delivered := r.Timing, timing.Verified, r.Shape
	if r.Confidence < gate {
		v = timing.Unverified
		if below == AtIdentity {
			tm, delivered = timing.Identity{}, r.IdentityShape
		}
	}
	fitJudged := r.Method != timing.Audio && uncovered(r.Shape, forced)
	if r.Measured && (fitJudged || uncovered(delivered, forced)) {
		return nil, 0, ErrDifferentCut
	}
	return tm, v, nil
}

// uncovered is the coverage guard: time the mapping inserts, an end run
// that fits worse than edgeGate, or reference dialogue it leaves uncovered
// past either end of the subtitle.
func uncovered(s Shape, forced bool) bool {
	return s.InsertedMs > maxRiseMs || s.EdgeFit < edgeGate || !forced && s.EdgeSpeechMs > maxEdgeSpeechMs
}
