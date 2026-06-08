package subsync

import "log/slog"

// --- Voting system ---
//
// The voting system selects the best sync result from multiple strategy
// candidates using weighted clustering and validation heuristics.

// voteCluster groups sync results with similar offsets.
type voteCluster struct {
	members []SyncResult
	weight  float64
	offset  int64 // representative offset (from first member)
}

// buildClusters groups candidates by offset similarity (within clusterMs).
func buildClusters(candidates []SyncResult, clusterMs int64) []voteCluster {
	var clusters []voteCluster
	for _, c := range candidates {
		placed := false
		for i := range clusters {
			if abs64(c.Offset-clusters[i].offset) <= clusterMs {
				clusters[i].members = append(clusters[i].members, c)
				clusters[i].weight += float64(c.Confidence)
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, voteCluster{
				members: []SyncResult{c},
				weight:  float64(c.Confidence),
				offset:  c.Offset,
			})
		}
	}
	return clusters
}

// applyAgreementBonus boosts clusters with multiple agreeing strategies.
func applyAgreementBonus(clusters []voteCluster) {
	for i := range clusters {
		n := len(clusters[i].members)
		if n >= 3 {
			clusters[i].weight *= defaultVoteConfig.AgreementBonus3Plus
		} else if n >= 2 {
			clusters[i].weight *= defaultVoteConfig.AgreementBonus2
		}
	}
}

// penalizeLargeOffsets reduces weight for clusters with unreasonably large
// offsets when reference and incorrect have similar total duration.
func penalizeLargeOffsets(clusters []voteCluster, similarDuration bool, durationDiff int64) {
	for i := range clusters {
		if similarDuration && abs64(clusters[i].offset) > defaultVoteConfig.LargeOffsetMs {
			clusters[i].weight *= defaultVoteConfig.LargeOffsetPenalty
			slog.Debug("sync vote: penalizing large offset",
				"cluster_offset_ms", clusters[i].offset,
				"duration_diff_ms", durationDiff,
				"members", len(clusters[i].members))
		}
	}
}

// applyAlignmentCheck boosts or penalizes clusters based on how well the
// offset aligns the first and last (90th percentile) cues.
func applyAlignmentCheck(clusters []voteCluster, reference, incorrect []Cue) {
	for i := range clusters {
		off := clusters[i].offset
		// Check first cue alignment.
		shiftedFirstMs := incorrect[0].Start.Milliseconds() + off
		firstDiff := abs64(shiftedFirstMs - reference[0].Start.Milliseconds())
		// Check last cue alignment (ignoring credits: use 90th percentile).
		incLast := incorrect[len(incorrect)*9/10].Start.Milliseconds() + off
		refLast := reference[len(reference)*9/10].Start.Milliseconds()
		lastDiff := abs64(incLast - refLast)

		if firstDiff < defaultVoteConfig.AlignFirstGood && lastDiff < defaultVoteConfig.AlignLastGood {
			clusters[i].weight *= defaultVoteConfig.AlignGoodBoost
		} else if firstDiff > defaultVoteConfig.AlignFirstBad || lastDiff > defaultVoteConfig.AlignLastBad {
			clusters[i].weight *= defaultVoteConfig.AlignBadPenalty
		}
	}
}

// pickWinner selects the highest-weighted cluster, then the best member
// within it by confidence.
func pickWinner(clusters []voteCluster) SyncResult {
	bestCluster := 0
	for i := 1; i < len(clusters); i++ {
		if clusters[i].weight > clusters[bestCluster].weight {
			bestCluster = i
		}
	}

	best := clusters[bestCluster].members[0]
	for _, m := range clusters[bestCluster].members[1:] {
		if m.Confidence > best.Confidence {
			best = m
		}
	}
	return best
}

// voteOnCandidates selects the best sync result using weighted voting.
//
// The voting system:
//  1. Group candidates by offset similarity (within 3s = same cluster)
//  2. Each cluster's weight = sum of member confidences + agreement bonus
//  3. Apply hard validation: reject clusters with unreasonable offsets
//  4. Return the highest-weighted cluster's best member
func voteOnCandidates(candidates []SyncResult,
	reference, incorrect []Cue,
) SyncResult {
	if len(candidates) == 1 {
		return candidates[0]
	}

	clusterMs := defaultVoteConfig.ClusterMs

	// Invariant: reference and incorrect are non-empty (checked by
	// SyncWithOptions and referenceSync before reaching here).
	refEndMs := reference[len(reference)-1].End.Milliseconds()
	incEndMs := incorrect[len(incorrect)-1].End.Milliseconds()
	durationDiff := abs64(refEndMs - incEndMs)
	similarDuration := durationDiff < defaultVoteConfig.SimilarDurationMs

	clusters := buildClusters(candidates, clusterMs)
	applyAgreementBonus(clusters)
	penalizeLargeOffsets(clusters, similarDuration, durationDiff)
	applyAlignmentCheck(clusters, reference, incorrect)
	return pickWinner(clusters)
}

// abs64 returns the absolute value of an int64.
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// voteConfig holds all tuning parameters for the voting system.
// Centralizes the multipliers and thresholds so the rule set is inspectable
// and independently tunable.
type voteConfig struct {
	ClusterMs           int64
	AgreementBonus2     float64 // 2 strategies agree
	AgreementBonus3Plus float64 // 3+ strategies agree
	LargeOffsetPenalty  float64
	AlignGoodBoost      float64
	AlignBadPenalty     float64
	LargeOffsetMs       int64 // penalize clusters with offsets exceeding this
	AlignFirstGood      int64 // first-cue alignment "good" threshold
	AlignLastGood       int64 // last-cue alignment "good" threshold
	AlignFirstBad       int64 // first-cue alignment "bad" threshold
	AlignLastBad        int64 // last-cue alignment "bad" threshold
	SimilarDurationMs   int64 // threshold for "similar duration" classification
}

// defaultVoteConfig contains the production tuning values for the
// voting system. Each multiplier reflects empirical tuning:
// - Agreement bonuses reward convergence across independent strategies
// - Large offset penalty discourages implausible shifts
// - Alignment boost/penalty validates structural consistency
var defaultVoteConfig = voteConfig{
	ClusterMs:           3000,
	AgreementBonus2:     1.25,
	AgreementBonus3Plus: 1.5,
	LargeOffsetPenalty:  0.3,
	AlignGoodBoost:      1.2,
	AlignBadPenalty:     0.5,
	LargeOffsetMs:       30_000,
	AlignFirstGood:      5_000,
	AlignLastGood:       10_000,
	AlignFirstBad:       30_000,
	AlignLastBad:        60_000,
	SimilarDurationMs:   60_000,
}
