package opensubtitles

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

func TestNumberings(t *testing.T) {
	ep := func(season, episode, absolute int) provider.Query {
		return provider.Query{Kind: media.Episode, Numbering: media.Numbering{Scheme: media.TVDB, Season: season, Episode: episode}, Absolute: absolute}
	}
	for _, tc := range []struct {
		name string
		q    provider.Query
		want []numbering
	}{
		{"a movie asks once", provider.Query{Kind: media.Movie, Absolute: 7}, []numbering{{}}},
		{"aired only", ep(2, 5, 0), []numbering{{2, 5}}},
		{"absolute as season 1", ep(2, 5, 18), []numbering{{2, 5}, {1, 18}}},
		{"absolute equal to the aired numbering", ep(1, 5, 5), []numbering{{1, 5}}},
		{"specials never ask the absolute number", ep(0, 1, 6), []numbering{{0, 1}}},
		{"a negative absolute number is none", ep(1, 2, -3), []numbering{{1, 2}}},
	} {
		if got := numberings(&tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIDParams(t *testing.T) {
	en, _ := languages.Wire(mustTag(t, "en"))
	for _, tc := range []struct {
		name    string
		q       provider.Query
		useHash bool
		n       numbering
		want    string // "" for no id
	}{
		{"movie by TMDB first", provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: 27205, IMDb: 1375666}}, false, numbering{}, "ai_translated=exclude&languages=en&tmdb_id=27205"},
		{"movie by IMDb without TMDB", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}}, false, numbering{}, "ai_translated=exclude&imdb_id=1375666&languages=en"},
		{"episode by the series IMDb id, never TMDB", provider.Query{Kind: media.Episode, External: media.ExternalIDs{TMDB: 1396, IMDb: 903747}}, false, numbering{1, 2}, "ai_translated=exclude&episode_number=2&languages=en&parent_imdb_id=903747&season_number=1"},
		{"episode without IMDb has no id", provider.Query{Kind: media.Episode, External: media.ExternalIDs{TMDB: 1396, TVDB: 81189}}, false, numbering{1, 2}, ""},
		{"no ids", provider.Query{Kind: media.Movie}, true, numbering{}, ""},
		{"zero season and episode left out", provider.Query{Kind: media.Episode, External: media.ExternalIDs{IMDb: 1}}, false, numbering{}, "ai_translated=exclude&languages=en&parent_imdb_id=1"},
		{"hash when on", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}, Hash: provider.VideoHash{OSHash: 0x8e245d9679d31e12}}, true, numbering{}, "ai_translated=exclude&imdb_id=1&languages=en&moviehash=8e245d9679d31e12"},
		{"no hash when off", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}, Hash: provider.VideoHash{OSHash: 1}}, false, numbering{}, "ai_translated=exclude&imdb_id=1&languages=en"},
		{"no hash when unknown", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}}, true, numbering{}, "ai_translated=exclude&imdb_id=1&languages=en"},
	} {
		a := &adapter{useHash: tc.useHash}
		p, ok := a.idParams(&tc.q, []string{en}, tc.n)
		if got := p.Encode(); ok != (tc.want != "") || got != tc.want {
			t.Errorf("%s: %q %v, want %q", tc.name, got, ok, tc.want)
		}
	}
}

// The answer's hearing_impaired and foreign_parts_only flags are independent:
// languages design 3.2 makes every combination of HI and Forced valid.
func TestCandidateFlags(t *testing.T) {
	en := mustTag(t, "en")
	for _, tc := range []struct {
		name  string
		hi    bool
		parts bool
		want  language.Flags
	}{
		{"neither", false, false, language.Flags{}},
		{"hearing impaired", true, false, language.Flags{HI: true}},
		{"foreign parts only", false, true, language.Flags{Forced: true}},
		{"both", true, true, language.Flags{HI: true, Forced: true}},
	} {
		raw := fmt.Sprintf(`{"language": "en", "release": "Inception.2010.1080p.BluRay.x264",
			"files": [{"file_id": 1, "file_name": "Inception.2010.1080p.BluRay.x264.srt"}],
			"hearing_impaired": %t, "foreign_parts_only": %t}`, tc.hi, tc.parts)
		var at attributes
		if err := json.Unmarshal([]byte(raw), &at); err != nil {
			t.Fatal(err)
		}
		c, ok := (&adapter{}).candidate(&at, []language.Tag{en}, rank.ID)
		if !ok || c.Flags != tc.want {
			t.Errorf("%s: %+v %v, want flags %+v", tc.name, c.Flags, ok, tc.want)
		}
	}
}

func mustTag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

func TestUntilUTCMidnight(t *testing.T) {
	paris := time.FixedZone("CET", 3600)
	for _, tc := range []struct {
		now  time.Time
		want time.Duration
	}{
		{time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), 24 * time.Hour},
		{time.Date(2026, 11, 6, 23, 30, 0, 0, time.UTC), 30 * time.Minute},
		{time.Date(2026, 11, 6, 23, 59, 59, 999_000_000, time.UTC), time.Second},
		{time.Date(2026, 11, 7, 0, 30, 0, 0, paris), 30 * time.Minute},
		{time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC), 12 * time.Hour},
	} {
		if got := untilUTCMidnight(tc.now); got != tc.want {
			t.Errorf("untilUTCMidnight(%v) = %v, want %v", tc.now, got, tc.want)
		}
	}
}
