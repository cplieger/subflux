package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/manualops"
)

func TestParseManualSearchQuery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		url         string
		wantLang    string
		wantType    api.MediaType
		wantFile    string
		wantTitle   string
		wantRelease string
		wantImdb    string
		wantTmdb    int
		wantTvdb    int
		wantYear    int
		wantSeason  int
		wantEpisode int
	}{
		{
			name: "defaults", url: "/api/search",
			wantLang: "en", wantType: "movie",
		},
		{
			name:     "all_params",
			url:      "/api/search?title=Breaking+Bad&imdb=tt0903747&tmdb=1396&tvdb=81189&lang=fr&type=episode&year=2008&season=1&episode=3&release=Breaking.Bad.S01E03&file=/media/bb.mkv",
			wantLang: "fr", wantType: "episode", wantFile: "/media/bb.mkv",
			wantTitle: "Breaking Bad", wantRelease: "Breaking.Bad.S01E03",
			wantImdb: "tt0903747", wantTmdb: 1396, wantTvdb: 81189,
			wantYear: 2008, wantSeason: 1, wantEpisode: 3,
		},
		{
			name: "infers_episode_type", url: "/api/search?season=2&episode=5",
			wantLang: "en", wantType: "episode", wantSeason: 2, wantEpisode: 5,
		},
		{
			name: "invalid_numbers_ignored", url: "/api/search?year=abc&season=xyz&episode=!",
			wantLang: "en", wantType: "episode", // season+episode params present → infers episode
		},
		{
			name: "file_sets_release_name", url: "/api/search?file=/media/Movie.2024.1080p.mkv",
			wantLang: "en", wantType: "movie", wantFile: "/media/Movie.2024.1080p.mkv",
			wantRelease: "/media/Movie.2024.1080p.mkv",
		},
		{
			name: "release_takes_priority_over_file", url: "/api/search?release=Custom.Release&file=/media/movie.mkv",
			wantLang: "en", wantType: "movie", wantFile: "/media/movie.mkv",
			wantRelease: "Custom.Release",
		},
		{
			name: "negative_numbers_ignored", url: "/api/search?year=-1&season=-5&episode=-10&tmdb=-100&tvdb=-200",
			wantLang: "en", wantType: "episode", // season+episode params present → infers episode
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.url, http.NoBody)
			req, lang, mediaType, filePath := manualops.ParseSearchQuery(r)

			if lang != tc.wantLang {
				t.Errorf("lang = %q, want %q", lang, tc.wantLang)
			}
			if mediaType != tc.wantType {
				t.Errorf("mediaType = %q, want %q", mediaType, tc.wantType)
			}
			if filePath != tc.wantFile {
				t.Errorf("filePath = %q, want %q", filePath, tc.wantFile)
			}
			if tc.wantTitle != "" && req.Title != tc.wantTitle {
				t.Errorf("req.Title = %q, want %q", req.Title, tc.wantTitle)
			}
			if tc.wantRelease != "" && req.ReleaseName != tc.wantRelease {
				t.Errorf("req.ReleaseName = %q, want %q", req.ReleaseName, tc.wantRelease)
			}
			if tc.wantImdb != "" && req.ImdbID != tc.wantImdb {
				t.Errorf("req.ImdbID = %q, want %q", req.ImdbID, tc.wantImdb)
			}
			if req.TmdbID != tc.wantTmdb {
				t.Errorf("req.TmdbID = %d, want %d", req.TmdbID, tc.wantTmdb)
			}
			if req.TvdbID != tc.wantTvdb {
				t.Errorf("req.TvdbID = %d, want %d", req.TvdbID, tc.wantTvdb)
			}
			if req.Year != tc.wantYear {
				t.Errorf("req.Year = %d, want %d", req.Year, tc.wantYear)
			}
			if req.Season != tc.wantSeason {
				t.Errorf("req.Season = %d, want %d", req.Season, tc.wantSeason)
			}
			if req.Episode != tc.wantEpisode {
				t.Errorf("req.Episode = %d, want %d", req.Episode, tc.wantEpisode)
			}
		})
	}
}

func TestBuildManualSearchResults_basic(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ReleaseName: "Movie.2024.srt", MatchedBy: "hash", ID: "123", HearingImp: true, Forced: false}, Score: 300, Matches: map[string]int{"source": 28, "release_group": 23}},
		{Sub: api.Subtitle{Provider: "yify", Language: "en", ReleaseName: "Movie.2024.en.srt", MatchedBy: "title", ID: "456"}, Score: 200},
	}

	refs := []api.DownloadedRef{
		{ReleaseName: "Movie.2024.srt", Provider: "os"},
	}
	results := manualops.BuildSearchResults(scored, refs)

	if len(results) != 2 {
		t.Fatalf("manualops.BuildSearchResults() returned %d results, want 2", len(results))
	}

	// First result should match on-disk.
	r0 := results[0]
	if r0.Provider != "os" {
		t.Errorf("results[0].Provider = %q, want %q", r0.Provider, "os")
	}
	if r0.Language != "fr" {
		t.Errorf("results[0].Language = %q, want %q", r0.Language, "fr")
	}
	if r0.Score != 300 {
		t.Errorf("results[0].Score = %d, want %d", r0.Score, 300)
	}
	if !r0.OnDisk {
		t.Error("results[0].OnDisk = false, want true (matches refs entry)")
	}
	if !r0.HearingImp {
		t.Error("results[0].HearingImp = false, want true")
	}
	if r0.SubtitleID != "123" {
		t.Errorf("results[0].SubtitleID = %q, want %q", r0.SubtitleID, "123")
	}
	if r0.Forced {
		t.Error("results[0].Forced = true, want false")
	}
	if len(r0.Matches) != 2 {
		t.Errorf("results[0].Matches has %d entries, want 2", len(r0.Matches))
	}
	if r0.Matches["source"] != 28 {
		t.Errorf("results[0].Matches[\"source\"] = %d, want 28", r0.Matches["source"])
	}

	// Second result should not match on-disk.
	r1 := results[1]
	if r1.OnDisk {
		t.Error("results[1].OnDisk = true, want false (different provider)")
	}
	if r1.Matches != nil {
		t.Errorf("results[1].Matches = %v, want nil (no matches provided)", r1.Matches)
	}
}

func TestBuildManualSearchResults_limits_to_max(t *testing.T) {
	t.Parallel()

	scored := make([]api.ScoredResult, 60)
	for i := range scored {
		scored[i] = api.ScoredResult{
			Sub:   api.Subtitle{Provider: "os", Language: "en", ID: "id"},
			Score: 100 - i,
		}
	}

	results := manualops.BuildSearchResults(scored, nil)

	if len(results) != manualops.MaxResults {
		t.Errorf("manualops.BuildSearchResults() returned %d results, want %d (capped)",
			len(results), manualops.MaxResults)
	}
}

func TestBuildManualSearchResults_empty_input(t *testing.T) {
	t.Parallel()

	results := manualops.BuildSearchResults(nil, nil)

	if len(results) != 0 {
		t.Errorf("manualops.BuildSearchResults(nil) returned %d results, want 0", len(results))
	}
}

func TestBuildManualSearchResults_fewer_than_10(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ID: "1"}, Score: 100},
	}

	results := manualops.BuildSearchResults(scored, nil)

	if len(results) != 1 {
		t.Errorf("manualops.BuildSearchResults() returned %d results, want 1", len(results))
	}
}

func TestBuildManualSearchResults_on_disk_requires_both_provider_and_release(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ReleaseName: "Movie.srt", ID: "1"}, Score: 100},
	}

	// Same provider, different release.
	results := manualops.BuildSearchResults(scored, []api.DownloadedRef{
		{ReleaseName: "Different.srt", Provider: "os"},
	})
	if results[0].OnDisk {
		t.Error("OnDisk = true with matching provider but different release, want false")
	}

	// Different provider, same release.
	results = manualops.BuildSearchResults(scored, []api.DownloadedRef{
		{ReleaseName: "Movie.srt", Provider: "yify"},
	})
	if results[0].OnDisk {
		t.Error("OnDisk = true with different provider but matching release, want false")
	}
}

func TestBuildManualSearchResults_multiple_historical_matches(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{
			Provider: "os", Language: "fr",
			ReleaseName: "Movie.2024.BluRay-GRP", ID: "1",
		}, Score: 300},
		{Sub: api.Subtitle{
			Provider: "subdl", Language: "fr",
			ReleaseName: "Movie.2024.WEB-DL-OTHER", ID: "2",
		}, Score: 250},
		{Sub: api.Subtitle{
			Provider: "yify", Language: "fr",
			ReleaseName: "Movie.2024.Other-NEW", ID: "3",
		}, Score: 200},
	}

	refs := []api.DownloadedRef{
		{ReleaseName: "Movie.2024.BluRay-GRP", Provider: "os"},
		{ReleaseName: "Movie.2024.WEB-DL-OTHER", Provider: "subdl"},
	}
	results := manualops.BuildSearchResults(scored, refs)

	if !results[0].OnDisk {
		t.Error("results[0] OnDisk = false, want true (first historical entry)")
	}
	if !results[1].OnDisk {
		t.Error("results[1] OnDisk = false, want true (second historical entry)")
	}
	if results[2].OnDisk {
		t.Error("results[2] OnDisk = true, want false (not in history)")
	}
}
