package scorer

import (
	"testing"

	"subflux/internal/api"
)

func FuzzScore(f *testing.F) {
	f.Add(10, 5, 3, 2, 4, 1, 7, 100, true, true, true, true, true, true, true, true, true)
	f.Add(0, 0, 0, 0, 0, 0, 0, 0, false, false, false, false, false, false, false, false, false)
	f.Fuzz(func(t *testing.T,
		releaseGroup, source, streamingService, edition, videoCodec, hdr, seasonPack, hash int,
		mRG, mSrc, mSS, mEd, mVC, mHDR, mSP, mHash, hashVerifiable bool) {

		scores := &api.Scores{
			ReleaseGroup:     releaseGroup,
			Source:           source,
			StreamingService: streamingService,
			Edition:          edition,
			VideoCodec:       videoCodec,
			HDR:              hdr,
			SeasonPack:       seasonPack,
			Hash:             hash,
		}
		e := New(scores)
		sub := api.SubtitleInfo{HashVerifiable: hashVerifiable}
		matches := api.MatchSet{
			ReleaseGroup:     mRG,
			Source:           mSrc,
			StreamingService: mSS,
			Edition:          mEd,
			VideoCodec:       mVC,
			HDR:              mHDR,
			SeasonPack:       mSP,
			Hash:             mHash,
		}
		// Must not panic.
		_, _ = e.Score(nil, sub, matches)
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
	f.Fuzz(func(t *testing.T, score int) {
		scores := &api.Scores{Hash: 100}
		e := New(scores)
		// Must not panic.
		tier := e.ScoreToTier(score, api.MediaTypeMovie)
		_ = tier
	})
}
