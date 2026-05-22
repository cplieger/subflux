package subsync

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"golang.org/x/sync/errgroup"
)

// Split detection parameters.
const (
	// defaultSplitPenalty controls sensitivity to split detection.
	// Higher values require larger offset changes to trigger a split.
	// Measured in milliseconds of alignment score penalty per split.
	defaultSplitPenalty = 1000.0

	// minSegmentCues is the minimum number of cues in a segment.
	// Segments shorter than this are merged with their neighbor.
	minSegmentCues = 5

	// maxSplits caps the number of split points to prevent pathological cases.
	maxSplits = 20
)

// segment represents a contiguous group of cues with a consistent offset.
type segment struct {
	startIdx int           // index into the incorrect cue slice
	endIdx   int           // exclusive end index
	offset   time.Duration // best constant offset for this segment
}

// AlignWithSplits performs split-aware alignment between reference and
// incorrect subtitles. It detects points where the timing offset changes
// abruptly (commercial breaks, different cuts) and aligns each segment
// independently.
//
// This is a Go port of the alass align_with_splits algorithm.
//
// Parameters:
//   - reference: correctly timed subtitle cues
//   - incorrect: subtitle cues to be corrected
//   - splitPenalty: cost of introducing a split point (0 = use default)
//
// Returns a SyncResult with the corrected cues and confidence score.
func alignWithSplits(ctx context.Context, reference, incorrect []Cue, splitPenalty float64) SyncResult {
	if len(reference) == 0 || len(incorrect) == 0 {
		return SyncResult{
			Cues:       incorrect,
			Confidence: ConfidenceNone,
			Method:     MethodSplit,
		}
	}

	if splitPenalty <= 0 {
		splitPenalty = defaultSplitPenalty
	}

	refSpans := cuesToSpans(reference)

	// Phase 1: compute per-cue best offsets.
	offsets := perCueOffsets(ctx, refSpans, incorrect)

	// Phase 2: detect split points using DP.
	splits := detectSplits(offsets, splitPenalty)

	if len(splits) <= 1 {
		// No splits detected; fall back to constant offset.
		cues, offset := syncCues(ctx, reference, incorrect)
		return SyncResult{
			Cues:       cues,
			Offset:     offset.Milliseconds(),
			Confidence: ConfidenceModerate,
			Method:     MethodSplit,
		}
	}

	// Phase 3: build segments from split points.
	segments := buildSegments(ctx, refSpans, incorrect, splits)

	// Phase 4: align each segment independently and merge.
	corrected := alignSegments(incorrect, segments)

	// Confidence based on actual overlap quality.
	confidence := segmentConfidence(segments, incorrect, refSpans)

	slog.Info("split-aware alignment complete",
		"segments", len(segments),
		"splits", len(splits)-1,
		"confidence", float64(confidence))

	// Offset is 0 for multi-segment results; per-segment offsets
	// are applied directly to cues. Use Applied() to check.
	return SyncResult{
		Cues:       corrected,
		Confidence: confidence,
		Method:     MethodSplit,
	}
}

// perCueOffset holds the best offset for a single cue.
type perCueOffset struct {
	offsetMs int64
}

// perCueOffsets computes the best constant offset for each incorrect cue
// by testing alignment against all reference spans. For each cue, the
// reference span producing the highest overlap score determines the offset.
//
// The computation is O(n*m) where n=len(incorrect) and m=len(refSpans).
// Since each cue's offset is independent (reads refSpans, writes to its own
// slot), the work is parallelized across CPUs via errgroup.
func perCueOffsets(ctx context.Context, refSpans []TimeSpan, incorrect []Cue) []perCueOffset {
	// Cap inputs to prevent O(n*m) blowup on pathological inputs.
	if len(refSpans) > maxAlignSpans {
		refSpans = refSpans[:maxAlignSpans]
	}
	if len(incorrect) > maxAlignSpans {
		incorrect = incorrect[:maxAlignSpans]
	}

	offsets := make([]perCueOffset, len(incorrect))
	n := len(incorrect)
	if n == 0 {
		return offsets
	}

	numCPU := runtime.NumCPU()
	chunkSize := (n + numCPU - 1) / numCPU

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(numCPU)

	for chunk := range numCPU {
		start := chunk * chunkSize
		if start >= n {
			break
		}
		end := min(start+chunkSize, n)

		g.Go(func() error {
			for i := start; i < end; i++ {
				if err := gctx.Err(); err != nil {
					return err
				}
				cue := incorrect[i]
				incSpan := TimeSpan{
					Start: cue.Start.Milliseconds(),
					End:   cue.End.Milliseconds(),
				}

				var bestScore float64
				var bestOffset int64

				for _, ref := range refSpans {
					offset := ref.Start - incSpan.Start
					shifted := TimeSpan{
						Start: incSpan.Start + offset,
						End:   incSpan.End + offset,
					}
					score := spanScore(ref, shifted)
					if score > bestScore {
						bestScore = score
						bestOffset = offset
					}
				}

				offsets[i] = perCueOffset{offsetMs: bestOffset}
			}
			return nil
		})
	}

	_ = g.Wait()
	return offsets
}

// buildSegments creates segments from split points and computes the best
// offset for each segment.
func buildSegments(ctx context.Context, refSpans []TimeSpan, incorrect []Cue, splits []int) []segment {
	segments := make([]segment, 0, len(splits))

	for i, start := range splits {
		end := len(incorrect)
		if i+1 < len(splits) {
			end = splits[i+1]
		}

		if end-start < minSegmentCues && len(segments) > 0 {
			// Merge tiny segment with previous.
			segments[len(segments)-1].endIdx = end
			continue
		}

		// Compute best offset for this segment.
		segCues := incorrect[start:end]
		segSpans := cuesToSpans(segCues)
		offset := alignConstantOffset(ctx, refSpans, segSpans)

		segments = append(segments, segment{
			startIdx: start,
			endIdx:   end,
			offset:   time.Duration(offset) * time.Millisecond,
		})
	}

	return segments
}

// alignSegments applies per-segment offsets to produce corrected cues.
func alignSegments(incorrect []Cue, segments []segment) []Cue {
	// Copy all cues first; segments should cover every index, but the
	// copy ensures uncovered cues retain original timing as a safety net.
	corrected := make([]Cue, len(incorrect))
	copy(corrected, incorrect)

	for _, seg := range segments {
		for i := seg.startIdx; i < seg.endIdx && i < len(corrected); i++ {
			corrected[i] = Cue{
				Start: max(0, incorrect[i].Start+seg.offset),
				End:   max(0, incorrect[i].End+seg.offset),
				Text:  incorrect[i].Text,
			}
		}
	}
	return corrected
}

// overlapTotal computes the total overlap between corrected and reference
// spans. Both slices must be sorted by start time.
func overlapTotal(corrSpans, refSpans []TimeSpan) (totalOverlap, totalRef float64) {
	var j int
	for _, r := range refSpans {
		totalRef += float64(r.End - r.Start)
		for j < len(corrSpans) && corrSpans[j].End <= r.Start {
			j++
		}
		if j < len(corrSpans) {
			if o := overlapMs(r, corrSpans[j]); o > 0 {
				totalOverlap += o
			}
		}
	}
	return
}

// segmentConfidence computes overall confidence by measuring how well
// the corrected cues actually overlap with the reference.
// This prevents high confidence on garbage segmentations.
func segmentConfidence(segments []segment, incorrect []Cue, refSpans []TimeSpan) Confidence {
	if len(segments) == 0 || len(incorrect) == 0 || len(refSpans) == 0 {
		return ConfidenceNone
	}

	// Apply segment offsets and measure overlap with reference.
	corrected := alignSegments(incorrect, segments)
	corrSpans := cuesToSpans(corrected)

	totalOverlap, totalRef := overlapTotal(corrSpans, refSpans)

	if totalRef == 0 {
		return ConfidenceNone
	}

	overlapRatio := totalOverlap / totalRef
	if overlapRatio > 1.0 {
		overlapRatio = 1.0
	}

	// Penalize complexity: more segments = less confident.
	segPenalty := float64(len(segments)-1) * float64(DefaultConfidenceCaps.SplitPenaltyPerSegment)
	maxConf := float64(DefaultConfidenceCaps.SplitBase) - segPenalty
	if maxConf < float64(DefaultConfidenceCaps.SplitMinConf) {
		maxConf = float64(DefaultConfidenceCaps.SplitMinConf)
	}

	return Confidence(overlapRatio * maxConf)
}
