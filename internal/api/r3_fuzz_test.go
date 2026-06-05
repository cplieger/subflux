package api

import (
	"strings"
	"testing"
)

func FuzzBuildMediaID(f *testing.F) {
	f.Add("movie", 100, 0, "tt1234567", 0, 0)
	f.Add("episode", 0, 12345, "tt0000001", 1, 5)
	f.Add("episode", 0, 0, "", 0, 0)
	f.Add("movie", 0, 0, "", 0, 0)
	f.Add("unknown", 50, 999, "tt9999999", 10, 20)
	f.Add("", 0, 0, "", 0, 0)

	f.Fuzz(func(t *testing.T, mediaType string, tmdbID, tvdbID int, imdbID string, season, episode int) {
		req := &SearchRequest{
			MediaType: MediaType(mediaType),
			TmdbID:    tmdbID,
			TvdbID:    tvdbID,
			ImdbID:    imdbID,
			Season:    season,
			Episode:   episode,
		}
		result := BuildMediaID(req)

		// Cross-check against individual builders
		switch MediaType(mediaType) {
		case MediaTypeMovie:
			want := BuildMovieID(tmdbID, imdbID)
			if result != want {
				t.Errorf("BuildMediaID movie: got %q, want %q", result, want)
			}
		case MediaTypeEpisode:
			want := BuildEpisodeID(tvdbID, imdbID, season, episode)
			if result != want {
				t.Errorf("BuildMediaID episode: got %q, want %q", result, want)
			}
		default:
			// Unknown types fall through to episode logic
			want := BuildEpisodeID(tvdbID, imdbID, season, episode)
			if result != want {
				t.Errorf("BuildMediaID default: got %q, want %q", result, want)
			}
		}

		// Nil request must return empty
		if BuildMediaID(nil) != "" {
			t.Error("BuildMediaID(nil) should return empty")
		}

		// Consistency: movie IDs never contain season/episode markers
		if MediaType(mediaType) == MediaTypeMovie && result != "" {
			if strings.Contains(result, "-s") {
				t.Errorf("movie ID %q contains season marker", result)
			}
		}
	})
}

func FuzzMediaLabel(f *testing.F) {
	f.Add("movie", "Inception", 2010, 0, 0)
	f.Add("episode", "Bleach", 2004, 9, 15)
	f.Add("episode", "", 0, 0, 0)
	f.Add("movie", "X", 0, 0, 0)

	f.Fuzz(func(t *testing.T, mediaType, title string, year, season, episode int) {
		req := &SearchRequest{
			MediaType: MediaType(mediaType),
			Title:     title,
			Year:      year,
			Season:    season,
			Episode:   episode,
		}
		result := req.MediaLabel()
		// Must not panic and must contain title if non-empty
		if title != "" && !strings.Contains(result, title) {
			t.Errorf("MediaLabel() = %q, missing title %q", result, title)
		}
	})
}
