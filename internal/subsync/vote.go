package subsync

import (
	"cmp"
	"log/slog"
	"slices"
)

// --- Arbitration (voting) ---
//
// The arbitration layer selects the best sync result from the reference
// strategies' candidates. Candidates are validated by their corrected cues,
// clustered order-canonically with complete linkage, and compared on one
// normalized alignment rating. The winner's calibrated Confidence is
// returned untouched — the two-value contract: the rating selects, the
// calibrated confidence gates (sync_min_confidence, audio fallback).
//
// Audio-based sync never passes through here; it runs OUTSIDE the vote as a
// fallback after the reference winner misses the caller's gate.

// CorrectedCueAgreementMs is the maximum per-cue timing difference, over ALL
// corrected cue starts and ends in milliseconds, for two candidates to count
// as agreeing. 1500ms is the package's crosslang anchor-agreement scale; the
// value is validated against the golden corpus (see corpusbench_test.go).
const CorrectedCueAgreementMs = 1500

// voteCluster groups candidates whose corrected cues agree.
type voteCluster struct {
	members []SyncResult
}

// isValidCandidate reports whether a candidate is safe to arbitrate:
// corrected cues present, index-parallel with the incorrect input, and
// monotonic (non-decreasing starts). Every voted generator corrects the
// same incorrect slice cue-for-cue; the guard protects the pairwise
// corrected-cue comparison from a future generator that drops, merges, or
// reorders cues.
func isValidCandidate(c *SyncResult, wantLen int) bool {
	if c.Cues == nil || len(c.Cues) != wantLen {
		return false
	}
	for i := 1; i < len(c.Cues); i++ {
		if c.Cues[i].Start < c.Cues[i-1].Start {
			return false
		}
	}
	return true
}

// filterValidCandidates drops malformed candidates before arbitration,
// logging each reject. Filters in place; the returned slice aliases
// candidates. See isValidCandidate for the invariant.
func filterValidCandidates(candidates []SyncResult, incorrect []Cue) []SyncResult {
	valid := candidates[:0]
	for i := range candidates {
		if isValidCandidate(&candidates[i], len(incorrect)) {
			valid = append(valid, candidates[i])
			continue
		}
		slog.Warn("sync vote: dropping malformed candidate",
			"source", candidates[i].Source.String(),
			"method", candidates[i].Method,
			"cues", len(candidates[i].Cues),
			"want_cues", len(incorrect))
	}
	return valid
}

// correctedCuesAgree reports whether two corrected cue slices agree within
// CorrectedCueAgreementMs at EVERY cue start and end. Full comparison, not
// a sample: a sampled scheme can miss a short mis-corrected segment, and
// the cost is bounded (at most 6 candidate pairs x cue count).
func correctedCuesAgree(a, b []Cue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if absMsDelta(a[i].Start.Milliseconds(), b[i].Start.Milliseconds()) > CorrectedCueAgreementMs ||
			absMsDelta(a[i].End.Milliseconds(), b[i].End.Milliseconds()) > CorrectedCueAgreementMs {
			return false
		}
	}
	return true
}

// absMsDelta returns |a-b| for two millisecond values.
func absMsDelta(a, b int64) int64 { return abs64(a - b) }

// candidatesAgree is the cluster-membership predicate: agreement is always
// decided by the corrected-cue predicate (R2.1). For two pure-shift
// candidates the retained ClusterMs pre-grouping acts first as a cheap
// early REJECTION — for constant shifts of the same input the per-cue
// delta is the declared shift delta (modulo zero-clamping), so a pair
// beyond ClusterMs is a different hypothesis family. Passing the prefilter
// never grants membership: every pair still needs correctedCuesAgree.
func candidatesAgree(a, b *SyncResult) bool {
	if a.Transform.Kind == TransformShift && b.Transform.Kind == TransformShift &&
		abs64(a.Transform.Shift-b.Transform.Shift) > defaultVoteConfig.ClusterMs {
		return false
	}
	return correctedCuesAgree(a.Cues, b.Cues)
}

// clusterCandidates groups candidates by corrected-cue agreement. The input
// is first copied and sorted into canonical CandidateSource order, making
// clustering order-canonical regardless of strategy completion order. It
// then applies complete linkage: a candidate joins a cluster only if it
// agrees with EVERY existing member (threshold agreement is non-transitive;
// single linkage would chain disagreeing members through an intermediate).
// Tie-break: the first eligible cluster in canonical order.
func clusterCandidates(candidates []SyncResult) []voteCluster {
	sorted := make([]SyncResult, len(candidates))
	copy(sorted, candidates)
	slices.SortStableFunc(sorted, func(a, b SyncResult) int {
		return cmp.Compare(a.Source, b.Source)
	})

	var clusters []voteCluster
	for i := range sorted {
		placed := false
		for j := range clusters {
			if clusterAccepts(&clusters[j], &sorted[i]) {
				clusters[j].members = append(clusters[j].members, sorted[i])
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, voteCluster{members: []SyncResult{sorted[i]}})
		}
	}
	return clusters
}

// clusterAccepts reports whether candidate c agrees with every member of
// the cluster (complete linkage).
func clusterAccepts(cl *voteCluster, c *SyncResult) bool {
	for i := range cl.members {
		if !candidatesAgree(&cl.members[i], c) {
			return false
		}
	}
	return true
}

// alignmentRating scores how well ALREADY-APPLIED corrected cues overlap
// the reference: overlapped reference time over total reference time,
// normalized to [0,1]. Unlike the generators' internal scores, no hidden
// offset is fitted — the cues are compared exactly as the candidate
// corrected them. The rating orders candidates; it is never written into
// SyncResult.Confidence.
func alignmentRating(reference, corrected []Cue) float64 {
	corrSpans := cuesToSpans(corrected)
	refSpans := cuesToSpans(reference)
	totalOverlap, totalRef := overlapTotal(corrSpans, refSpans)
	if totalRef == 0 {
		return 0
	}
	return min(totalOverlap/totalRef, 1.0)
}

// plausibleCandidate reports whether a candidate passes the large-offset
// plausibility prior: on similar-duration content, a pure shift beyond
// LargeOffsetMs is implausible. Non-shift transforms are always plausible
// (framerate and split corrections carry no single headline shift). The
// guard is rating-independent and cluster-internal: it restricts which
// member may represent a cluster (R3.3's retained plausibility prior on
// shift winners), never the cross-cluster order, the rating, or the
// calibrated confidence.
func plausibleCandidate(c *SyncResult, similarDuration bool) bool {
	if !similarDuration || c.Transform.Kind != TransformShift {
		return true
	}
	return abs64(c.Transform.Shift) <= defaultVoteConfig.LargeOffsetMs
}

// clusterScore is the ordinal ranking key for one cluster: the validated
// cluster size and the best member with its alignment rating.
type clusterScore struct {
	best   *SyncResult
	rating float64
	size   int
}

// scoreCluster evaluates one cluster: its size and its best member —
// highest alignment rating, restricted to members passing the plausibility
// guard when the cluster has any (the guard's only seat: it filters which
// member may represent the cluster, never the cross-cluster order), with
// ties resolving to the earliest member in canonical source order.
func scoreCluster(cl *voteCluster, reference []Cue, similarDuration bool) clusterScore {
	s := clusterScore{size: len(cl.members)}
	anyPlausible := false
	for i := range cl.members {
		if plausibleCandidate(&cl.members[i], similarDuration) {
			anyPlausible = true
			break
		}
	}
	for i := range cl.members {
		m := &cl.members[i]
		if anyPlausible && !plausibleCandidate(m, similarDuration) {
			continue
		}
		r := alignmentRating(reference, m.Cues)
		if s.best == nil || r > s.rating {
			s.best = m
			s.rating = r
		}
	}
	return s
}

// outranks reports whether s beats o in the settled ordinal winner ladder
// (R3.2): validated-cluster size first (genuine corrected-cue consensus),
// then best member rating. Equal keys keep the incumbent, so the first
// cluster in canonical order wins ties (the source-order tie-break).
func (s clusterScore) outranks(o clusterScore) bool {
	if s.size != o.size {
		return s.size > o.size
	}
	return s.rating > o.rating
}

// pickWinner selects the winning candidate ordinally across clusters. The
// returned member is unchanged: its Confidence is exactly the calibrated
// value its generator produced (two-value contract).
func pickWinner(clusters []voteCluster, reference []Cue, similarDuration bool) SyncResult {
	best := scoreCluster(&clusters[0], reference, similarDuration)
	for i := 1; i < len(clusters); i++ {
		if s := scoreCluster(&clusters[i], reference, similarDuration); s.outranks(best) {
			best = s
		}
	}
	return *best.best
}

// voteOnCandidates selects the best sync result from validated candidates:
//
//  1. Copy and sort the candidates into canonical source order
//  2. Cluster by corrected-cue agreement with complete linkage
//  3. Rank clusters ordinally: validated size, then best member rating
//     (the plausibility guard restricts member selection within a cluster)
//  4. Return the winning member with its calibrated Confidence untouched
//
// Callers must pre-filter candidates with filterValidCandidates.
func voteOnCandidates(candidates []SyncResult, reference, incorrect []Cue) SyncResult {
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Invariant: reference and incorrect are non-empty (checked by
	// SyncWithOptions and referenceSync before reaching here).
	refEndMs := reference[len(reference)-1].End.Milliseconds()
	incEndMs := incorrect[len(incorrect)-1].End.Milliseconds()
	similarDuration := abs64(refEndMs-incEndMs) < defaultVoteConfig.SimilarDurationMs

	clusters := clusterCandidates(candidates)
	return pickWinner(clusters, reference, similarDuration)
}

// abs64 returns the absolute value of an int64.
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// voteConfig holds the tuning parameters for the arbitration layer.
type voteConfig struct {
	// ClusterMs is the scalar pre-grouping shortcut threshold for the
	// pure-shift candidate family (see candidatesAgree).
	ClusterMs int64
	// LargeOffsetMs is the shift plausibility guard threshold: on
	// similar-duration content a shift beyond this is implausible.
	LargeOffsetMs int64
	// SimilarDurationMs classifies reference/incorrect as similar-duration
	// content, arming the plausibility guard.
	SimilarDurationMs int64
}

// defaultVoteConfig contains the production tuning values for arbitration.
var defaultVoteConfig = voteConfig{
	ClusterMs:         3000,
	LargeOffsetMs:     30_000,
	SimilarDurationMs: 60_000,
}
