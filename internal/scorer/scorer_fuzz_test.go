package scorer

import (
	"testing"

	"subflux/internal/api"
)

// tierRank maps tiers to ordinal values for comparison.
var tierRank = map[api.ScoreTier]int{
	api.TierNone:       0,
	api.TierMinimal:    1,
	api.TierAcceptable: 2,
	api.TierGood:       3,
	api.TierExcellent:  4,
}

// FuzzScoreToTierMonotonic verifies that ScoreToTier is monotonically
// non-decreasing: a higher score never produces a lower tier.
func FuzzScoreToTierMonotonic(f *testing.F) {
	f.Add(0, 1)
	f.Add(19, 20)
	f.Add(49, 50)
	f.Add(79, 80)
	f.Add(100, 101)
	f.Add(-1, 0)

	e := New(&api.Scores{Hash: 100})

	f.Fuzz(func(t *testing.T, lo, hi int) {
		if lo > hi {
			lo, hi = hi, lo
		}
		tierLo := e.ScoreToTier(lo, api.MediaTypeMovie)
		tierHi := e.ScoreToTier(hi, api.MediaTypeMovie)
		if tierRank[tierLo] > tierRank[tierHi] {
			t.Fatalf("monotonicity violated: score %d → %s (rank %d), score %d → %s (rank %d)",
				lo, tierLo, tierRank[tierLo], hi, tierHi, tierRank[tierHi])
		}
	})
}
