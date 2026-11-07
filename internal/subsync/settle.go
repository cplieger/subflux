package subsync

import (
	"errors"

	"github.com/cplieger/subflux/internal/timing"
)

// ErrDifferentCut reports a mapping showing the candidate is for another cut
// of the video: reference dialogue it leaves uncovered, lines over no reference
// dialogue, time it inserts, or an end of the subtitle that fits the
// reference worse than edgeGate.
var ErrDifferentCut = errors.New("subsync: the subtitle is for a different cut")

// Settle refuses a measured result with ErrDifferentCut when its InsertedMs
// passes maxRiseMs, its GapSpeechMs passes maxGapSpeechMs, its EdgeSpeechMs
// passes maxEdgeSpeechMs or its ExtraLinesMs passes maxExtraLinesMs, at any
// confidence for a reference fit and once audioGate passes for an audio one;
// and, once its gate passes, when its EdgeFit is below edgeGate. forced skips
// the GapSpeechMs, EdgeSpeechMs and ExtraLinesMs grounds. Otherwise it
// returns r's timing and Verified when its gate passes (referenceGate, or
// audioGate for audio), else Identity and Unverified.
func Settle(r Result, forced bool) (timing.Timing, timing.Verdict, error) {
	gate := referenceGate
	if r.Method == timing.Audio {
		gate = audioGate
	}
	passes := r.Confidence >= gate
	// An audio fit's reference already ran the guard at any confidence
	// before audio ran, so audio meets it only once its own gate passes.
	if r.Measured && (passes || r.Method != timing.Audio) && uncovered(r.Shape, forced) {
		return nil, 0, ErrDifferentCut
	}
	if !passes {
		return timing.Identity{}, timing.Unverified, nil
	}
	if r.Measured && r.Shape.EdgeFit < edgeGate {
		return nil, 0, ErrDifferentCut
	}
	return r.Timing, timing.Verified, nil
}

// uncovered is the coverage guard: content one side has and the other
// lacks, or time the mapping inserts.
func uncovered(s Shape, forced bool) bool {
	if s.InsertedMs > maxRiseMs {
		return true
	}
	return !forced && (s.GapSpeechMs > maxGapSpeechMs || s.EdgeSpeechMs > maxEdgeSpeechMs || s.ExtraLinesMs > maxExtraLinesMs)
}
