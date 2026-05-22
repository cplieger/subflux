package subsync

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

// SyncOptions configures the sync behavior.
type SyncOptions struct {
	// VideoPath is the path to the video file (required for audio sync).
	VideoPath string

	// AudioHints provides content characteristics for adaptive audio
	// sync strategy selection. Optional; zero value uses defaults.
	AudioHints AudioSyncHints

	// SplitPenalty controls split detection sensitivity (0 = default).
	SplitPenalty float64

	// MinConfidence is the minimum confidence to apply a sync result.
	// Default: 0.5.
	MinConfidence Confidence

	// EnableFramerate enables framerate correction detection.
	EnableFramerate bool

	// EnableSplits enables split-aware DP alignment.
	EnableSplits bool

	// EnableAudio enables audio-based sync (requires video file path).
	EnableAudio bool
}

// DefaultSyncOptions returns sensible defaults.
func DefaultSyncOptions() SyncOptions {
	return SyncOptions{
		EnableFramerate: true,
		EnableSplits:    true,
		EnableAudio:     false, // opt-in; requires video file access
		MinConfidence:   ShouldApplyThreshold,
	}
}

// SyncWithOptions performs multi-strategy subtitle synchronization.
//
// Strategy order:
//  1. If reference subtitle available: try framerate correction, then
//     split-aware alignment, then constant offset
//  2. If no reference but video path provided and audio enabled:
//     try audio-based sync
//
// Returns the best result above the minimum confidence threshold.
func SyncWithOptions(ctx context.Context, reference, incorrect []Cue, opts *SyncOptions) SyncResult {
	if len(incorrect) == 0 {
		return SyncResult{
			Cues:       incorrect,
			Confidence: ConfidenceNone,
			Method:     MethodNone,
		}
	}

	if opts == nil {
		defaults := DefaultSyncOptions()
		opts = &defaults
	}

	if opts.MinConfidence <= 0 {
		opts.MinConfidence = ShouldApplyThreshold
	}

	slog.Debug("sync: starting",
		"ref_cues", len(reference),
		"inc_cues", len(incorrect),
		"framerate", opts.EnableFramerate,
		"splits", opts.EnableSplits,
		"audio", opts.EnableAudio,
		"min_confidence", float64(opts.MinConfidence))

	var best SyncResult

	// Strategy 1: Reference-based sync.
	if len(reference) > 0 {
		best = referenceSync(ctx, reference, incorrect, opts)
		if best.Confidence >= opts.MinConfidence {
			return best
		}
	}

	// Strategy 2: Audio-based sync (no reference needed).
	if opts.EnableAudio && opts.VideoPath != "" {
		audioResult := audioSync(ctx, incorrect, opts.VideoPath, opts.AudioHints)
		if audioResult.Confidence > best.Confidence {
			best = audioResult
		}
	}

	// If nothing worked well enough, return original cues with the best
	// result we found (caller checks ShouldApply).
	if best.Confidence < opts.MinConfidence {
		if best.Cues == nil {
			best.Cues = incorrect
		}
		if best.Method == "" {
			best.Method = MethodNone
		}
		slog.Debug("sync: no strategy met confidence threshold",
			"best_method", best.Method,
			"best_confidence", float64(best.Confidence),
			"min_confidence", float64(opts.MinConfidence),
			"cues", len(incorrect))
	}
	return best
}

// referenceSync tries all reference-based sync strategies concurrently
// and returns the best result.
//
// All four strategies are independent and stateless; running them in
// parallel with errgroup cuts wall-clock latency by roughly the slowest
// individual strategy (typically CorrectFramerate, which blocks on an
// ffprobe subprocess) instead of sum-of-all when run sequentially.
func referenceSync(ctx context.Context, reference, incorrect []Cue, opts *SyncOptions) SyncResult {
	// Pre-allocate the candidates slice; each strategy slot is one entry,
	// each goroutine writes to its own index, then we filter zeros below.
	// This avoids a mutex and keeps the goroutine bodies simple.
	const numStrategies = 4
	candidates := make([]SyncResult, numStrategies)

	g, gctx := errgroup.WithContext(ctx)

	// Strategy 1: Cross-language anchor matching. (pure CPU)
	g.Go(func() error {
		candidates[0] = crossLangAlign(gctx, reference, incorrect)
		return nil
	})

	// Strategy 2: Framerate correction. (I/O via ffprobe + CPU)
	if opts.EnableFramerate {
		g.Go(func() error {
			candidates[1] = correctFramerate(gctx, reference, incorrect, opts.VideoPath)
			return nil
		})
	}

	// Strategy 3: Constant offset (alass). (pure CPU)
	g.Go(func() error {
		cues, offset := syncCues(gctx, reference, incorrect)
		r := SyncResult{
			Cues:   cues,
			Offset: offset.Milliseconds(),
			Rate:   1.0,
			Method: MethodOffset,
		}
		r.Confidence = constantOffsetConfidence(reference, incorrect, offset)
		candidates[2] = r
		return nil
	})

	// Strategy 4: Split-aware alignment. (pure CPU)
	if opts.EnableSplits {
		g.Go(func() error {
			candidates[3] = alignWithSplits(gctx, reference, incorrect, opts.SplitPenalty)
			return nil
		})
	}

	// Strategies set their own ctx-cancellation handling internally;
	// errgroup's Wait collects them all (no error returns are emitted
	// by these strategies — they signal failure via Confidence=0).
	_ = g.Wait() //nolint:errcheck // goroutines always return nil

	// Filter out zero-confidence results.
	live := candidates[:0]
	for _, c := range candidates {
		if c.Confidence > ConfidenceNone {
			live = append(live, c)
		}
	}

	if len(live) == 0 {
		slog.Debug("reference sync: all strategies returned zero confidence",
			"ref_cues", len(reference),
			"inc_cues", len(incorrect),
			"framerate_enabled", opts.EnableFramerate,
			"splits_enabled", opts.EnableSplits)
		return SyncResult{
			Cues:       incorrect,
			Confidence: ConfidenceNone,
			Method:     MethodNone,
		}
	}

	// Vote: strategies that agree on a similar offset reinforce each other.
	winner := voteOnCandidates(live, reference, incorrect)

	slog.Info("sync voting complete",
		"candidates", len(live),
		"winner", winner.Method,
		"offset_ms", winner.Offset,
		"confidence", float64(winner.Confidence))

	return winner
}

// voteCluster groups sync strategy results with similar offsets.
// constantOffsetConfidence estimates confidence for a constant offset sync
// by measuring how well the shifted subtitles overlap with the reference.
func constantOffsetConfidence(reference, incorrect []Cue, offset time.Duration) Confidence {
	if len(reference) == 0 || len(incorrect) == 0 {
		return ConfidenceNone
	}

	refSpans := cuesToSpans(reference)
	shifted := ShiftCues(incorrect, offset)
	shiftedSpans := cuesToSpans(shifted)

	// Two-pointer overlap computation. Both span slices are sorted by time
	// (CuesToSpans preserves cue order, cues are time-ordered).
	var totalOverlap, totalRef float64
	j := 0
	for _, r := range refSpans {
		refLen := float64(r.End - r.Start)
		totalRef += refLen

		// Advance j past spans that end before this ref span starts.
		for j < len(shiftedSpans) && shiftedSpans[j].End <= r.Start {
			j++
		}
		// Note: only checks one shifted span per ref span. This is correct
		// for non-overlapping subtitle cues where each ref maps to at most
		// one shifted cue after constant-offset alignment.
		if j < len(shiftedSpans) {
			overlap := overlapMs(r, shiftedSpans[j])
			if overlap > 0 {
				totalOverlap += overlap
			}
		}
	}

	if totalRef == 0 {
		return ConfidenceNone
	}

	ratio := totalOverlap / totalRef
	if ratio > 1.0 {
		ratio = 1.0
	}
	return Confidence(ratio * float64(DefaultConfidenceCaps.ForMethod(MethodOffset))) // cap at offset confidence for offset-only sync
}
