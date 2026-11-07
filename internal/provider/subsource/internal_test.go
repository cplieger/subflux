package subsource

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

func tagOf(tb testing.TB, s string) language.Tag {
	tb.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		tb.Fatalf("tag %q", s)
	}
	return tg
}

func TestMatchTitle(t *testing.T) {
	shows := []titleHit{
		{MovieID: 100, Title: "Breaking Bad", ReleaseYear: 2008},
		{MovieID: 200, Title: "The Wire", ReleaseYear: 2002},
		{MovieID: 300, Title: "Better Call Saul", ReleaseYear: 2015},
	}
	sequels := []titleHit{
		{MovieID: 1, Title: "The Matrix", ReleaseYear: 1999},
		{MovieID: 2, Title: "The Matrix Reloaded", ReleaseYear: 2003},
		{MovieID: 3, Title: "The Matrix Resurrections", ReleaseYear: 2021},
	}
	unknownYear := []titleHit{{MovieID: 5, Title: "Test Movie"}}
	for _, tc := range []struct {
		name  string
		hits  []titleHit
		title string
		year  int
		want  int
	}{
		{"exact, no year", shows, "Breaking Bad", 0, 100},
		{"exact, its year", shows, "Breaking Bad", 2008, 100},
		{"another year", shows, "Breaking Bad", 2020, 0},
		{"case folded", shows, "breaking bad", 0, 100},
		{"substring", shows, "Wire", 0, 200},
		{"no match", shows, "Dexter", 0, 0},
		{"year picks the sequel", sequels, "Matrix", 2003, 2},
		{"year picks the original", sequels, "Matrix", 1999, 1},
		{"no year takes the first match", sequels, "Matrix", 0, 1},
		{"no hit in that year", sequels, "Matrix", 2010, 0},
		{"unknown year, none asked", unknownYear, "Test", 0, 5},
		{"unknown year, one asked", unknownYear, "Test", 2020, 0},
		{"no hits", nil, "test", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchTitle(tc.hits, tc.title, tc.year); got != tc.want {
				t.Errorf("matchTitle(%q, %d) = %d, want %d", tc.title, tc.year, got, tc.want)
			}
		})
	}
}

func TestFlexInt(t *testing.T) {
	for in, want := range map[string]flexInt{
		`2024`: 2024, `"2015"`: 2015, `"abc"`: 0, `""`: 0, `null`: 0, `0`: 0,
		`20.5`: 0, `{"y":1}`: 0, `" 2015"`: 0, `99999999999999999999`: 0,
	} {
		var got flexInt
		if err := got.UnmarshalJSON([]byte(in)); err != nil || got != want {
			t.Errorf("UnmarshalJSON(%s) = %d, %v, want %d", in, got, err, want)
		}
	}
}

func TestTitleKey(t *testing.T) {
	if titleKey("tt0903747", 1) == titleKey("tt0903747", 2) {
		t.Error("two seasons share one title id")
	}
	if titleKey("tt0000001", 0) == titleKey("tt0000002", 0) {
		t.Error("two IMDb ids share one title id")
	}
}

func TestCandidates(t *testing.T) {
	en := tagOf(t, "en")
	for _, tc := range []struct {
		name  string
		items []item
		want  []string // each candidate's token and release
		hi    []bool
	}{
		{name: "none", items: nil},
		{name: "hearing impaired by flag, commentary or release", items: []item{
			{SubtitleID: 1, HearingImpaired: true, ReleaseInfo: []string{"rel"}},
			{SubtitleID: 2, Commentary: "SDH version", ReleaseInfo: []string{"rel"}},
			{SubtitleID: 3, ReleaseInfo: []string{"Show.S01E01.HI.720p"}},
			{SubtitleID: 4, Commentary: "non-SDH", ReleaseInfo: []string{"rel"}},
		}, want: []string{"1 rel", "2 rel", "3 Show.S01E01.HI.720p", "4 rel"}, hi: []bool{true, true, true, false}},
		{name: "one candidate per release name", items: []item{
			{SubtitleID: 1, ReleaseInfo: []string{"SPARKS", "YIFY", "FGT"}},
		}, want: []string{"1 SPARKS", "1 YIFY", "1 FGT"}, hi: []bool{false, false, false}},
		{name: "no release name", items: []item{{SubtitleID: 7}}, want: []string{"7 "}, hi: []bool{false}},
		{name: "no id left out", items: []item{{SubtitleID: 0, ReleaseInfo: []string{"rel"}}, {SubtitleID: -3}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := candidates(tc.items, en)
			if len(got) != len(tc.want) {
				t.Fatalf("%d candidates, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, c := range got {
				if s := c.Ref.Token() + " " + c.Release; s != tc.want[i] || c.Flags.HI != tc.hi[i] || c.Lang != en {
					t.Errorf("candidate %d: %q HI %v %v, want %q HI %v en", i, s, c.Flags.HI, c.Lang, tc.want[i], tc.hi[i])
				}
			}
		})
	}
}

func TestRefusal(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		kind provider.FailureKind // 0: no failure, no results
	}{
		{"Invalid API key", provider.Auth},
		{"api_key missing", provider.Auth},
		{"Unauthorized", provider.Auth},
		{"You are not authorized", provider.Auth},
		{"No subtitles found", 0},
		{"Movie not found", 0},
		{"no results", 0},
		{"internal error", provider.Permanent},
		{"", provider.Permanent},
	} {
		err := refusal(tc.msg)
		f, _ := errors.AsType[*provider.Failure](err)
		switch {
		case tc.kind == 0 && err != nil:
			t.Errorf("refusal(%q) = %v, want none", tc.msg, err)
		case tc.kind != 0 && (f == nil || f.Kind != tc.kind):
			t.Errorf("refusal(%q) = %v, want kind %v", tc.msg, err, tc.kind)
		}
	}
}

func FuzzMatchTitle(f *testing.F) {
	f.Add("Breaking Bad", 2008, "breaking bad", 2008, 101)
	f.Add("The Office", 0, "office", 2005, 42)
	f.Add("", 0, "", 0, 0)
	f.Add("日本語", 2020, "日本語", 2020, 1)
	f.Fuzz(func(t *testing.T, title string, year int, hitTitle string, hitYear, id int) {
		got := matchTitle([]titleHit{{Title: hitTitle, ReleaseYear: flexInt(hitYear), MovieID: id}}, title, year)
		if got != 0 && got != id {
			t.Fatalf("matchTitle = %d, not the hit's %d", got, id)
		}
		if year != 0 && hitYear != year && got != 0 {
			t.Fatalf("matchTitle took a hit from %d for %d", hitYear, year)
		}
	})
}

func FuzzCandidates(f *testing.F) {
	f.Add("commentary", "rel1", 1, true, false)
	f.Add("", "", 42, false, false)
	f.Add("forced", "name", 7, false, true)
	en := tagOf(f, "en")
	f.Fuzz(func(t *testing.T, commentary, rel string, id int, hi, foreignParts bool) {
		got := candidates([]item{{Commentary: commentary, ReleaseInfo: []string{rel}, SubtitleID: id, HearingImpaired: hi, ForeignParts: foreignParts}}, en)
		if (len(got) == 1) != (id > 0) {
			t.Fatalf("id %d gave %d candidates, want one exactly when the id is positive", id, len(got))
		}
		if len(got) == 1 && (hi && !got[0].Flags.HI || foreignParts && !got[0].Flags.Forced) {
			t.Fatalf("flags %+v dropped the provider's HI %v or foreign-parts %v flag", got[0].Flags, hi, foreignParts)
		}
	})
}
