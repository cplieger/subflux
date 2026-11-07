package animemap_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

// index decodes a document holding anime as written by mapread.Encode.
func index(t *testing.T, anime ...mapread.Anime) *mapread.Index {
	t.Helper()
	b, err := mapread.Encode(&mapread.Document{
		Version:     2,
		GeneratedAt: "2026-11-01T00:00:00Z",
		Sources:     mapread.Sources{Mapping: mapread.MappingSource{Revision: "r"}, AniDB: mapread.AniDBSource{TitlesDump: "2026-10-31"}},
		Attribution: mapread.Attribution{License: "ODbL-1.0", LicenseURL: "u", ContentsLicense: "DbCL-1.0", ContentsLicenseURL: "u", Notice: "n"},
		Anime:       anime,
		Entries:     []mapread.Entry{},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := mapread.Decode(bytes.NewReader(b), mapread.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return mapread.NewIndex(doc)
}

func targets(ts ...mapread.Target) *[]mapread.Target { return &ts }

func TestCoversQuerySeriesOnly(t *testing.T) {
	x := index(t, mapread.Anime{
		AniDBID: 1, Type: "TV", TVDBID: 10, Last: mapread.Last{Regular: 1},
		Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TVDB: targets(
			mapread.Target{Season: 1, Episode: 5}, mapread.Target{Season: 1, Episode: 6}, mapread.Target{Series: 11, Season: 0, Episode: 1},
		)}},
	})
	ref := mapread.Ref{AniDB: 1, Kind: mapread.Regular, Number: 1}
	on := func(series uint64) *provider.Query {
		return &provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: series}, Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 5}}
	}
	want := []media.Numbering{{Scheme: media.TVDB, Season: 1, Episode: 5}, {Scheme: media.TVDB, Season: 1, Episode: 6}}
	if got := animemap.Covers(x, ref, on(10)); !slices.Equal(got, want) {
		t.Errorf("series 10 covers %v; want %v", got, want)
	}
	if got := animemap.Covers(x, ref, on(11)); !slices.Equal(got, []media.Numbering{{Scheme: media.TVDB, Season: 0, Episode: 1}}) {
		t.Errorf("series 11 covers %v; want S00E01", got)
	}
	if got := animemap.Covers(x, mapread.Ref{AniDB: 1, Kind: mapread.Regular, Number: 2}, on(10)); got != nil {
		t.Errorf("an episode past the anime's last covers %v", got)
	}
	if got := animemap.Covers(x, ref, &provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: 5, TVDB: 10}}); got != nil {
		t.Errorf("a movie query covers %v", got)
	}
}

func TestKeyed(t *testing.T) {
	tests := []struct {
		name string
		q    provider.Query
		want bool
	}{
		{"tvdb episode", provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: 10}, Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}}, true},
		{"tvdb episode with no series id", provider.Query{Kind: media.Episode, Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}}, false},
		{"tmdb-numbered episode", provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: 10, TMDB: 5}, Numbering: media.Numbering{Scheme: media.TMDB, Season: 1, Episode: 1}}, false},
		{"tmdb movie", provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: 5}}, true},
		{"imdb-only movie", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 5}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := animemap.Keyed(&tt.q); got != tt.want {
				t.Errorf("Keyed %v; want %v", got, tt.want)
			}
		})
	}
}
