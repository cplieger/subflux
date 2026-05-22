package subsync

import (
	"math"
	"slices"
)

// detectSplits uses dynamic programming to find optimal split points.
// The DP minimizes: sum of per-segment variance + splitPenalty * numSplits.
//
// Returns sorted indices where splits should occur (always includes 0).
func detectSplits(offsets []perCueOffset, penalty float64) []int {
	n := len(offsets)
	if n == 0 {
		return nil
	}

	// dp[i] = minimum cost to optimally segment offsets[0:i].
	// parent[i] = start of the last segment ending at i.
	dp := make([]float64, n+1)
	parent := make([]int, n+1)

	for i := 1; i <= n; i++ {
		dp[i] = math.MaxFloat64
	}

	for i := 1; i <= n; i++ {
		for j := max(0, i-maxSegmentLen(n)); j < i; j++ {
			segCost := segmentCost(offsets[j:i])
			splitCost := penalty
			if j == 0 {
				splitCost = 0 // first segment has no split penalty
			}
			total := dp[j] + segCost + splitCost
			if total < dp[i] {
				dp[i] = total
				parent[i] = j
			}
		}
	}

	// Trace back to find split points.
	var splits []int
	for i := n; i > 0; i = parent[i] {
		splits = append(splits, parent[i])
	}
	slices.Reverse(splits)

	// Enforce maxSplits.
	if len(splits) > maxSplits+1 {
		splits = splits[:maxSplits+1]
	}

	return splits
}

// maxSegmentLen returns the maximum segment length to consider in DP.
// Caps at n to avoid O(n³) behavior on large inputs.
func maxSegmentLen(n int) int {
	return min(n, 500)
}

// segmentCost computes the cost of treating a range of offsets as a single
// segment. Uses variance of offsets as the cost metric.
func segmentCost(offsets []perCueOffset) float64 {
	if len(offsets) < 2 {
		return 0
	}

	var sum, sumSq float64
	for _, o := range offsets {
		v := float64(o.offsetMs)
		sum += v
		sumSq += v * v
	}
	n := float64(len(offsets))
	mean := sum / n
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0 // floating-point cancellation when sumSq/n ≈ mean²
	}
	return variance * n // total squared deviation
}
