package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzNormalizeTitle_idempotent verifies NormalizeTitle is idempotent:
// applying it twice yields the same result as applying it once.
func FuzzNormalizeTitle_idempotent(f *testing.F) {
	f.Add("Breaking Bad")
	f.Add("the.office.us")
	f.Add("Movie-Name_2024")
	f.Add("")
	f.Add("A:B:C")
	f.Add("   spaces   everywhere   ")
	f.Add("日本語タイトル")
	f.Add("Attack-on_Titan: Final Season")
	f.Add("  multiple   spaces  ")
	f.Add("UPPER.case-MiXeD")

	f.Fuzz(func(t *testing.T, s string) {
		once := NormalizeTitle(s)
		twice := NormalizeTitle(once)
		if once != twice {
			t.Fatalf("NormalizeTitle not idempotent: %q -> %q -> %q", s, once, twice)
		}
	})
}

// FuzzTitlesMatch_symmetric verifies TitlesMatch is symmetric:
// TitlesMatch(a,b) == TitlesMatch(b,a).
func FuzzTitlesMatch_symmetric(f *testing.F) {
	f.Add("Breaking Bad", "breaking bad")
	f.Add("The Office", "the.office")
	f.Add("", "anything")
	f.Add("anything", "")
	f.Add("", "")
	f.Add("Show", "Show 2")
	f.Add("Inception", "Inception 2010")

	f.Fuzz(func(t *testing.T, a, b string) {
		if TitlesMatch(a, b) != TitlesMatch(b, a) {
			t.Fatalf("TitlesMatch not symmetric for (%q, %q)", a, b)
		}
	})
}

func FuzzEpisodeNumberMatch(f *testing.F) {
	f.Add(1, 5, 1, 5, 0, 0, 0)
	f.Add(0, 0, 1, 1, 0, 0, 0)
	f.Add(1, 1, 2, 3, 0, 0, 0)
	f.Add(1, 25, 1, 25, 0, 1, 25)
	f.Add(0, 0, 0, 0, 0, 0, 0)

	f.Fuzz(func(t *testing.T, subSeason, subEpisode, season, episode, sceneSeason, sceneEpisode, absEpisode int) {
		if subSeason < 0 || subSeason > 99 {
			return
		}
		if subEpisode < 0 || subEpisode > 9999 {
			return
		}
		if season < 0 || season > 99 || episode < 0 || episode > 9999 {
			return
		}
		if sceneSeason < 0 || sceneSeason > 99 || sceneEpisode < 0 || sceneEpisode > 9999 {
			return
		}
		if absEpisode < 0 || absEpisode > 9999 {
			return
		}

		req := &api.SearchRequest{
			Season:          season,
			Episode:         episode,
			SceneSeason:     sceneSeason,
			SceneEpisode:    sceneEpisode,
			AbsoluteEpisode: absEpisode,
		}
		// Must not panic.
		_ = EpisodeNumberMatch(subSeason, subEpisode, req)
	})
}

func FuzzReleaseNameMatchesTitle(f *testing.F) {
	f.Add("Breaking Bad", "Breaking.Bad.S01E01.720p.WEB-DL")
	f.Add("The Office", "The.Office.US.S02E03.HDTV")
	f.Add("Inception", "Inception.2010.1080p.BluRay")
	f.Add("", "")
	f.Add("Show", "Show.II.S01E01")

	f.Fuzz(func(t *testing.T, title, release string) {
		// Manual index arithmetic over the release name must never panic.
		_ = ReleaseNameMatchesTitle(title, release)
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

func FuzzExtractReleaseSeason(f *testing.F) {
	f.Add("Show.S01E05.720p")
	f.Add("Show.S12.Complete")
	f.Add("")
	f.Add("no-season-here")
	f.Add("S99E01")

	f.Fuzz(func(t *testing.T, name string) {
		n := ExtractReleaseSeason(name)
		if n < 0 {
			t.Fatalf("ExtractReleaseSeason(%q) = %d, want >= 0", name, n)
		}
	})
}

// FuzzIsSeasonPackImpliesSeason verifies that if IsSeasonPack returns true,
// ExtractReleaseSeason returns a non-negative season number. Season 0
// (specials) is a legitimate pack — the codebase treats 0 as "unspecified /
// non-constraining" (see scoring.identity_filter, which only applies a
// season constraint when ExtractReleaseSeason > 0), so the guarantee is
// non-negativity, not positivity. This also exercises IsSeasonPack for
// panic-safety on every input.
func FuzzIsSeasonPackImpliesSeason(f *testing.F) {
	f.Add("Show.S01.1080p.BluRay")
	f.Add("Show.S02E01.720p")
	f.Add("random string")
	f.Add("")
	f.Add("S99")

	f.Fuzz(func(t *testing.T, releaseName string) {
		if IsSeasonPack(releaseName) {
			season := ExtractReleaseSeason(releaseName)
			if season < 0 {
				t.Fatalf("IsSeasonPack(%q)=true but ExtractReleaseSeason=%d", releaseName, season)
			}
		}
	})
}
