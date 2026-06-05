package subsource

import "testing"

func FuzzMatchTitle(f *testing.F) {
	f.Add("Breaking Bad", 2008, "breaking bad", 2008, 101)
	f.Add("The Office", 0, "office", 2005, 42)
	f.Add("", 0, "", 0, 0)
	f.Add("Inception", 2010, "inception 2010", 2010, 7)
	f.Add("日本語", 2020, "日本語", 2020, 1)

	f.Fuzz(func(t *testing.T, title string, year int, resTitle string, resYear, resID int) {
		if year < 0 || year > 2100 || resYear < 0 || resYear > 2100 || resID < 0 {
			return
		}
		data := []searchResult{
			{Title: resTitle, ReleaseYear: FlexInt(resYear), MovieID: resID},
		}
		// Must not panic.
		got := matchTitle(data, title, year)
		// If year is specified and doesn't match, result must be 0.
		if year != 0 && resYear != year && got != 0 {
			t.Fatalf("matchTitle returned %d when years differ: want=%d got=%d", got, year, resYear)
		}
	})
}

func FuzzIso2ToSubSource(f *testing.F) {
	f.Add("en")
	f.Add("fr")
	f.Add("zh")
	f.Add("")
	f.Add("xx")
	f.Add("eng")
	f.Add("pb")

	f.Fuzz(func(t *testing.T, code string) {
		// Must not panic.
		_ = iso2ToSubSource(code)
	})
}

func FuzzBuildSubtitles(f *testing.F) {
	f.Add("commentary", "rel1", 1, true, false, true, "en", 1, 5)
	f.Add("", "", 42, false, false, false, "fr", 0, 0)
	f.Add("forced", "name", 7, false, true, false, "es", 2, 3)
	f.Add("", "", 0, false, false, false, "", 0, 0)

	f.Fuzz(func(t *testing.T, commentary, relInfo string, subID int, hi, foreign, foreignParts bool, lang string, season, episode int) {
		if subID < 0 || season < 0 || season > 99 || episode < 0 || episode > 999 {
			return
		}
		var releases []string
		if relInfo != "" {
			releases = []string{relInfo}
		}
		items := []subtitleItem{
			{
				Language:     lang,
				Commentary:   commentary,
				ReleaseInfo:  releases,
				SubtitleID:   subID,
				HearingImp:   hi,
				ForeignParts: foreignParts,
			},
		}
		// Must not panic.
		subs := buildSubtitles(items, lang, season, episode)
		// Invariant: forced/foreignParts items are excluded.
		if foreignParts && len(subs) > 0 {
			t.Fatal("buildSubtitles should exclude foreignParts items")
		}
		for _, s := range subs {
			if s.Season != season || s.Episode != episode {
				t.Fatalf("season/episode mismatch: got %d/%d want %d/%d", s.Season, s.Episode, season, episode)
			}
		}
	})
}
