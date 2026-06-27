package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- matchesPair (season/episode wildcard + equality) ---

func TestMatchesPair_season_logic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		subSeason  int
		subEpisode int
		candSeason int
		candEpis   int
		want       bool
	}{
		{"sub season zero is wildcard", 0, 0, 5, 0, true},
		{"cand season zero is wildcard", 5, 0, 0, 0, true},
		{"equal positive seasons match", 3, 0, 3, 0, true},
		{"different positive seasons do not match", 3, 0, 4, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matchesPair(tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis)
			if got != tc.want {
				t.Errorf("matchesPair(%d,%d,%d,%d) = %v, want %v",
					tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis, got, tc.want)
			}
		})
	}
}

func TestMatchesPair_episode_logic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		subSeason  int
		subEpisode int
		candSeason int
		candEpis   int
		want       bool
	}{
		{"sub episode zero is wildcard", 0, 0, 0, 5, true},
		{"cand episode zero is wildcard", 0, 5, 0, 0, true},
		{"equal positive episodes match", 0, 4, 0, 4, true},
		{"different positive episodes do not match", 0, 4, 0, 6, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := matchesPair(tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis)
			if got != tc.want {
				t.Errorf("matchesPair(%d,%d,%d,%d) = %v, want %v",
					tc.subSeason, tc.subEpisode, tc.candSeason, tc.candEpis, got, tc.want)
			}
		})
	}
}

// --- EpisodeNumberMatch (aired + scene + absolute numbering) ---

func TestEpisodeNumberMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		subSeason  int
		subEpisode int
		req        api.SearchRequest
		want       bool
	}{
		{
			name:       "scene episode matches when aired differs",
			subSeason:  1,
			subEpisode: 5,
			req:        api.SearchRequest{Season: 1, Episode: 99, SceneSeason: 1, SceneEpisode: 5},
			want:       true,
		},
		{
			name:       "scene branch skipped when scene episode zero",
			subSeason:  1,
			subEpisode: 5,
			req:        api.SearchRequest{Season: 1, Episode: 99, SceneSeason: 1, SceneEpisode: 0},
			want:       false,
		},
		{
			name:       "scene season falls back to aired season on mismatch",
			subSeason:  3,
			subEpisode: 5,
			req:        api.SearchRequest{Season: 2, Episode: 9, SceneSeason: 0, SceneEpisode: 5},
			want:       false,
		},
		{
			name:       "absolute episode matches when aired differs",
			subSeason:  1,
			subEpisode: 10,
			req:        api.SearchRequest{Season: 1, Episode: 99, AbsoluteEpisode: 10},
			want:       true,
		},
		{
			name:       "absolute branch skipped when absolute episode zero",
			subSeason:  1,
			subEpisode: 10,
			req:        api.SearchRequest{Season: 1, Episode: 99, AbsoluteEpisode: 0},
			want:       false,
		},
		{
			name:       "absolute season falls back to one on mismatch",
			subSeason:  2,
			subEpisode: 100,
			req:        api.SearchRequest{Season: 2, Episode: 9, AbsoluteEpisode: 100},
			want:       false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EpisodeNumberMatch(tc.subSeason, tc.subEpisode, &tc.req)
			if got != tc.want {
				t.Errorf("EpisodeNumberMatch(%d, %d, %+v) = %v, want %v",
					tc.subSeason, tc.subEpisode, tc.req, got, tc.want)
			}
		})
	}
}

// --- ExtractReleaseSeason ---

func TestExtractReleaseSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		release string
		want    int
	}{
		{"season only", "Show.S05.1080p", 5},
		{"season with episode", "Series.S12E03.720p", 12},
		{"no season marker", "Great.Film.2020.1080p.WEB", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ExtractReleaseSeason(tc.release); got != tc.want {
				t.Errorf("ExtractReleaseSeason(%q) = %d, want %d", tc.release, got, tc.want)
			}
		})
	}
}

// --- ReleaseNameMatchesTitle (word-boundary + sequel-indicator logic) ---

func TestReleaseNameMatchesTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		reqTitle string
		release  string
		want     bool
	}{
		{"empty release matches", "batman", "", true},
		{"empty title matches anything", "", "hello world", true},
		{"title absent from release", "zzz", "hello world", false},
		{"prefix match kept", "batman", "batman begins", true},
		{"non-space before match rejected", "ab", "xab", false},
		{"space before match kept", "ab", "x ab", true},
		{"match exactly at end kept", "batman", "the batman", true},
		{"run-in suffix kept", "dragon ball", "dragon ballz", true},
		{"sequel token after space rejected", "dragon ball", "dragon ball z", false},
		{"title before episode marker matches", "Breaking Bad", "Breaking.Bad.S01E01.720p", true},
		{"different title before episode marker rejected", "The Office", "Breaking.Bad.S01E01.720p", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ReleaseNameMatchesTitle(tc.reqTitle, tc.release); got != tc.want {
				t.Errorf("ReleaseNameMatchesTitle(%q, %q) = %v, want %v",
					tc.reqTitle, tc.release, got, tc.want)
			}
		})
	}
}

// --- TitlesMatch ---

func TestTitlesMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requested string
		candidate string
		want      bool
	}{
		{"equal after normalization", "Breaking Bad", "breaking-bad", true},
		{"distinct titles", "Breaking Bad", "Better Call Saul", false},
		{"empty requested matches", "", "anything", true},
		{"empty candidate matches", "anything", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TitlesMatch(tc.requested, tc.candidate); got != tc.want {
				t.Errorf("TitlesMatch(%q, %q) = %v, want %v", tc.requested, tc.candidate, got, tc.want)
			}
		})
	}
}
