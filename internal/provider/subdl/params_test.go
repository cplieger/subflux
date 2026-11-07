package subdl

import (
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

func TestSearchParams(t *testing.T) {
	ids := media.ExternalIDs{IMDb: 1375666, TMDB: 27205}
	cases := []struct {
		name  string
		q     provider.Query
		key   string
		value string
		by    rank.MatchedBy
	}{
		{"movie by IMDb", provider.Query{Kind: media.Movie, External: ids, Titles: []string{"Inception"}}, "imdb_id", "tt1375666", rank.ID},
		{"movie by TMDB", provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: 27205}, Titles: []string{"Inception"}}, "tmdb_id", "27205", rank.ID},
		{"movie by title", provider.Query{Kind: media.Movie, Titles: []string{"Inception", "Origen"}}, "film_name", "Inception", rank.Title},
		{"episode by IMDb", provider.Query{Kind: media.Episode, External: ids}, "imdb_id", "tt1375666", rank.ID},
		{"episode by its series' TMDB id", provider.Query{Kind: media.Episode, External: media.ExternalIDs{TMDB: 1396}, Titles: []string{"Breaking Bad"}}, "tmdb_id", "1396", rank.ID},
		{"episode by title", provider.Query{Kind: media.Episode, Titles: []string{"Breaking Bad"}}, "film_name", "Breaking Bad", rank.Title},
		{"a short IMDb id is padded", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 50083}}, "imdb_id", "tt0050083", rank.ID},
	}
	for _, tc := range cases {
		p, by, ok := searchParams(&tc.q, []string{"EN"})
		if !ok || p.Get(tc.key) != tc.value || by != tc.by {
			t.Errorf("%s: %s by %d ok %v", tc.name, p.Encode(), by, ok)
		}
		for _, other := range []string{"imdb_id", "tmdb_id", "film_name"} {
			if other != tc.key && p.Has(other) {
				t.Errorf("%s: also sends %s", tc.name, other)
			}
		}
	}
	if p, _, ok := searchParams(&provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: 81189}}, []string{"EN"}); ok {
		t.Errorf("an episode with only a TVDB id is searched: %s", p.Encode())
	}
	if p, _, _ := searchParams(&provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}}, []string{"EN"}); p.Has("hi") {
		t.Errorf("sends hi, which narrows the answer to hearing-impaired files: %s", p.Encode())
	}
}

func TestCandidates(t *testing.T) {
	items := []item{
		{Language: "EN", URL: "/subtitle/1-1.zip?api_key=k", ReleaseName: "pack name", Releases: []string{"A", "B"}},
		{Language: "en", URL: "/subtitle/2-2.zip", ReleaseName: "Movie.2020.720p", Comment: "forced subs"},
		{Language: "XX", URL: "/subtitle/3-3.zip"},
		{Language: "FR", URL: "https://elsewhere.example/4.zip"},
		{Language: "FR", URL: "//elsewhere.example/5.zip"},
		{Language: "BR_PT", URL: "/subtitle/6-6.zip", Name: "show_hi_.zip"},
		{Language: "EN", URL: "/subtitle/7-7.zip", EpisodeFrom: 1, EpisodeEnd: 10},
		{Language: "EN", URL: "/subtitle/8-8.zip", FullSeason: true},
	}
	got := candidates(items, true, rank.ID)
	if len(got) != 4 {
		t.Fatalf("%d candidates, want 4: %+v", len(got), got)
	}
	for i, want := range []string{"A", "B"} {
		if c := got[i]; c.Ref.Token() != "/subtitle/1-1.zip" || c.Release != want || c.Flags.HI || c.Flags.Forced {
			t.Errorf("candidate %d %+v, want release %q of file 1", i, c, want)
		}
	}
	if got[2].Ref.Token() != "/subtitle/2-2.zip" || got[2].Release != "Movie.2020.720p" || !got[2].Flags.Forced {
		t.Errorf("third %+v, want the forced file kept, marked, under its release_name", got[2])
	}
	if got[3].Ref.Token() != "/subtitle/6-6.zip" || got[3].Lang.String() != "pt-BR" || !got[3].Flags.HI {
		t.Errorf("fourth %+v", got[3])
	}
	if movie := candidates(items, false, rank.ID); len(movie) != 6 {
		t.Errorf("a movie keeps %d, want 6 with the packs", len(movie))
	}
}

func TestDownloadPath(t *testing.T) {
	for link, want := range map[string]string{
		"/subtitle/1-2.zip?api_key=k": "/subtitle/1-2.zip",
		"/subtitle/1-2.zip":           "/subtitle/1-2.zip",
		"https://dl.subdl.com/x.zip":  "",
		"//dl.subdl.com/x.zip":        "",
		"subtitle/x.zip":              "",
		"mailto:x":                    "",
		"%zz":                         "",
	} {
		got, ok := downloadPath(link)
		if got != want || ok != (want != "") {
			t.Errorf("downloadPath(%q) = %q, %v; want %q", link, got, ok, want)
		}
	}
}

func FuzzDownloadPath(f *testing.F) {
	f.Add("/subtitle/1-2.zip?api_key=k")
	f.Add("//x")
	f.Fuzz(func(t *testing.T, link string) {
		p, ok := downloadPath(link)
		if ok && (len(p) == 0 || p[0] != '/' || (len(p) > 1 && p[1] == '/')) {
			t.Fatalf("downloadPath(%q) = %q", link, p)
		}
	})
}
