package subflux

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
