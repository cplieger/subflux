package clisearch

import (
	"testing"

	"subflux/internal/api"
)

func TestParseTmdbID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"valid integer", "12345", 12345},
		{"zero", "0", 0},
		{"empty string returns zero", "", 0},
		{"non-numeric returns zero", "abc", 0},
		{"negative number", "-1", -1},
		{"large number", "999999", 999999},
		{"float string returns zero", "1.5", 0},
		{"whitespace returns zero", " 42 ", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseTmdbID(tt.input)
			if got != tt.want {
				t.Errorf("parseTmdbID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEpisodesForSeries(t *testing.T) {
	t.Parallel()

	series := &api.Series{
		Title:  "Breaking Bad",
		Year:   2008,
		ImdbID: "tt0903747",
		TvdbID: 81189,
	}

	episodes := []api.Episode{
		{
			SeasonNumber:  1,
			EpisodeNumber: 1,
			HasFile:       true,
			EpisodeFile:   &api.EpisodeFile{Path: "/tv/bb/s01e01.mkv", SceneName: "BB.S01E01"},
		},
		{
			SeasonNumber:  1,
			EpisodeNumber: 2,
			HasFile:       true,
			EpisodeFile:   &api.EpisodeFile{Path: "/tv/bb/s01e02.mkv", SceneName: "BB.S01E02"},
		},
		{
			SeasonNumber:  1,
			EpisodeNumber: 3,
			HasFile:       false,
			EpisodeFile:   nil,
		},
		{
			SeasonNumber:  2,
			EpisodeNumber: 1,
			HasFile:       true,
			EpisodeFile:   &api.EpisodeFile{Path: "/tv/bb/s02e01.mkv", SceneName: "BB.S02E01"},
		},
		{
			SeasonNumber:  2,
			EpisodeNumber: 2,
			HasFile:       true,
			EpisodeFile:   &api.EpisodeFile{Path: "/tv/bb/s02e02.mkv", SceneName: ""},
		},
	}

	tests := []struct {
		name          string
		seasonFilter  int
		episodeFilter int
		wantCount     int
		wantFirst     string
	}{
		{
			name:      "no filters returns all episodes with files",
			wantCount: 4,
			wantFirst: "BB.S01E01",
		},
		{
			name:         "season filter returns only matching season",
			seasonFilter: 1,
			wantCount:    2,
			wantFirst:    "BB.S01E01",
		},
		{
			name:         "season filter for season 2",
			seasonFilter: 2,
			wantCount:    2,
			wantFirst:    "BB.S02E01",
		},
		{
			name:          "season and episode filter returns single episode",
			seasonFilter:  1,
			episodeFilter: 2,
			wantCount:     1,
			wantFirst:     "BB.S01E02",
		},
		{
			name:         "non-matching season returns empty",
			seasonFilter: 99,
			wantCount:    0,
		},
		{
			name:          "non-matching episode returns empty",
			seasonFilter:  1,
			episodeFilter: 99,
			wantCount:     0,
		},
		{
			name:          "episode filter without season filter matches across seasons",
			episodeFilter: 1,
			wantCount:     2,
			wantFirst:     "BB.S01E01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := episodesForSeries(series, episodes, tt.seasonFilter, tt.episodeFilter)

			if len(got) != tt.wantCount {
				t.Fatalf("episodesForSeries(seasonFilter=%d, episodeFilter=%d) returned %d items, want %d",
					tt.seasonFilter, tt.episodeFilter, len(got), tt.wantCount)
			}
			if tt.wantCount > 0 {
				if got[0].SceneName != tt.wantFirst {
					t.Errorf("first item sceneName = %q, want %q",
						got[0].SceneName, tt.wantFirst)
				}
				if got[0].Title != "Breaking Bad" {
					t.Errorf("title = %q, want %q", got[0].Title, "Breaking Bad")
				}
				if got[0].ImdbID != "tt0903747" {
					t.Errorf("imdbID = %q, want %q", got[0].ImdbID, "tt0903747")
				}
				if got[0].TvdbID != 81189 {
					t.Errorf("tvdbID = %d, want %d", got[0].TvdbID, 81189)
				}
				if got[0].MediaType != "episode" {
					t.Errorf("mediaType = %q, want %q", got[0].MediaType, "episode")
				}
			}
		})
	}
}

func TestEpisodesForSeries_skips_episodes_without_files(t *testing.T) {
	t.Parallel()

	series := &api.Series{Title: "Test", ImdbID: "tt0000001"}
	episodes := []api.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, HasFile: false, EpisodeFile: nil},
		{SeasonNumber: 1, EpisodeNumber: 2, HasFile: true, EpisodeFile: nil},
		{SeasonNumber: 1, EpisodeNumber: 3, HasFile: false, EpisodeFile: &api.EpisodeFile{Path: "/x"}},
	}

	got := episodesForSeries(series, episodes, 0, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 items for episodes without valid files, got %d", len(got))
	}
}

func TestEpisodesForSeries_empty_episodes(t *testing.T) {
	t.Parallel()

	series := &api.Series{Title: "Test", ImdbID: "tt0000001"}
	got := episodesForSeries(series, nil, 0, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 items for nil episodes, got %d", len(got))
	}
}

func TestMatchSeries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		series api.Series
		imdbID string
		title  string
		want   bool
	}{
		{
			name:   "match by imdb ID",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			imdbID: "tt0903747",
			want:   true,
		},
		{
			name:   "match by title case insensitive",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			title:  "breaking bad",
			want:   true,
		},
		{
			name:   "no match when both empty",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			want:   false,
		},
		{
			name:   "no match wrong imdb",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			imdbID: "tt9999999",
			want:   false,
		},
		{
			name:   "no match wrong title",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			title:  "Better Call Saul",
			want:   false,
		},
		{
			name:   "imdb match takes priority even with wrong title",
			series: api.Series{ImdbID: "tt0903747", Title: "Breaking Bad"},
			imdbID: "tt0903747",
			title:  "Wrong Title",
			want:   true,
		},
		{
			name:   "empty imdb on series does not match empty search imdb",
			series: api.Series{ImdbID: "", Title: "Show"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := matchSeries(&tt.series, tt.imdbID, tt.title)
			if got != tt.want {
				t.Errorf("matchSeries(%+v, %q, %q) = %v, want %v",
					tt.series, tt.imdbID, tt.title, got, tt.want)
			}
		})
	}
}

func TestMatchMovie(t *testing.T) {
	t.Parallel()

	movie := &api.Movie{
		Title:  "Inception",
		ImdbID: "tt1375666",
		TmdbID: 27205,
	}

	tests := []struct {
		name    string
		imdbID  string
		tmdbInt int
		title   string
		want    bool
	}{
		{"match by imdb", "tt1375666", 0, "", true},
		{"match by tmdb", "", 27205, "", true},
		{"match by title case insensitive", "", 0, "inception", true},
		{"no match all empty", "", 0, "", false},
		{"no match wrong imdb", "tt9999999", 0, "", false},
		{"no match wrong tmdb", "", 99999, "", false},
		{"no match wrong title", "", 0, "Interstellar", false},
		{"tmdb zero does not match", "", 0, "", false},
		{"negative tmdb does not match", "", -1, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := matchMovie(movie, tt.imdbID, tt.tmdbInt, tt.title)
			if got != tt.want {
				t.Errorf("matchMovie(%q, %d, %q) = %v, want %v",
					tt.imdbID, tt.tmdbInt, tt.title, got, tt.want)
			}
		})
	}
}

func TestFilterRadarrMovies(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{
		{
			Title: "Inception", ImdbID: "tt1375666", TmdbID: 27205,
			Year: 2010, HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/inception.mkv", SceneName: "Inception.2010.1080p"},
		},
		{
			Title: "Interstellar", ImdbID: "tt0816692", TmdbID: 157336,
			Year: 2014, HasFile: true,
			MovieFile: &api.MovieFile{Path: "/movies/interstellar.mkv", SceneName: "Interstellar.2014"},
		},
		{
			Title: "No File Movie", ImdbID: "tt0000001", TmdbID: 1,
			Year: 2020, HasFile: false, MovieFile: nil,
		},
		{
			Title: "Has File Flag But Nil", ImdbID: "tt0000002", TmdbID: 2,
			Year: 2021, HasFile: true, MovieFile: nil,
		},
	}

	tests := []struct {
		name      string
		imdbID    string
		tmdbInt   int
		title     string
		wantCount int
		wantTitle string
	}{
		{
			name:      "match by imdb returns first match",
			imdbID:    "tt1375666",
			wantCount: 1,
			wantTitle: "Inception",
		},
		{
			name:      "match by tmdb",
			tmdbInt:   157336,
			wantCount: 1,
			wantTitle: "Interstellar",
		},
		{
			name:      "match by title case insensitive",
			title:     "interstellar",
			wantCount: 1,
			wantTitle: "Interstellar",
		},
		{
			name:      "no match returns nil",
			imdbID:    "tt9999999",
			wantCount: 0,
		},
		{
			name:      "skips movie without file",
			imdbID:    "tt0000001",
			wantCount: 0,
		},
		{
			name:      "skips movie with HasFile but nil MovieFile",
			imdbID:    "tt0000002",
			wantCount: 0,
		},
		{
			name:      "empty criteria returns nil",
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := filterRadarrMovies(movies, tt.imdbID, tt.tmdbInt, tt.title)

			if len(got) != tt.wantCount {
				t.Fatalf("filterRadarrMovies(%q, %d, %q) returned %d items, want %d",
					tt.imdbID, tt.tmdbInt, tt.title, len(got), tt.wantCount)
			}
			if tt.wantCount > 0 {
				if got[0].Title != tt.wantTitle {
					t.Errorf("title = %q, want %q", got[0].Title, tt.wantTitle)
				}
				if got[0].MediaType != "movie" {
					t.Errorf("mediaType = %q, want %q", got[0].MediaType, "movie")
				}
			}
		})
	}
}

func TestFilterRadarrMovies_empty_list(t *testing.T) {
	t.Parallel()

	got := filterRadarrMovies(nil, "tt1375666", 0, "")
	if got != nil {
		t.Errorf("filterRadarrMovies(nil) = %v, want nil", got)
	}
}

func TestFilterRadarrMovies_propagates_metadata(t *testing.T) {
	t.Parallel()

	movies := []api.Movie{{
		Title: "Test", ImdbID: "tt0000001", TmdbID: 42,
		Year: 2024, HasFile: true,
		MovieFile: &api.MovieFile{Path: "/m/test.mkv", SceneName: "Test.2024"},
	}}

	got := filterRadarrMovies(movies, "tt0000001", 0, "")
	if len(got) != 1 {
		t.Fatal("expected 1 result")
	}
	if got[0].Year != 2024 {
		t.Errorf("year = %d, want 2024", got[0].Year)
	}
	if got[0].TmdbID != 42 {
		t.Errorf("tmdbID = %d, want 42", got[0].TmdbID)
	}
	if got[0].SceneName != "Test.2024" {
		t.Errorf("sceneName = %q, want %q", got[0].SceneName, "Test.2024")
	}
	if got[0].FilePath != "/m/test.mkv" {
		t.Errorf("filePath = %q, want %q", got[0].FilePath, "/m/test.mkv")
	}
}
