package mediaid

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

func TestIsValidMediaPrefix_valid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"tvdb_with_trailing_dash", "tvdb-81189-"},
		{"tvdb_large_id", "tvdb-999999999-"},
		{"tvdb_single_digit", "tvdb-1-"},
		{"tmdb_with_trailing_dash", "tmdb-1271-"},
		{"tmdb_without_trailing_dash", "tmdb-1271"},
		{"tmdb_single_digit", "tmdb-1"},
		{"imdb_standard", "imdb-tt1234567"},
		{"imdb_short_id", "imdb-tt1"},
		{"imdb_long_id", "imdb-tt12345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !ValidPrefix(tt.prefix) {
				t.Errorf("ValidPrefix(%q) = false, want true", tt.prefix)
			}
		})
	}
}

func TestIsValidMediaPrefix_invalid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"empty_string", ""},
		{"arbitrary_text", "hello-world"},
		{"tvdb_no_digits", "tvdb--"},
		{"tvdb_no_trailing_dash", "tvdb-81189"},
		{"tmdb_no_digits", "tmdb-"},
		{"imdb_no_tt", "imdb-1234567"},
		{"imdb_no_digits", "imdb-tt"},
		{"just_prefix", "tvdb"},
		{"numeric_only", "12345"},
		{"wrong_case_tvdb", "TVDB-81189-"},
		{"wrong_case_tmdb", "TMDB-1271"},
		{"wrong_case_imdb", "IMDB-tt1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if ValidPrefix(tt.prefix) {
				t.Errorf("ValidPrefix(%q) = true, want false", tt.prefix)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *subflux.SearchRequest
		want string
	}{
		{name: "movie with tmdb ID", req: &subflux.SearchRequest{MediaType: "movie", TmdbID: 42}, want: "tmdb-42"},
		{name: "movie with both IDs prefers tmdb", req: &subflux.SearchRequest{MediaType: "movie", ImdbID: "tt9999999", TmdbID: 42}, want: "tmdb-42"},
		{name: "movie with imdb ID only", req: &subflux.SearchRequest{MediaType: "movie", ImdbID: "tt1234567"}, want: "tt1234567"},
		{name: "movie with no IDs returns empty", req: &subflux.SearchRequest{MediaType: "movie"}, want: ""},
		{name: "episode with tvdb ID", req: &subflux.SearchRequest{MediaType: "episode", TvdbID: 81189, Season: 3, Episode: 7}, want: "tvdb-81189-s03e07"},
		{name: "episode with tvdb and imdb prefers tvdb", req: &subflux.SearchRequest{MediaType: "episode", ImdbID: "tt1234567", TvdbID: 81189, Season: 3, Episode: 7}, want: "tvdb-81189-s03e07"},
		{name: "episode with imdb only fallback", req: &subflux.SearchRequest{MediaType: "episode", ImdbID: "tt1234567", Season: 3, Episode: 7}, want: "tt1234567-s03e07"},
		{name: "episode with zero season and episode", req: &subflux.SearchRequest{MediaType: "episode", TvdbID: 1, Season: 0, Episode: 0}, want: "tvdb-1-s00e00"},
		{name: "episode with large season and episode numbers", req: &subflux.SearchRequest{MediaType: "episode", TvdbID: 99999, Season: 99, Episode: 150}, want: "tvdb-99999-s99e150"},
		{name: "episode with no IDs", req: &subflux.SearchRequest{MediaType: "episode", Season: 1, Episode: 1}, want: "s01e01"},
		{name: "unknown media type falls through to episode path", req: &subflux.SearchRequest{MediaType: "special", TvdbID: 100, Season: 0, Episode: 1}, want: "tvdb-100-s00e01"},
		{name: "movie with negative tmdb ID still used", req: &subflux.SearchRequest{MediaType: "movie", TmdbID: -1}, want: "tmdb--1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Build(tt.req)

			if got != tt.want {
				t.Errorf("Build(%+v) = %q, want %q",
					tt.req, got, tt.want)
			}
		})
	}
}

func TestMovie(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		imdbID string
		want   string
		tmdbID int
	}{
		{name: "tmdb present", imdbID: "", want: "tmdb-42", tmdbID: 42},
		{name: "imdb only", imdbID: "tt1234567", want: "tt1234567", tmdbID: 0},
		{name: "both prefers tmdb", imdbID: "tt1234567", want: "tmdb-42", tmdbID: 42},
		{name: "neither returns empty", imdbID: "", want: "", tmdbID: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Movie(tt.tmdbID, tt.imdbID)

			if got != tt.want {
				t.Errorf("Movie(%d, %q) = %q, want %q",
					tt.tmdbID, tt.imdbID, got, tt.want)
			}
		})
	}
}

func TestEpisode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		imdbID string
		want   string
		ep     SeasonEpisode
		tvdbID int
	}{
		{name: "tvdb present", imdbID: "", want: "tvdb-81189-s03e07", ep: SeasonEpisode{Season: 3, Episode: 7}, tvdbID: 81189},
		{name: "imdb fallback", imdbID: "tt1234567", want: "tt1234567-s01e01", ep: SeasonEpisode{Season: 1, Episode: 1}, tvdbID: 0},
		{name: "both prefers tvdb", imdbID: "tt1234567", want: "tvdb-81189-s03e07", ep: SeasonEpisode{Season: 3, Episode: 7}, tvdbID: 81189},
		{name: "no IDs", imdbID: "", want: "s01e01", ep: SeasonEpisode{Season: 1, Episode: 1}, tvdbID: 0},
		{name: "asymmetric pair", imdbID: "", want: "tvdb-121361-s01e09", ep: SeasonEpisode{Season: 1, Episode: 9}, tvdbID: 121361},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Episode(tt.tvdbID, tt.imdbID, tt.ep)

			if got != tt.want {
				t.Errorf("Episode(%d, %q, %+v) = %q, want %q",
					tt.tvdbID, tt.imdbID, tt.ep, got, tt.want)
			}
		})
	}
}

// TestEpisodeTakesOnePairNotTwoInts pins the parameter SHAPE, not a value. The
// season and episode numbers were adjacent ints, and a transposition there
// yields "…-s09e01": a well-formed bbolt primary key and HTTP path segment for
// a different episode, so nothing errors and the wrong row is written. Flatten
// the pair back into two ints and this fails.
func TestEpisodeTakesOnePairNotTwoInts(t *testing.T) {
	t.Parallel()
	fn := reflect.TypeOf(Episode)
	if got, want := fn.NumIn(), 3; got != want {
		t.Fatalf("Episode takes %d parameters, want %d (season and episode travel as one SeasonEpisode)", got, want)
	}
	if got, want := fn.In(2), reflect.TypeOf(SeasonEpisode{}); got != want {
		t.Errorf("Episode's third parameter is %s, want %s", got, want)
	}
}

func TestSeriesPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		imdbID string
		want   string
		tvdbID int
	}{
		{name: "tvdb present", imdbID: "", want: "tvdb-81189-", tvdbID: 81189},
		{name: "imdb only", imdbID: "tt1234567", want: "tt1234567-", tvdbID: 0},
		{name: "both prefers tvdb", imdbID: "tt1234567", want: "tvdb-81189-", tvdbID: 81189},
		{name: "no IDs returns empty", imdbID: "", want: "", tvdbID: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SeriesPrefix(tt.tvdbID, tt.imdbID)

			if got != tt.want {
				t.Errorf("SeriesPrefix(%d, %q) = %q, want %q",
					tt.tvdbID, tt.imdbID, got, tt.want)
			}
		})
	}
}

func TestBuildMediaID_movie_never_contains_season_episode_format(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &subflux.SearchRequest{
			MediaType: "movie",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TmdbID:    rapid.IntRange(1, 999999).Draw(t, "tmdb_id"),
		}

		got := Build(req)

		if strings.Contains(got, "-s") {
			t.Errorf("Build(movie) = %q, should not contain season/episode format",
				got)
		}
		if !strings.HasPrefix(got, "tmdb-") {
			t.Errorf("Build(movie) = %q, should start with tmdb- when TmdbID is set",
				got)
		}
	})
}

func TestBuildMediaID_episode_always_contains_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &subflux.SearchRequest{
			MediaType: "episode",
			TvdbID:    rapid.IntRange(1, 999999).Draw(t, "tvdb_id"),
			Season:    rapid.IntRange(0, 99).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 999).Draw(t, "episode"),
		}

		got := Build(req)

		if !strings.Contains(got, "s") || !strings.Contains(got, "e") {
			t.Errorf("Build(episode) = %q, should contain season/episode format",
				got)
		}
	})
}

// --- Additional Build PBT ---

func TestBuildMediaID_movie_imdb_only_never_contains_season(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &subflux.SearchRequest{
			MediaType: "movie",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TmdbID:    0, // No TMDB ID, falls back to IMDB.
		}

		got := Build(req)

		if strings.Contains(got, "-s") {
			t.Errorf("Build(movie, imdb-only) = %q, should not contain season/episode",
				got)
		}
		if !strings.HasPrefix(got, "tt") {
			t.Errorf("Build(movie, imdb-only) = %q, should start with tt",
				got)
		}
	})
}

func TestBuildMediaID_episode_imdb_fallback_contains_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &subflux.SearchRequest{
			MediaType: "episode",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TvdbID:    0, // No TVDB ID, falls back to IMDB.
			Season:    rapid.IntRange(1, 50).Draw(t, "season"),
			Episode:   rapid.IntRange(1, 99).Draw(t, "episode"),
		}

		got := Build(req)

		if !strings.Contains(got, "-s") || !strings.Contains(got, "e") {
			t.Errorf("Build(episode, imdb-fallback) = %q, should contain -sNNeNN",
				got)
		}
		if !strings.HasPrefix(got, "tt") {
			t.Errorf("Build(episode, imdb-fallback) = %q, should start with tt",
				got)
		}
	})
}

func TestBuildMediaID_episode_no_ids_still_has_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &subflux.SearchRequest{
			MediaType: "episode",
			TvdbID:    0,
			ImdbID:    "",
			Season:    rapid.IntRange(0, 99).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 999).Draw(t, "episode"),
		}

		got := Build(req)

		if !strings.HasPrefix(got, "s") {
			t.Errorf("Build(episode, no-ids) = %q, should start with s",
				got)
		}
		if !strings.Contains(got, "e") {
			t.Errorf("Build(episode, no-ids) = %q, should contain e",
				got)
		}
	})
}

// --- MediaLabel PBT ---

func TestBuildMediaID_nil_request_returns_empty(t *testing.T) {
	t.Parallel()

	got := Build(nil)

	if got != "" {
		t.Errorf("Build(nil) = %q, want empty string", got)
	}
}

func BenchmarkBuild(b *testing.B) {
	req := &subflux.SearchRequest{
		MediaType: subflux.MediaTypeEpisode,
		TvdbID:    12345,
		ImdbID:    "tt1234567",
		Season:    3,
		Episode:   7,
	}
	b.ReportAllocs()
	for range b.N {
		_ = Build(req)
	}
}

func BenchmarkEpisode(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = Episode(98765, "tt7654321", SeasonEpisode{Season: 2, Episode: 14})
	}
}
