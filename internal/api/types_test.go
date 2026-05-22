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
		hi     bool
		forced bool
		want   Variant
	}{
		{"standard", false, false, DefaultVariant},
		{"hi", true, false, VariantHI},
		{"forced", false, true, VariantForced},
		{"hi takes precedence over forced", true, true, VariantHI},
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
		{
			"movie with tmdb ID",
			&SearchRequest{MediaType: "movie", TmdbID: 42},
			"tmdb-42",
		},
		{
			"movie with both IDs prefers tmdb",
			&SearchRequest{MediaType: "movie", ImdbID: "tt9999999", TmdbID: 42},
			"tmdb-42",
		},
		{
			"movie with imdb ID only",
			&SearchRequest{MediaType: "movie", ImdbID: "tt1234567"},
			"tt1234567",
		},
		{
			"movie with no IDs returns empty",
			&SearchRequest{MediaType: "movie"},
			"",
		},
		{
			"episode with tvdb ID",
			&SearchRequest{MediaType: "episode", TvdbID: 81189, Season: 3, Episode: 7},
			"tvdb-81189-s03e07",
		},
		{
			"episode with tvdb and imdb prefers tvdb",
			&SearchRequest{MediaType: "episode", ImdbID: "tt1234567", TvdbID: 81189, Season: 3, Episode: 7},
			"tvdb-81189-s03e07",
		},
		{
			"episode with imdb only fallback",
			&SearchRequest{MediaType: "episode", ImdbID: "tt1234567", Season: 3, Episode: 7},
			"tt1234567-s03e07",
		},
		{
			"episode with zero season and episode",
			&SearchRequest{MediaType: "episode", TvdbID: 1, Season: 0, Episode: 0},
			"tvdb-1-s00e00",
		},
		{
			"episode with large season and episode numbers",
			&SearchRequest{MediaType: "episode", TvdbID: 99999, Season: 99, Episode: 150},
			"tvdb-99999-s99e150",
		},
		{
			"episode with no IDs",
			&SearchRequest{MediaType: "episode", Season: 1, Episode: 1},
			"s01e01",
		},
		{
			"unknown media type falls through to episode path",
			&SearchRequest{MediaType: "special", TvdbID: 100, Season: 0, Episode: 1},
			"tvdb-100-s00e01",
		},
		{
			"movie with negative tmdb ID still used",
			&SearchRequest{MediaType: "movie", TmdbID: -1},
			"tmdb--1",
		},
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
		{"tmdb present", "", "tmdb-42", 42},
		{"imdb only", "tt1234567", "tt1234567", 0},
		{"both prefers tmdb", "tt1234567", "tmdb-42", 42},
		{"neither returns empty", "", "", 0},
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
		{"tvdb present", "", "tvdb-81189-s03e07", 81189, 3, 7},
		{"imdb fallback", "tt1234567", "tt1234567-s01e01", 0, 1, 1},
		{"both prefers tvdb", "tt1234567", "tvdb-81189-s03e07", 81189, 3, 7},
		{"no IDs", "", "s01e01", 0, 1, 1},
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
		{"tvdb present", "", "tvdb-81189-", 81189},
		{"imdb only", "tt1234567", "tt1234567-", 0},
		{"both prefers tvdb", "tt1234567", "tvdb-81189-", 81189},
		{"no IDs returns empty", "", "", 0},
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
		{
			"movie with year",
			&SearchRequest{MediaType: "movie", Title: "Inception", Year: 2010},
			"Inception (2010)",
		},
		{
			"movie without year",
			&SearchRequest{MediaType: "movie", Title: "Inception"},
			"Inception",
		},
		{
			"episode with year",
			&SearchRequest{MediaType: "episode", Title: "Bleach", Year: 2004, Season: 9, Episode: 15},
			"Bleach (2004) - S09E15",
		},
		{
			"episode without year",
			&SearchRequest{MediaType: "episode", Title: "Bleach", Season: 1, Episode: 1},
			"Bleach - S01E01",
		},
		{
			"episode zero-pads season and episode",
			&SearchRequest{MediaType: "episode", Title: "Show", Year: 2020, Season: 1, Episode: 5},
			"Show (2020) - S01E05",
		},
		{
			"empty title movie",
			&SearchRequest{MediaType: "movie", Title: ""},
			"",
		},
		{
			"empty title episode without year",
			&SearchRequest{MediaType: "episode", Title: "", Season: 1, Episode: 1},
			" - S01E01",
		},
		{
			"episode with large episode number",
			&SearchRequest{MediaType: "episode", Title: "Show", Year: 2020, Season: 1, Episode: 999},
			"Show (2020) - S01E999",
		},
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
