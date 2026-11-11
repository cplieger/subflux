package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

func FuzzNormalizeTitle_idempotent(f *testing.F) {
	for _, s := range []string{"Breaking Bad", "the.office.us", "Movie-Name_2024", "", "A:B:C", "   spaces   ", "日本語タイトル", "UPPER.case-MiXeD"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		once := normalizeTitle(s)
		if twice := normalizeTitle(once); once != twice {
			t.Fatalf("normalizeTitle(%q) = %q, then %q", s, once, twice)
		}
	})
}

func FuzzTitlesMatch_symmetric(f *testing.F) {
	f.Add("Breaking Bad", "breaking bad")
	f.Add("The Office", "the.office")
	f.Add("", "anything")
	f.Add("Show", "Show 2")
	f.Fuzz(func(t *testing.T, a, b string) {
		if titlesMatch(a, b) != titlesMatch(b, a) {
			t.Fatalf("titlesMatch(%q, %q) is not symmetric", a, b)
		}
	})
}

// FuzzIdentify_covers checks that an id lookup's Covers decide an
// episode's identity whatever the release name: Covers holding the item
// never contradict it, Covers missing it always do.
func FuzzIdentify_covers(f *testing.F) {
	for _, s := range []string{"Show.S01E02", "[G] Show S2 - 05", "Show.S03.1080p", "", "S01E01E02E03"} {
		f.Add(s, 1, 1)
	}
	f.Fuzz(func(t *testing.T, release string, season, episode int) {
		s := episodes([]string{"Show"}, 0, media.Numbering{Season: 1, Episode: 1})
		item := media.Numbering{Season: season, Episode: episode}
		c := provider.Candidate{Release: release, MatchedBy: rank.ID, Covers: []media.Numbering{item}}
		got, _ := identify(s, &c)
		if hit := season == 1 && episode == 1; (got == identityContradicted) == hit {
			t.Fatalf("identify(%q, Covers %v) = %d", release, item, got)
		}
	})
}
