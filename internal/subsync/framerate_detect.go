package subsync

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/subsync/framerate"
)

// framerateConfig consolidates all framerate detection tuning parameters.
// Matches the pattern used by audioSyncConfig and voteConfig.
type framerateConfig struct {
	// RatioTolerance is the maximum relative error between the observed
	// drift ratio and a known framerate pair for a match.
	RatioTolerance float64
	// MinCues is the minimum number of cues needed to reliably detect
	// framerate drift.
	MinCues int
	// MinDuration is the minimum subtitle duration for reliable detection.
	MinDuration time.Duration
	// CueCountMismatchThreshold is the max relative difference in cue counts
	// before skipping framerate correction.
	CueCountMismatchThreshold float64
	// FPSTolerance is the tolerance for matching video FPS to known framerates.
	FPSTolerance float64
	// MaxResidualMs is the maximum residual (ms) for verification to pass.
	MaxResidualMs int64
	// GSSMaxIter is the maximum iterations for golden-section search.
	GSSMaxIter int
	// GSSTolerance is the convergence tolerance for golden-section search.
	GSSTolerance float64
	// GSSSearchRange is the ±percentage around observed ratio to search.
	GSSSearchRange float64
}

// defaultFramerateConfig holds the production tuning values.
var defaultFramerateConfig = framerateConfig{
	RatioTolerance:            0.001,
	MinCues:                   20,
	MinDuration:               2 * time.Minute,
	CueCountMismatchThreshold: 0.25,
	FPSTolerance:              0.05,
	MaxResidualMs:             200,
	GSSMaxIter:                30,
	GSSTolerance:              1e-7,
	GSSSearchRange:            0.05,
}

// correctFramerate detects and corrects framerate mismatch between
// reference and incorrect subtitles. Returns the corrected cues, the
// applied ratio, and a confidence score.
//
// When videoPath is provided, ffprobe detects the actual video FPS to
// narrow the known-ratio search and tighten the golden-section range.
//
// The algorithm:
// 1. Measure drift at multiple points across the subtitle timeline
// 2. If drift grows linearly (not constant), it's a framerate issue
// 3. Try known framerate ratios first (fast, high confidence)
// 4. Fall back to golden-section search if no known ratio matches
func correctFramerate(ctx context.Context, reference, incorrect []Cue, videoPath string) SyncResult {
	noResult := SyncResult{Rate: 1.0, Confidence: ConfidenceNone, Method: MethodFramerate}

	if len(reference) < defaultFramerateConfig.MinCues || len(incorrect) < defaultFramerateConfig.MinCues {
		return noResult
	}

	// Skip framerate correction when cue counts differ significantly.
	// measureDrifts assumes positional correspondence (cue N in reference
	// ≈ cue N in incorrect). With different languages or different line
	// splitting, cue counts can differ by 30%+, making drift measurements
	// compare unrelated dialogue lines and producing garbage ratios.
	minCues := min(len(reference), len(incorrect))
	maxCues := max(len(reference), len(incorrect))
	if float64(maxCues-minCues)/float64(maxCues) > defaultFramerateConfig.CueCountMismatchThreshold {
		slog.Debug("framerate: cue count mismatch too large, skipping",
			"ref_cues", len(reference), "inc_cues", len(incorrect))
		return noResult
	}

	// Check subtitle duration is long enough.
	refDur := reference[len(reference)-1].End
	incDur := incorrect[len(incorrect)-1].End
	if refDur < defaultFramerateConfig.MinDuration || incDur < defaultFramerateConfig.MinDuration {
		return noResult
	}

	// Measure drift at multiple points.
	drifts := measureDrifts(reference, incorrect)
	// Defensive: unreachable when both inputs have >= MinCues cues,
	// since measureDrifts returns min(10, n/2) >= 3 when n >= 20.
	if len(drifts) < 3 {
		return noResult
	}

	// Check if drift is linear (framerate issue) vs constant (offset issue).
	slope, intercept, r2 := linearRegression(drifts)
	if r2 < framerate.MinLinearR2 {
		// Drift is not linear; not a framerate issue.
		return noResult
	}

	// The slope of drift vs time gives us the framerate ratio deviation.
	// drift(t) = (ratio - 1) * t + offset
	// So ratio = slope + 1.
	observedRatio := slope + 1.0

	// Detect actual video FPS via ffprobe to narrow the search.
	var videoFPS float64
	if videoPath != "" {
		videoFPS = ffmpeg.ProbeVideoFPS(ctx, videoPath)
	}

	// Try known framerate pairs (optionally filtered by actual video FPS).
	// Verify the match produces good alignment; fall through to golden-section
	// search if the known ratio doesn't align well (can happen when two
	// known ratios are close, e.g. 24→25 vs 23.976→25).
	if result, ok := matchKnownRatio(ctx, observedRatio, incorrect, r2, videoFPS, reference); ok {
		if verifyFramerateCorrection(ctx, reference, result.Cues) {
			return result
		}
		slog.Info("framerate correction: known ratio rejected, falling through to golden-section",
			"ratio", result.Rate)
	}

	// Golden-section search for optimal ratio.
	slog.Debug("framerate: falling back to golden-section search",
		"observed_ratio", observedRatio, "intercept_ms", intercept, "r2", r2)
	return goldenSectionSearch(ctx, reference, incorrect, observedRatio, r2)
}

// driftPoint records the measured timing drift at a specific point.
type driftPoint struct {
	timeMs  float64 // position in the timeline (ms)
	driftMs float64 // measured drift at this position (ms)
}

// measureDrifts samples drift at evenly spaced points by comparing
// corresponding cues (by index) between reference and incorrect subtitles.
func measureDrifts(reference, incorrect []Cue) []driftPoint {
	// Sample at ~10 evenly spaced points across the timeline.
	n := min(len(reference), len(incorrect))
	numSamples := min(10, n/2)
	if numSamples < 3 {
		return nil
	}

	step := n / numSamples
	points := make([]driftPoint, 0, numSamples)

	for i := range numSamples {
		idx := i * step
		if idx >= n {
			break
		}
		incMidMs := float64(incorrect[idx].Start.Milliseconds()+incorrect[idx].End.Milliseconds()) / 2.0
		refMidMs := float64(reference[idx].Start.Milliseconds()+reference[idx].End.Milliseconds()) / 2.0

		points = append(points, driftPoint{
			timeMs:  incMidMs,
			driftMs: incMidMs - refMidMs,
		})
	}
	return points
}

// linearRegression delegates to the framerate sub-package's implementation.
func linearRegression(points []driftPoint) (slope, intercept, r2 float64) {
	fps := make([]framerate.DriftPoint, len(points))
	for i, p := range points {
		fps[i] = framerate.DriftPoint{TimeMs: p.timeMs, DriftMs: p.driftMs}
	}
	return framerate.LinearRegression(fps)
}

// matchKnownRatio checks if the observed ratio matches a known framerate pair.
// When videoFPS > 0, only pairs involving that FPS are tried, and confidence
// is boosted (the actual video FPS confirms the match).
// Evaluates all candidates within tolerance and picks the one that produces
// the best alignment score, not just the closest ratio numerically.
func matchKnownRatio(ctx context.Context, observed float64, incorrect []Cue, r2, videoFPS float64, reference []Cue) (SyncResult, bool) {
	candidates := collectRatioCandidates(observed, videoFPS)
	if len(candidates) == 0 {
		return SyncResult{}, false
	}

	// Evaluate alignment quality to pick the best candidate.
	refSpans := cuesToSpans(reference)
	bestPair, bestCues := bestRatioCandidate(ctx, candidates, incorrect, refSpans)
	if bestPair == nil {
		// Context cancelled (or no candidate scored finite); no match.
		return SyncResult{}, false
	}

	corrected := bestCues

	maxConf := float64(DefaultConfidenceCaps.FramerateKnown)
	if videoFPS > 0 {
		maxConf = float64(DefaultConfidenceCaps.FramerateFPS)
	}
	confidence := Confidence(min(r2, maxConf))

	slog.Info("framerate correction: known ratio match",
		"from_fps", bestPair.From,
		"to_fps", bestPair.To,
		"ratio", bestPair.Ratio,
		"observed", observed,
		"video_fps", videoFPS,
		"candidates", len(candidates),
		"confidence", float64(confidence))

	return SyncResult{
		Cues:       corrected,
		Rate:       bestPair.Ratio,
		Confidence: confidence,
		Method:     MethodFramerate,
	}, true
}

// collectRatioCandidates returns the known framerate pairs whose ratio is
// within tolerance of the observed drift ratio. When videoFPS > 0, only pairs
// involving that FPS (within FPSTolerance) are considered.
func collectRatioCandidates(observed, videoFPS float64) []*framerate.RatioPair {
	fpsTolerance := defaultFramerateConfig.FPSTolerance

	var candidates []*framerate.RatioPair
	for i := range framerate.KnownRatios {
		pair := &framerate.KnownRatios[i]

		if videoFPS > 0 {
			fromMatch := math.Abs(pair.From-videoFPS) < fpsTolerance
			toMatch := math.Abs(pair.To-videoFPS) < fpsTolerance
			if !fromMatch && !toMatch {
				continue
			}
		}

		relErr := math.Abs(observed-pair.Ratio) / pair.Ratio
		if relErr < defaultFramerateConfig.RatioTolerance {
			candidates = append(candidates, pair)
		}
	}
	return candidates
}

// bestRatioCandidate scales the incorrect cues by each candidate ratio and
// returns the pair (with its scaled cues) that produces the best alignment
// score. Returns (nil, nil) if the context is cancelled before a candidate is
// evaluated.
func bestRatioCandidate(ctx context.Context, candidates []*framerate.RatioPair, incorrect []Cue, refSpans []TimeSpan) (*framerate.RatioPair, []Cue) {
	var bestPair *framerate.RatioPair
	var bestCues []Cue
	bestScore := math.Inf(-1)

	for _, pair := range candidates {
		if ctx.Err() != nil {
			return nil, nil
		}
		scaled := scaleCues(incorrect, pair.Ratio)
		scaledSpans := cuesToSpans(scaled)
		offset := alignConstantOffset(ctx, refSpans, scaledSpans)
		score := alignmentScore(refSpans, scaledSpans, offset)
		if score > bestScore {
			bestScore = score
			bestPair = pair
			bestCues = scaled
		}
	}
	return bestPair, bestCues
}
