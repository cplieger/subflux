package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func FuzzMatchBreakdown(f *testing.F) {
	f.Add(true, true, true, true, true, true, true, true)
	f.Add(false, false, false, false, false, false, false, false)
	f.Add(true, false, true, false, true, false, true, false)

	f.Fuzz(func(t *testing.T, hash, src, rg, ss, vc, hdr, ed, sp bool) {
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
		scores := &api.DefaultScores
		// Must not panic.
		breakdown := MatchBreakdown(scores, matches)
		// Invariant: if hash is set, breakdown must contain "hash" key.
		if hash && breakdown["hash"] != scores.Hash {
			t.Fatalf("MatchBreakdown: hash set but breakdown[hash]=%d, want %d", breakdown["hash"], scores.Hash)
		}
		// All values must be non-negative.
		for k, v := range breakdown {
			if v < 0 {
				t.Fatalf("MatchBreakdown: negative value for %q: %d", k, v)
			}
		}
	})
}

func FuzzAnyReleaseNameMatches(f *testing.F) {
	f.Add("Breaking Bad", "Breaking.Bad.S01E01.720p.WEB-DL", "")
	f.Add("The Office", "The.Office.US.S02E03.HDTV", "Office US")
	f.Add("", "", "")
	f.Add("Show", "Totally.Different.Release", "Alt Title")

	f.Fuzz(func(t *testing.T, title, releaseName, altTitle string) {
		req := &api.SearchRequest{
			Title: title,
		}
		if altTitle != "" {
			req.AlternativeTitles = []string{altTitle}
		}
		// Must not panic.
		_ = AnyReleaseNameMatches(req, releaseName)
	})
}
