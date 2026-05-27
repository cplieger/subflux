package api

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// --- Error types ---

func TestAuthError_returns_message(t *testing.T) {
	t.Parallel()

	err := &AuthError{Msg: "invalid credentials"}

	got := err.Error()

	if got != "invalid credentials" {
		t.Errorf("AuthError.Error() = %q, want %q", got, "invalid credentials")
	}
}

func TestRateLimitError_returns_message(t *testing.T) {
	t.Parallel()

	err := &RateLimitError{Msg: "too many requests"}

	got := err.Error()

	if got != "too many requests" {
		t.Errorf("RateLimitError.Error() = %q, want %q", got, "too many requests")
	}
}

func TestAuthError_satisfies_error_interface(t *testing.T) {
	t.Parallel()

	var err error = &AuthError{Msg: "expired token"}

	authErr, ok := errors.AsType[*AuthError](err)
	if !ok {
		t.Error("errors.AsType failed to match *AuthError")
	}
	if authErr.Msg != "expired token" {
		t.Errorf("AuthError.Msg = %q, want %q", authErr.Msg, "expired token")
	}
}

func TestRateLimitError_satisfies_error_interface(t *testing.T) {
	t.Parallel()

	var err error = &RateLimitError{Msg: "429 slow down"}

	rlErr, ok := errors.AsType[*RateLimitError](err)
	if !ok {
		t.Error("errors.AsType failed to match *RateLimitError")
	}
	if rlErr.Msg != "429 slow down" {
		t.Errorf("RateLimitError.Msg = %q, want %q", rlErr.Msg, "429 slow down")
	}
}

// --- EffectiveVariant ---

func TestEffectiveVariant_returns_default_when_empty(t *testing.T) {
	t.Parallel()

	target := &SubtitleTarget{Code: "en"}

	got := target.EffectiveVariant()

	if got != DefaultVariant {
		t.Errorf("EffectiveVariant() = %q, want %q", got, DefaultVariant)
	}
}

func TestEffectiveVariant_returns_set_variant(t *testing.T) {
	t.Parallel()

	target := &SubtitleTarget{Code: "en", Variant: "forced"}

	got := target.EffectiveVariant()

	if got != "forced" {
		t.Errorf("EffectiveVariant() = %q, want %q", got, "forced")
	}
}

// --- VariantFromFlags ---

func TestVariantFromFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   Variant
		hi     bool
		forced bool
	}{
		{name: "standard", hi: false, forced: false, want: DefaultVariant},
		{name: "hi", hi: true, forced: false, want: VariantHI},
		{name: "forced", hi: false, forced: true, want: VariantForced},
		{name: "hi takes precedence over forced", hi: true, forced: true, want: VariantHI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VariantFromFlags(tt.hi, tt.forced)
			if got != tt.want {
				t.Errorf("VariantFromFlags(%v, %v) = %q, want %q",
					tt.hi, tt.forced, got, tt.want)
			}
		})
	}
}

func TestBuildMediaID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *SearchRequest
		want string
	}{
		{name: "movie with tmdb ID", req: &SearchRequest{MediaType: "movie", TmdbID: 42}, want: "tmdb-42"},
		{name: "movie with both IDs prefers tmdb", req: &SearchRequest{MediaType: "movie", ImdbID: "tt9999999", TmdbID: 42}, want: "tmdb-42"},
		{name: "movie with imdb ID only", req: &SearchRequest{MediaType: "movie", ImdbID: "tt1234567"}, want: "tt1234567"},
		{name: "movie with no IDs returns empty", req: &SearchRequest{MediaType: "movie"}, want: ""},
		{name: "episode with tvdb ID", req: &SearchRequest{MediaType: "episode", TvdbID: 81189, Season: 3, Episode: 7}, want: "tvdb-81189-s03e07"},
		{name: "episode with tvdb and imdb prefers tvdb", req: &SearchRequest{MediaType: "episode", ImdbID: "tt1234567", TvdbID: 81189, Season: 3, Episode: 7}, want: "tvdb-81189-s03e07"},
		{name: "episode with imdb only fallback", req: &SearchRequest{MediaType: "episode", ImdbID: "tt1234567", Season: 3, Episode: 7}, want: "tt1234567-s03e07"},
		{name: "episode with zero season and episode", req: &SearchRequest{MediaType: "episode", TvdbID: 1, Season: 0, Episode: 0}, want: "tvdb-1-s00e00"},
		{name: "episode with large season and episode numbers", req: &SearchRequest{MediaType: "episode", TvdbID: 99999, Season: 99, Episode: 150}, want: "tvdb-99999-s99e150"},
		{name: "episode with no IDs", req: &SearchRequest{MediaType: "episode", Season: 1, Episode: 1}, want: "s01e01"},
		{name: "unknown media type falls through to episode path", req: &SearchRequest{MediaType: "special", TvdbID: 100, Season: 0, Episode: 1}, want: "tvdb-100-s00e01"},
		{name: "movie with negative tmdb ID still used", req: &SearchRequest{MediaType: "movie", TmdbID: -1}, want: "tmdb--1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildMediaID(tt.req)

			if got != tt.want {
				t.Errorf("BuildMediaID(%+v) = %q, want %q",
					tt.req, got, tt.want)
			}
		})
	}
}

func TestBuildMovieID(t *testing.T) {
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

			got := BuildMovieID(tt.tmdbID, tt.imdbID)

			if got != tt.want {
				t.Errorf("BuildMovieID(%d, %q) = %q, want %q",
					tt.tmdbID, tt.imdbID, got, tt.want)
			}
		})
	}
}

func TestBuildEpisodeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		imdbID  string
		want    string
		tvdbID  int
		season  int
		episode int
	}{
		{name: "tvdb present", imdbID: "", want: "tvdb-81189-s03e07", tvdbID: 81189, season: 3, episode: 7},
		{name: "imdb fallback", imdbID: "tt1234567", want: "tt1234567-s01e01", tvdbID: 0, season: 1, episode: 1},
		{name: "both prefers tvdb", imdbID: "tt1234567", want: "tvdb-81189-s03e07", tvdbID: 81189, season: 3, episode: 7},
		{name: "no IDs", imdbID: "", want: "s01e01", tvdbID: 0, season: 1, episode: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BuildEpisodeID(tt.tvdbID, tt.imdbID, tt.season, tt.episode)

			if got != tt.want {
				t.Errorf("BuildEpisodeID(%d, %q, %d, %d) = %q, want %q",
					tt.tvdbID, tt.imdbID, tt.season, tt.episode, got, tt.want)
			}
		})
	}
}

func TestBuildSeriesPrefix(t *testing.T) {
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

			got := BuildSeriesPrefix(tt.tvdbID, tt.imdbID)

			if got != tt.want {
				t.Errorf("BuildSeriesPrefix(%d, %q) = %q, want %q",
					tt.tvdbID, tt.imdbID, got, tt.want)
			}
		})
	}
}

func TestMediaLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *SearchRequest
		want string
	}{
		{name: "movie with year", req: &SearchRequest{MediaType: "movie", Title: "Inception", Year: 2010}, want: "Inception (2010)"},
		{name: "movie without year", req: &SearchRequest{MediaType: "movie", Title: "Inception"}, want: "Inception"},
		{name: "episode with year", req: &SearchRequest{MediaType: "episode", Title: "Bleach", Year: 2004, Season: 9, Episode: 15}, want: "Bleach (2004) - S09E15"},
		{name: "episode without year", req: &SearchRequest{MediaType: "episode", Title: "Bleach", Season: 1, Episode: 1}, want: "Bleach - S01E01"},
		{name: "episode zero-pads season and episode", req: &SearchRequest{MediaType: "episode", Title: "Show", Year: 2020, Season: 1, Episode: 5}, want: "Show (2020) - S01E05"},
		{name: "empty title movie", req: &SearchRequest{MediaType: "movie", Title: ""}, want: ""},
		{name: "empty title episode without year", req: &SearchRequest{MediaType: "episode", Title: "", Season: 1, Episode: 1}, want: " - S01E01"},
		{name: "episode with large episode number", req: &SearchRequest{MediaType: "episode", Title: "Show", Year: 2020, Season: 1, Episode: 999}, want: "Show (2020) - S01E999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.req.MediaLabel()

			if got != tt.want {
				t.Errorf("MediaLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Property-based tests ---

func TestBuildMediaID_movie_never_contains_season_episode_format(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "movie",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TmdbID:    rapid.IntRange(1, 999999).Draw(t, "tmdb_id"),
		}

		got := BuildMediaID(req)

		if strings.Contains(got, "-s") {
			t.Errorf("BuildMediaID(movie) = %q, should not contain season/episode format",
				got)
		}
		if !strings.HasPrefix(got, "tmdb-") {
			t.Errorf("BuildMediaID(movie) = %q, should start with tmdb- when TmdbID is set",
				got)
		}
	})
}

func TestEffectiveVariant_never_empty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		variant := Variant(rapid.SampledFrom([]string{"", "forced", "hi", "standard", "sdh"}).Draw(t, "variant"))
		target := &SubtitleTarget{Code: "en", Variant: variant}

		got := target.EffectiveVariant()

		if got == "" {
			t.Errorf("EffectiveVariant() returned empty string for variant=%q", variant)
		}
	})
}

func TestBuildMediaID_episode_always_contains_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "episode",
			TvdbID:    rapid.IntRange(1, 999999).Draw(t, "tvdb_id"),
			Season:    rapid.IntRange(0, 99).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 999).Draw(t, "episode"),
		}

		got := BuildMediaID(req)

		if !strings.Contains(got, "s") || !strings.Contains(got, "e") {
			t.Errorf("BuildMediaID(episode) = %q, should contain season/episode format",
				got)
		}
	})
}

// --- Additional BuildMediaID PBT ---

func TestBuildMediaID_movie_imdb_only_never_contains_season(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "movie",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TmdbID:    0, // No TMDB ID, falls back to IMDB.
		}

		got := BuildMediaID(req)

		if strings.Contains(got, "-s") {
			t.Errorf("BuildMediaID(movie, imdb-only) = %q, should not contain season/episode",
				got)
		}
		if !strings.HasPrefix(got, "tt") {
			t.Errorf("BuildMediaID(movie, imdb-only) = %q, should start with tt",
				got)
		}
	})
}

func TestBuildMediaID_episode_imdb_fallback_contains_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "episode",
			ImdbID:    rapid.StringMatching(`tt[0-9]{7}`).Draw(t, "imdb_id"),
			TvdbID:    0, // No TVDB ID, falls back to IMDB.
			Season:    rapid.IntRange(1, 50).Draw(t, "season"),
			Episode:   rapid.IntRange(1, 99).Draw(t, "episode"),
		}

		got := BuildMediaID(req)

		if !strings.Contains(got, "-s") || !strings.Contains(got, "e") {
			t.Errorf("BuildMediaID(episode, imdb-fallback) = %q, should contain -sNNeNN",
				got)
		}
		if !strings.HasPrefix(got, "tt") {
			t.Errorf("BuildMediaID(episode, imdb-fallback) = %q, should start with tt",
				got)
		}
	})
}

func TestBuildMediaID_episode_no_ids_still_has_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "episode",
			TvdbID:    0,
			ImdbID:    "",
			Season:    rapid.IntRange(0, 99).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 999).Draw(t, "episode"),
		}

		got := BuildMediaID(req)

		if !strings.HasPrefix(got, "s") {
			t.Errorf("BuildMediaID(episode, no-ids) = %q, should start with s",
				got)
		}
		if !strings.Contains(got, "e") {
			t.Errorf("BuildMediaID(episode, no-ids) = %q, should contain e",
				got)
		}
	})
}

// --- MediaLabel PBT ---

func TestMediaLabel_episode_always_contains_season_episode_markers(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "episode",
			Title:     rapid.StringMatching(`[A-Za-z ]{1,30}`).Draw(t, "title"),
			Year:      rapid.IntRange(0, 2030).Draw(t, "year"),
			Season:    rapid.IntRange(0, 99).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 999).Draw(t, "episode"),
		}

		got := req.MediaLabel()

		if !strings.Contains(got, "S") || !strings.Contains(got, "E") {
			t.Errorf("MediaLabel(episode) = %q, should contain S and E markers",
				got)
		}
	})
}

func TestMediaLabel_movie_never_contains_season_episode(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		req := &SearchRequest{
			MediaType: "movie",
			Title:     rapid.StringMatching(`[A-Za-z ]{1,30}`).Draw(t, "title"),
			Year:      rapid.IntRange(1900, 2030).Draw(t, "year"),
		}

		got := req.MediaLabel()

		if strings.Contains(got, " - S") {
			t.Errorf("MediaLabel(movie) = %q, should not contain episode format",
				got)
		}
	})
}

func TestBuildMediaID_nil_request_returns_empty(t *testing.T) {
	t.Parallel()

	got := BuildMediaID(nil)

	if got != "" {
		t.Errorf("BuildMediaID(nil) = %q, want empty string", got)
	}
}
