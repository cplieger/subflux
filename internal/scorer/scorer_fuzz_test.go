package scorer

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// tierRank maps tiers to ordinal values for comparison.
var tierRank = map[api.ScoreTier]int{
	api.TierNone:       0,
	api.TierMinimal:    1,
	api.TierAcceptable: 2,
	api.TierGood:       3,
	api.TierExcellent:  4,
}

func FuzzScore(f *testing.F) {
	f.Add(true, true, true, true, true, true, true, true, true)
	f.Add(false, false, false, false, false, false, false, false, false)
	f.Add(true, false, false, false, false, false, false, false, true)

	e := New(&api.DefaultScores)

	f.Fuzz(func(t *testing.T, hash, src, rg, ss, vc, hdr, ed, sp, verifiable bool) {
		matches := api.MatchSet{
			Hash:             hash,
			Source:           src,
			ReleaseGroup:     rg,
			StreamingService: ss,
			VideoCodec:       vc,
			HDR:              hdr,
			Edition:          ed,
			SeasonPack:       sp,
		}
		sub := api.SubtitleInfo{HashVerifiable: verifiable}
		score, scoreNoHash := e.Score(sub, matches)

		if score < 0 {
			t.Fatalf("score = %d, want >= 0", score)
		}
		if scoreNoHash < 0 {
			t.Fatalf("scoreNoHash = %d, want >= 0", scoreNoHash)
		}
		if scoreNoHash > score {
			t.Fatalf("scoreNoHash=%d > score=%d", scoreNoHash, score)
		}
	})
}

func FuzzScoreToTier(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(20)
	f.Add(50)
	f.Add(80)
	f.Add(100)
	f.Add(-1)

	e := New(&api.DefaultScores)

	f.Fuzz(func(t *testing.T, score int) {
		tier := e.ScoreToTier(score)
		switch tier {
		case api.TierExcellent, api.TierGood, api.TierAcceptable, api.TierMinimal, api.TierNone:
		default:
			t.Fatalf("ScoreToTier(%d) = %q, unknown tier", score, tier)
		}
	})
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
		tierLo := e.ScoreToTier(lo)
		tierHi := e.ScoreToTier(hi)
		if tierRank[tierLo] > tierRank[tierHi] {
			t.Fatalf("monotonicity violated: score %d → %s (rank %d), score %d → %s (rank %d)",
				lo, tierLo, tierRank[tierLo], hi, tierHi, tierRank[tierHi])
		}
	})
}
