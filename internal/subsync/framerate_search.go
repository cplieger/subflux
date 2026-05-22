package subsync

import (
	"context"
	"log/slog"
	"math"
	"time"
)

// goldenSectionSearch finds the optimal framerate ratio using the
// golden-section minimization algorithm. Searches around the observed
// ratio for the value that maximizes alignment quality.
func goldenSectionSearch(ctx context.Context, reference, incorrect []Cue, observedRatio, r2 float64) SyncResult {
	// Search range: ±GSSSearchRange around observed ratio.
	lo := observedRatio * (1 - defaultFramerateConfig.GSSSearchRange)
	hi := observedRatio * (1 + defaultFramerateConfig.GSSSearchRange)

	phi := (math.Sqrt(5) - 1) / 2 // golden ratio conjugate

	refSpans := cuesToSpans(reference)

	// Evaluate alignment quality for a given ratio.
	eval := func(ratio float64) float64 {
		scaled := scaleCues(incorrect, ratio)
		scaledSpans := cuesToSpans(scaled)
		// Use the existing alignment score as quality metric.
		// A better ratio produces a higher alignment score.
		offset := alignConstantOffset(ctx, refSpans, scaledSpans)
		return -alignmentScore(refSpans, scaledSpans, offset)
	}

	// Golden-section minimization (we negate to find maximum).
	maxIter := defaultFramerateConfig.GSSMaxIter
	tolerance := defaultFramerateConfig.GSSTolerance

	x1 := hi - phi*(hi-lo)
	x2 := lo + phi*(hi-lo)
	f1 := eval(x1)
	f2 := eval(x2)

	for range maxIter {
		if ctx.Err() != nil {
			break
		}
		if hi-lo < tolerance {
			break
		}
		if f1 < f2 {
			hi = x2
			x2 = x1
			f2 = f1
			x1 = hi - phi*(hi-lo)
			f1 = eval(x1)
		} else {
			lo = x1
			x1 = x2
			f1 = f2
			x2 = lo + phi*(hi-lo)
			f2 = eval(x2)
		}
	}

	bestRatio := (lo + hi) / 2
	if math.Abs(bestRatio-1.0) < 1e-5 {
		// Ratio is essentially 1.0; no framerate correction needed.
		return SyncResult{Rate: 1.0, Confidence: ConfidenceNone, Method: MethodFramerate}
	}

	corrected := scaleCues(incorrect, bestRatio)

	// Confidence is lower for GSS than known ratios (capped via ForMethod).
	confidence := Confidence(min(r2, float64(DefaultConfidenceCaps.ForMethod(MethodFramerate))))

	slog.Info("framerate correction: golden-section search",
		"ratio", bestRatio,
		"confidence", float64(confidence))

	return SyncResult{
		Cues:       corrected,
		Rate:       bestRatio,
		Confidence: confidence,
		Method:     MethodFramerate,
	}
}

// verifyFramerateCorrection checks whether the corrected cues align well
// with the reference. Returns true if the max residual is within tolerance.
func verifyFramerateCorrection(ctx context.Context, reference, corrected []Cue) bool {
	corrSpans := cuesToSpans(corrected)
	refSpans := cuesToSpans(reference)
	offset := alignConstantOffset(ctx, refSpans, corrSpans)

	n := min(len(reference), len(corrected))
	skip := n / 10
	var maxRes int64
	for i := skip; i < n-skip; i++ {
		shifted := corrected[i].Start + time.Duration(offset)*time.Millisecond
		diff := abs64(reference[i].Start.Milliseconds() - shifted.Milliseconds())
		if diff > maxRes {
			maxRes = diff
		}
	}
	return maxRes <= defaultFramerateConfig.MaxResidualMs
}

// alignmentScore computes the total overlap score between reference and
// shifted incorrect spans using a two-pointer sweep. Higher is better.
// Both ref and inc must be sorted by Start (which CuesToSpans guarantees).
func alignmentScore(ref, inc []TimeSpan, offset int64) float64 {
	var total float64
	j := 0
	for _, r := range ref {
		// Advance j past spans that end before this ref starts.
		for j < len(inc) && inc[j].End+offset <= r.Start {
			j++
		}
		// Check all inc spans that could overlap with r.
		for k := j; k < len(inc); k++ {
			shifted := TimeSpan{Start: inc[k].Start + offset, End: inc[k].End + offset}
			if shifted.Start >= r.End {
				break
			}
			overlap := overlapMs(r, shifted)
			if overlap > 0 {
				total += overlap
			}
		}
	}
	return total
}

// overlapMs returns the overlap duration in milliseconds between two spans.
func overlapMs(a, b TimeSpan) float64 {
	start := max(a.Start, b.Start)
	end := min(a.End, b.End)
	if end <= start {
		return 0
	}
	return float64(end - start)
}
