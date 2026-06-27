package crosslang

import (
	"cmp"
	"context"
	"log/slog"
	"math"
	"slices"
	"time"
)

// config holds all tuning parameters for cross-language alignment.
type config struct {
	NormWindow      float64
	TopK            int
	Pass2WindowMs   int64
	AgreementMs     int64
	DPMaxPred       int
	ConfBase        float64
	ConfBonus20     float64
	ConfCap         float64
	ConfPenaltyLow5 float64
}

var defaultConfig = config{
	NormWindow:      0.10,
	TopK:            7,
	Pass2WindowMs:   8_000,
	AgreementMs:     1500,
	DPMaxPred:       300,
	ConfBase:        0.88,
	ConfBonus20:     0.05,
	ConfCap:         0.92,
	ConfPenaltyLow5: 0.5,
}

// MinCuesForSync is the minimum number of cues required for alignment.
// Must match subsync.MinCuesForSync — kept as a const here to avoid a
// circular import between subsync and crosslang.
const MinCuesForSync = 5

// CuePair holds a matched pair of cues with their similarity score.
// Exported for test compatibility with the parent subsync package.
type CuePair struct {
	IncIdx   int
	RefIdx   int
	Score    float64
	OffsetMs int64
}

// Align finds the best constant offset between reference and incorrect
// subtitles using cross-language anchor matching.
func Align(ctx context.Context, reference, incorrect []Cue) Result {
	noResult := Result{
		Cues:       incorrect,
		Confidence: 0,
	}

	if len(reference) < MinCuesForSync || len(incorrect) < MinCuesForSync {
		slog.Debug("crosslang: too few cues",
			"reference", len(reference),
			"incorrect", len(incorrect))
		return noResult
	}

	refAnchors := extractAllAnchors(reference)
	incAnchors := extractAllAnchors(incorrect)

	refStrong, _ := markStrong(refAnchors)
	incStrong, incStrongCount := markStrong(incAnchors)

	if incStrongCount < 3 {
		slog.Debug("crosslang: too few anchored cues",
			"anchored", incStrongCount)
		return noResult
	}

	refDurMs := reference[len(reference)-1].End.Milliseconds()
	incDurMs := incorrect[len(incorrect)-1].End.Milliseconds()
	if refDurMs <= 0 || incDurMs <= 0 {
		slog.Debug("crosslang: zero or negative duration",
			"ref_dur_ms", refDurMs,
			"inc_dur_ms", incDurMs)
		return noResult
	}

	cfg := defaultConfig

	pass1Candidates := gatherCandidates(
		ctx, incorrect, reference, incAnchors, refAnchors, incStrong, refStrong,
		func(incStartMs, refStartMs int64) (bool, float64) {
			incNorm := float64(incStartMs) / float64(incDurMs)
			refNorm := float64(refStartMs) / float64(refDurMs)
			normDist := math.Abs(refNorm - incNorm)
			return normDist <= cfg.NormWindow, normDist / cfg.NormWindow
		},
		cfg.TopK,
	)

	if len(pass1Candidates) < 3 {
		slog.Debug("crosslang: too few pass1 candidates",
			"candidates", len(pass1Candidates))
		return noResult
	}

	roughAligned := dpAlign(pass1Candidates)
	if len(roughAligned) < 3 {
		slog.Debug("crosslang: pass1 DP too sparse",
			"aligned", len(roughAligned))
		return noResult
	}
	roughOffset := weightedMedianOffset(roughAligned)

	pass2Candidates := gatherCandidates(
		ctx, incorrect, reference, incAnchors, refAnchors, incStrong, refStrong,
		func(incStartMs, refStartMs int64) (bool, float64) {
			expectedRefMs := incStartMs + roughOffset
			rawDist := abs64(refStartMs - expectedRefMs)
			return rawDist <= cfg.Pass2WindowMs, float64(rawDist) / float64(cfg.Pass2WindowMs)
		},
		cfg.TopK,
	)

	pairs := pass2Candidates
	if len(pairs) < len(pass1Candidates)/3 {
		pairs = pass1Candidates
	}

	aligned := dpAlign(pairs)
	if len(aligned) < 3 {
		slog.Debug("crosslang: final DP too sparse",
			"aligned", len(aligned))
		return noResult
	}

	medianOffset := weightedMedianOffset(aligned)
	confidence := computeConfidence(aligned, medianOffset)

	slog.Info("crosslang alignment",
		"pass1_candidates", len(pass1Candidates),
		"pass2_candidates", len(pass2Candidates),
		"rough_offset_ms", roughOffset,
		"dp_aligned", len(aligned),
		"offset_ms", medianOffset,
		"confidence", confidence)

	if confidence < 0.3 {
		return noResult
	}

	offset := time.Duration(medianOffset) * time.Millisecond
	shifted := shiftCues(incorrect, offset)

	return Result{
		Cues:       shifted,
		Offset:     medianOffset,
		Rate:       1.0,
		Confidence: confidence,
	}
}

// extractAllAnchors extracts language-independent anchors for every cue.
func extractAllAnchors(cues []Cue) []anchor {
	anchors := make([]anchor, len(cues))
	for i := range cues {
		anchors[i] = extractAnchors(cues[i].Text)
	}
	return anchors
}

// markStrong flags each anchor that carries at least one matchable feature
// (number, proper noun, or cognate) and returns how many were flagged.
func markStrong(anchors []anchor) (strong []bool, count int) {
	strong = make([]bool, len(anchors))
	for i := range anchors {
		strong[i] = hasAnyAnchor(&anchors[i])
		if strong[i] {
			count++
		}
	}
	return strong, count
}

func shiftCues(cues []Cue, offset time.Duration) []Cue {
	out := make([]Cue, len(cues))
	for i, c := range cues {
		out[i] = Cue{
			Text:  c.Text,
			Start: c.Start + offset,
			End:   c.End + offset,
		}
	}
	return out
}

func computeConfidence(aligned []CuePair, medianOffset int64) float64 {
	cfg := defaultConfig
	var agreeCount int
	var agreeWeight, totalWeight float64
	for _, p := range aligned {
		totalWeight += p.Score
		if abs64(p.OffsetMs-medianOffset) <= cfg.AgreementMs {
			agreeCount++
			agreeWeight += p.Score
		}
	}
	if totalWeight == 0 {
		return 0
	}
	weightRatio := agreeWeight / totalWeight
	confidence := weightRatio * cfg.ConfBase
	if agreeCount >= 20 {
		confidence = math.Min(confidence+cfg.ConfBonus20, cfg.ConfCap)
	}
	if agreeCount < 5 {
		confidence *= cfg.ConfPenaltyLow5
	}
	return confidence
}

type scored struct {
	refIdx   int
	score    float64
	offsetMs int64
}

type windowFunc func(incStartMs, refStartMs int64) (inWindow bool, normDist float64)

type strongRef struct {
	startMs int64
	origIdx int
}

func gatherCandidates(
	ctx context.Context,
	incorrect, reference []Cue,
	incAnchors, refAnchors []anchor,
	incStrong, refStrong []bool,
	inWindow windowFunc,
	topK int,
) []CuePair {
	refs := collectStrongRefs(reference, refStrong)

	var candidates []CuePair
	for i := range incorrect {
		if ctx.Err() != nil {
			return nil
		}
		if !incStrong[i] {
			continue
		}
		incStartMs := incorrect[i].Start.Milliseconds()
		for _, c := range scoredCandidatesForCue(i, incStartMs, refs, incAnchors, refAnchors, inWindow, topK) {
			candidates = append(candidates, CuePair{
				IncIdx: i, RefIdx: c.refIdx,
				Score: c.score, OffsetMs: c.offsetMs,
			})
		}
	}
	return candidates
}

// collectStrongRefs gathers the strongly-anchored reference cues sorted by
// start time, ready for windowed binary search.
func collectStrongRefs(reference []Cue, refStrong []bool) []strongRef {
	var refs []strongRef
	for j := range reference {
		if refStrong[j] {
			refs = append(refs, strongRef{startMs: reference[j].Start.Milliseconds(), origIdx: j})
		}
	}
	slices.SortFunc(refs, func(a, b strongRef) int {
		return cmp.Compare(a.startMs, b.startMs)
	})
	return refs
}

// scoredCandidatesForCue scores every in-window reference anchor against the
// incorrect cue at index i and returns the top-K matches by combined score.
func scoredCandidatesForCue(
	i int,
	incStartMs int64,
	refs []strongRef,
	incAnchors, refAnchors []anchor,
	inWindow windowFunc,
	topK int,
) []scored {
	span := estimateMaxWindowMs(refs, incStartMs, inWindow)
	lo := findFirstGE(refs, incStartMs-span)
	hi := min(findFirstGE(refs, incStartMs+span+1), len(refs))

	var cands []scored
	for k := lo; k < hi; k++ {
		ok, normDist := inWindow(incStartMs, refs[k].startMs)
		if !ok {
			continue
		}
		s := anchorMatchScore(&incAnchors[i], &refAnchors[refs[k].origIdx])
		if s < defaultAnchorScoreConfig.MinScore {
			continue
		}
		posFactor := 1.0 - normDist
		combined := s*(1.0-defaultAnchorScoreConfig.PositionBlend) + defaultAnchorScoreConfig.PositionBlend*posFactor
		cands = append(cands, scored{refs[k].origIdx, combined, refs[k].startMs - incStartMs})
	}

	slices.SortFunc(cands, func(a, b scored) int {
		return cmp.Compare(b.score, a.score)
	})
	if len(cands) > topK {
		cands = cands[:topK]
	}
	return cands
}

func findFirstGE(refs []strongRef, target int64) int {
	lo, hi := 0, len(refs)
	for lo < hi {
		mid := (lo + hi) / 2
		if refs[mid].startMs < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func estimateMaxWindowMs(refs []strongRef, incStartMs int64, inWindow windowFunc) int64 {
	if len(refs) == 0 {
		return 0
	}
	totalMs := refs[len(refs)-1].startMs
	totalMs = max(totalMs, 1)
	upper := totalMs
	lo, hi := int64(0), upper
	for lo < hi {
		mid := (lo + hi + 1) / 2
		ok, _ := inWindow(incStartMs, incStartMs+mid)
		if ok {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + lo/10 + 1000
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- DP alignment ---

var dpMaxPredecessors = defaultConfig.DPMaxPred

// WeightedMedianOffset computes the weighted median offset from pairs.
func WeightedMedianOffset(pairs []CuePair) int64 { return weightedMedianOffset(pairs) }

// DPAlign finds the optimal monotonic alignment path.
func DPAlign(pairs []CuePair) []CuePair { return dpAlign(pairs) }

func weightedMedianOffset(pairs []CuePair) int64 {
	if len(pairs) == 0 {
		return 0
	}
	sorted := make([]CuePair, len(pairs))
	copy(sorted, pairs)
	slices.SortFunc(sorted, func(a, b CuePair) int {
		return cmp.Compare(a.OffsetMs, b.OffsetMs)
	})
	var totalWeight float64
	for _, p := range sorted {
		totalWeight += p.Score
	}
	half := totalWeight / 2.0
	var cum float64
	for _, p := range sorted {
		cum += p.Score
		if cum >= half {
			return p.OffsetMs
		}
	}
	return sorted[len(sorted)/2].OffsetMs
}

func dpAlign(pairs []CuePair) []CuePair {
	slices.SortFunc(pairs, compareCuePair)

	n := len(pairs)
	if n == 0 {
		return nil
	}

	dp, parent := computeDP(pairs)

	bestIdx := 0
	for i := 1; i < n; i++ {
		if dp[i] > dp[bestIdx] {
			bestIdx = i
		}
	}

	// idx walks the parent chain back to a root. For a valid alignment the
	// chain is strictly decreasing (parent[i] < i, or -1 at a root), so it
	// visits at most n nodes. The `len(path) < n` guard makes that bound
	// explicit: a corrupted parent array (e.g. a sentinel that is not -1, or
	// any value that forms a cycle) can no longer turn this into an unbounded
	// append that exhausts memory and takes the whole process down.
	var path []CuePair
	for idx := bestIdx; idx >= 0 && len(path) < n; idx = parent[idx] {
		path = append(path, pairs[idx])
	}
	slices.Reverse(path)
	return path
}

// compareCuePair orders pairs by IncIdx, then RefIdx — the canonical order the
// DP fill assumes.
func compareCuePair(a, b CuePair) int {
	if a.IncIdx != b.IncIdx {
		return cmp.Compare(a.IncIdx, b.IncIdx)
	}
	return cmp.Compare(a.RefIdx, b.RefIdx)
}

// computeDP runs the longest-increasing-path DP over the sorted pairs and
// returns the best accumulated score and the parent chain for each node. Each
// node looks back only over a bounded predecessor window (dpMaxPredecessors).
func computeDP(pairs []CuePair) (dp []float64, parent []int) {
	n := len(pairs)
	dp = make([]float64, n)
	parent = make([]int, n)
	for i := range n {
		parent[i] = -1
		dp[i] = pairs[i].Score
		start := max(0, i-dpMaxPredecessors)
		for j := i - 1; j >= start; j-- {
			if pairs[j].IncIdx >= pairs[i].IncIdx || pairs[j].RefIdx >= pairs[i].RefIdx {
				continue
			}
			if candidate := dp[j] + pairs[i].Score; candidate > dp[i] {
				dp[i] = candidate
				parent[i] = j
			}
		}
	}
	return dp, parent
}
