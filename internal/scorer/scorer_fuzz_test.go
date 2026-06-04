package scorer

import (
	"testing"

	"subflux/internal/api"
)

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
		score, scoreNoHash := e.Score(nil, sub, matches)

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
		tier := e.ScoreToTier(score, api.MediaTypeMovie)
		switch tier {
		case api.TierExcellent, api.TierGood, api.TierAcceptable, api.TierMinimal, api.TierNone:
		default:
			t.Fatalf("ScoreToTier(%d) = %q, unknown tier", score, tier)
		}
	})
}
