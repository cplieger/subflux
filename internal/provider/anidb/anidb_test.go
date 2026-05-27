package anidb

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestFindInMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		list      *animeList
		name      string
		tvdbID    int
		season    int
		episode   int
		wantAniDB int
		wantEp    int
	}{
		{
			name: "direct_season_match",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 100, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
			}},
			tvdbID: 999, season: 1, episode: 5, wantAniDB: 100, wantEp: 5,
		},
		{
			name: "with_episode_offset",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 200, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
				{AniDBID: 201, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 12},
			}},
			tvdbID: 999, season: 1, episode: 15, wantAniDB: 201, wantEp: 3,
		},
		{
			name: "no_match",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 100, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
			}},
			tvdbID: 888, season: 1, episode: 1, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "wrong_season",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 100, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
			}},
			tvdbID: 999, season: 2, episode: 1, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "absolute_mapping_with_text",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 300, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 1, Text: ";1-1;2-2;3-3;"}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 2, wantAniDB: 300, wantEp: 2,
		},
		{
			name: "absolute_text_no_match_falls_to_offset",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 400, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 1, Text: ";1-1;2-2;", Offset: 0}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 5, wantAniDB: 400, wantEp: 5,
		},
		{
			name: "absolute_no_season_match",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 500, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 2, Text: ";1-1;"}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 1, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "episode_at_offset_boundary",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 600, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 5},
			}},
			tvdbID: 999, season: 1, episode: 5, wantAniDB: 0, wantEp: 0,
		},
		{
			name:   "empty_list",
			list:   &animeList{},
			tvdbID: 999, season: 1, episode: 1, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "multiple_tvdb_ids_only_one_matches",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 100, TVDBID: "888", DefaultTVDBSeason: "1", EpisodeOffset: 0},
				{AniDBID: 200, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
				{AniDBID: 300, TVDBID: "777", DefaultTVDBSeason: "1", EpisodeOffset: 0},
			}},
			tvdbID: 999, season: 1, episode: 3, wantAniDB: 200, wantEp: 3,
		},
		{
			name: "absolute_nil_mapping_list",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 700, TVDBID: "999", DefaultTVDBSeason: "a"},
			}},
			tvdbID: 999, season: 1, episode: 1, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "absolute_empty_text_uses_offset",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 800, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 1, Text: "", Offset: 3}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 7, wantAniDB: 800, wantEp: 4,
		},
		{
			name: "absolute_offset_drops_episode_to_zero",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 900, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 1, Text: ";10-10;11-11;", Offset: 5}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 3, wantAniDB: 0, wantEp: 0,
		},
		{
			name: "absolute_episode_equals_offset_rejects",
			list: &animeList{Animes: []animeEntry{
				{AniDBID: 901, TVDBID: "999", DefaultTVDBSeason: "a", MappingList: &mappingList{
					Mappings: []mapping{{TVDBSeason: 1, Text: "", Offset: 5}},
				}},
			}},
			tvdbID: 999, season: 1, episode: 5, wantAniDB: 0, wantEp: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			anidbID, epNo := findInMapping(tt.list, tt.tvdbID, tt.season, tt.episode)
			if anidbID != tt.wantAniDB {
				t.Errorf("findInMapping() anidbID = %d, want %d", anidbID, tt.wantAniDB)
			}
			if epNo != tt.wantEp {
				t.Errorf("findInMapping() epNo = %d, want %d", epNo, tt.wantEp)
			}
		})
	}
}

func TestFindInMapping_unsorted_offsets(t *testing.T) {
	t.Parallel()
	list := &animeList{Animes: []animeEntry{
		{AniDBID: 201, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 12},
		{AniDBID: 200, TVDBID: "999", DefaultTVDBSeason: "1", EpisodeOffset: 0},
	}}

	anidbID, epNo := findInMapping(list, 999, 1, 15)
	if anidbID != 201 || epNo != 3 {
		t.Errorf("ep=15: got (%d, %d), want (201, 3)", anidbID, epNo)
	}
	anidbID2, epNo2 := findInMapping(list, 999, 1, 5)
	if anidbID2 != 200 || epNo2 != 5 {
		t.Errorf("ep=5: got (%d, %d), want (200, 5)", anidbID2, epNo2)
	}
}

func TestFindInMapping_absolute_multiple_season_mappings_text_priority(t *testing.T) {
	t.Parallel()
	list := &animeList{Animes: []animeEntry{{
		AniDBID: 902, TVDBID: "999", DefaultTVDBSeason: "a",
		MappingList: &mappingList{Mappings: []mapping{
			{TVDBSeason: 1, Text: ";1-1;2-2;", Offset: 25},
			{TVDBSeason: 1, Offset: 10},
		}},
	}}}

	anidbID, epNo := findInMapping(list, 999, 1, 20)
	if anidbID != 902 || epNo != 10 {
		t.Errorf("ep=20: got (%d, %d), want (902, 10)", anidbID, epNo)
	}
	anidbID2, epNo2 := findInMapping(list, 999, 1, 2)
	if anidbID2 != 902 || epNo2 != 2 {
		t.Errorf("ep=2: got (%d, %d), want (902, 2)", anidbID2, epNo2)
	}
}

func TestFindEpisodeInMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		text        string
		tvdbEpisode int
		want        int
	}{
		{name: "simple match", text: ";1-1;2-2;3-3;", tvdbEpisode: 2, want: 2},
		{name: "no match", text: ";1-1;2-2;", tvdbEpisode: 5, want: 0},
		{name: "multi-episode plus", text: ";1-1+2;3-3;", tvdbEpisode: 2, want: 1},
		{name: "multi-episode first", text: ";1-1+2;3-3;", tvdbEpisode: 1, want: 1},
		{name: "empty text", text: "", tvdbEpisode: 1, want: 0},
		{name: "malformed no dash", text: ";abc;", tvdbEpisode: 1, want: 0},
		{name: "non-numeric anidb side", text: ";abc-1;", tvdbEpisode: 1, want: 0},
		{name: "non-numeric tvdb side", text: ";1-abc;", tvdbEpisode: 1, want: 0},
		{name: "whitespace around parts", text: "; 1-1 ; 2-2 ;", tvdbEpisode: 2, want: 2},
		{name: "whitespace around dash", text: ";1 - 1;2 - 2;", tvdbEpisode: 2, want: 2},
		{name: "only semicolons", text: ";;;", tvdbEpisode: 1, want: 0},
		{name: "leading zeros", text: ";01-01;02-02;", tvdbEpisode: 2, want: 2},
		{name: "duplicate tvdb episode first wins", text: ";5-3;10-3;", tvdbEpisode: 3, want: 5},
		{name: "single entry no semicolons", text: "1-1", tvdbEpisode: 1, want: 1},
		{name: "plus with three episodes", text: ";1-1+2+3;", tvdbEpisode: 3, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := findEpisodeInMapping(tt.text, tt.tvdbEpisode)
			if got != tt.want {
				t.Errorf("findEpisodeInMapping(%q, %d) = %d, want %d",
					tt.text, tt.tvdbEpisode, got, tt.want)
			}
		})
	}
}

// anidb-002 — property-based tests for findEpisodeInMapping.

func TestFindEpisodeInMapping_never_panics_on_arbitrary_input(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		text := rapid.String().Draw(rt, "text")
		episode := rapid.IntRange(-1000, 1000).Draw(rt, "episode")
		_ = findEpisodeInMapping(text, episode) // must not panic
	})
}

func TestFindEpisodeInMapping_roundtrip_on_well_formed(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// Generate distinct (anidbEp, tvdbEp) pairs.
		n := rapid.IntRange(1, 10).Draw(rt, "n")
		anidbEps := make([]int, n)
		tvdbEps := make([]int, n)
		seenTVDB := make(map[int]bool, n)
		parts := make([]string, 0, n)
		for i := range n {
			anidbEps[i] = rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("a%d", i))
			var tv int
			for {
				tv = rapid.IntRange(1, 500).Draw(rt, fmt.Sprintf("t%d", i))
				if !seenTVDB[tv] {
					seenTVDB[tv] = true
					break
				}
			}
			tvdbEps[i] = tv
			parts = append(parts, fmt.Sprintf("%d-%d", anidbEps[i], tv))
		}
		text := ";" + strings.Join(parts, ";") + ";"

		// Target an existing pair: must return the paired AniDB episode.
		idx := rapid.IntRange(0, n-1).Draw(rt, "idx")
		got := findEpisodeInMapping(text, tvdbEps[idx])
		if got != anidbEps[idx] {
			rt.Errorf("findEpisodeInMapping(%q, %d) = %d, want %d",
				text, tvdbEps[idx], got, anidbEps[idx])
		}

		// Target a TVDB episode outside the generation range: must return 0.
		missing := 501 + rapid.IntRange(0, 100).Draw(rt, "missing_offset")
		if seenTVDB[missing] {
			return // extremely unlikely; guard anyway
		}
		if got := findEpisodeInMapping(text, missing); got != 0 {
			rt.Errorf("findEpisodeInMapping(%q, %d) = %d, want 0 (missing tvdb episode)",
				text, missing, got)
		}
	})
}

// anidb-003 — buildEpisodeCacheKey covers the leading-zero / whitespace
// normalization path that was previously only reachable via live HTTP.

func TestBuildEpisodeCacheKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		epNo     string
		want     string
		seriesID int
	}{
		{name: "numeric simple", epNo: "5", want: "123:5", seriesID: 123},
		{name: "numeric with leading zero", epNo: "05", want: "123:5", seriesID: 123},
		{name: "numeric with leading zeros", epNo: "001", want: "123:1", seriesID: 123},
		{name: "numeric with surrounding whitespace", epNo: "  7  ", want: "123:7", seriesID: 123},
		{name: "numeric zero", epNo: "0", want: "123:0", seriesID: 123},
		{name: "numeric large", epNo: "9999", want: "42:9999", seriesID: 42},
		{name: "special S1", epNo: "S1", want: "123:S1", seriesID: 123},
		{name: "special C1", epNo: "C1", want: "123:C1", seriesID: 123},
		{name: "special T1", epNo: "T1", want: "123:T1", seriesID: 123},
		{name: "non-numeric with whitespace trimmed", epNo: "  S2  ", want: "123:S2", seriesID: 123},
		{name: "empty string falls through to string branch", epNo: "", want: "123:", seriesID: 123},
		{name: "pure whitespace", epNo: "   ", want: "123:", seriesID: 123},
		{name: "mixed alphanumeric", epNo: "3a", want: "123:3a", seriesID: 123},
		{name: "negative-looking numeric", epNo: "-1", want: "123:-1", seriesID: 123},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildEpisodeCacheKey(tt.seriesID, tt.epNo)
			if got != tt.want {
				t.Errorf("buildEpisodeCacheKey(%d, %q) = %q, want %q",
					tt.seriesID, tt.epNo, got, tt.want)
			}
		})
	}
}

func TestBuildEpisodeCacheKey_matches_getEpisodeID_format(t *testing.T) {
	t.Parallel()

	// Regression guard: getEpisodeID builds cache keys via fmt.Sprintf("%d:%d",
	// seriesID, episodeNo). buildEpisodeCacheKey must produce the same string
	// for numeric inputs, otherwise every numeric lookup silently misses.
	seriesID, epNo := 1234, 7
	getKey := fmt.Sprintf("%d:%d", seriesID, epNo)
	if builtKey := buildEpisodeCacheKey(seriesID, strconv.Itoa(epNo)); builtKey != getKey {
		t.Errorf("cache key format drift: built %q, lookup %q", builtKey, getKey)
	}

	// Leading-zero normalization is part of the contract.
	if builtKey := buildEpisodeCacheKey(seriesID, "007"); builtKey != getKey {
		t.Errorf("leading-zero normalization broken: %q != %q", builtKey, getKey)
	}
}
