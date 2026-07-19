package api

import "testing"

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
			if !IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = false, want true", tt.prefix)
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
			if IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = true, want false", tt.prefix)
			}
		})
	}
}
