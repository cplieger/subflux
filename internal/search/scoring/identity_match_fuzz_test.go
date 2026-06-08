package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func FuzzNormalizeTitle(f *testing.F) {
	f.Add("Breaking Bad")
	f.Add("the.office.us")
	f.Add("Movie-Name_2024")
	f.Add("")
	f.Add("A:B:C")
	f.Add("   spaces   everywhere   ")
	f.Add("日本語タイトル")

	f.Fuzz(func(t *testing.T, s string) {
		result := NormalizeTitle(s)
		// Idempotent: normalizing again must yield the same result.
		if NormalizeTitle(result) != result {
			t.Fatalf("NormalizeTitle not idempotent for %q", s)
		}
	})
}

func FuzzTitlesMatch(f *testing.F) {
	f.Add("Breaking Bad", "breaking bad")
	f.Add("The Office", "the.office")
	f.Add("", "anything")
	f.Add("anything", "")
	f.Add("", "")
	f.Add("Show", "Show 2")

	f.Fuzz(func(t *testing.T, a, b string) {
		r1 := TitlesMatch(a, b)
		r2 := TitlesMatch(b, a)
		// Symmetry: TitlesMatch(a,b) == TitlesMatch(b,a).
		if r1 != r2 {
			t.Fatalf("TitlesMatch not symmetric: (%q,%q)=%v vs (%q,%q)=%v", a, b, r1, b, a, r2)
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
		// Must not panic.
		_ = ReleaseNameMatchesTitle(title, release)
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

func FuzzIsSeasonPack(f *testing.F) {
	f.Add("Show.S01.Complete.1080p")
	f.Add("Show.S01E01.720p")
	f.Add("")
	f.Add("S03")

	f.Fuzz(func(t *testing.T, name string) {
		// Must not panic.
		_ = IsSeasonPack(name)
	})
}
