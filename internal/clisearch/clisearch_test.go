package clisearch

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestSearchItem_label(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		item searchItem
	}{
		{
			name: "movie with year",
			item: searchItem{Title: "Inception", Year: 2010, MediaType: "movie"},
			want: "Inception (2010)",
		},
		{
			name: "episode with season and episode",
			item: searchItem{Title: "Breaking Bad", Season: 1, Episode: 3, MediaType: "episode"},
			want: "Breaking Bad S01E03",
		},
		{
			name: "season only",
			item: searchItem{Title: "Breaking Bad", Season: 2, MediaType: "episode"},
			want: "Breaking Bad Season 2",
		},
		{
			name: "title only",
			item: searchItem{Title: "Some Show", MediaType: "episode"},
			want: "Some Show",
		},
		{
			name: "movie with zero year",
			item: searchItem{Title: "Unknown", Year: 0, MediaType: "movie"},
			want: "Unknown (0)",
		},
		{
			name: "episode formatting pads single digits",
			item: searchItem{Title: "Show", Season: 1, Episode: 1, MediaType: "episode"},
			want: "Show S01E01",
		},
		{
			name: "episode formatting pads double digits",
			item: searchItem{Title: "Show", Season: 10, Episode: 25, MediaType: "episode"},
			want: "Show S10E25",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.item.label()
			if got != tt.want {
				t.Errorf("searchItem{title=%q, mediaType=%q, season=%d, episode=%d}.label() = %q, want %q",
					tt.item.Title, tt.item.MediaType, tt.item.Season, tt.item.Episode, got, tt.want)
			}
		})
	}
}

func TestSearchItem_label_never_panics(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		item := searchItem{
			Title:     rapid.String().Draw(t, "title"),
			MediaType: api.MediaType(rapid.SampledFrom([]string{"movie", "episode", ""}).Draw(t, "mediaType")),
			Year:      rapid.IntRange(0, 3000).Draw(t, "year"),
			Season:    rapid.IntRange(0, 100).Draw(t, "season"),
			Episode:   rapid.IntRange(0, 1000).Draw(t, "episode"),
		}
		// Must not panic for any input combination.
		got := item.label()
		if got == "" && item.Title == "" {
			// Empty title produces empty label — that's fine.
			return
		}
		// Label should always contain the title.
		if item.Title != "" && len(got) < len(item.Title) {
			t.Errorf("label() = %q shorter than title %q", got, item.Title)
		}
	})
}

func TestParseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantParams   map[string]string
		name         string
		args         []string
		wantDownload bool
	}{
		{
			name:         "empty args",
			args:         nil,
			wantParams:   map[string]string{},
			wantDownload: false,
		},
		{
			name:         "single key-value pair",
			args:         []string{"--imdb", "tt0903747"},
			wantParams:   map[string]string{"imdb": "tt0903747"},
			wantDownload: false,
		},
		{
			name:         "multiple key-value pairs",
			args:         []string{"--imdb", "tt0903747", "--lang", "fr", "--season", "1"},
			wantParams:   map[string]string{"imdb": "tt0903747", "lang": "fr", "season": "1"},
			wantDownload: false,
		},
		{
			name:         "download flag only",
			args:         []string{"--download"},
			wantParams:   map[string]string{},
			wantDownload: true,
		},
		{
			name:         "download flag with key-value pairs",
			args:         []string{"--imdb", "tt1375666", "--download", "--lang", "fr"},
			wantParams:   map[string]string{"imdb": "tt1375666", "lang": "fr"},
			wantDownload: true,
		},
		{
			name:         "download flag at end",
			args:         []string{"--imdb", "tt1375666", "--lang", "fr", "--download"},
			wantParams:   map[string]string{"imdb": "tt1375666", "lang": "fr"},
			wantDownload: true,
		},
		{
			name:         "key without value at end is ignored",
			args:         []string{"--imdb", "tt0903747", "--lang"},
			wantParams:   map[string]string{"imdb": "tt0903747"},
			wantDownload: false,
		},
		{
			name:         "bare double dash is ignored",
			args:         []string{"--", "value"},
			wantParams:   map[string]string{},
			wantDownload: false,
		},
		{
			name:         "non-flag args are ignored",
			args:         []string{"positional", "--imdb", "tt0903747"},
			wantParams:   map[string]string{"imdb": "tt0903747"},
			wantDownload: false,
		},
		{
			name:         "last value wins for duplicate keys",
			args:         []string{"--lang", "en", "--lang", "fr"},
			wantParams:   map[string]string{"lang": "fr"},
			wantDownload: false,
		},
		{
			name:         "pick flag with numeric value",
			args:         []string{"--pick", "3"},
			wantParams:   map[string]string{"pick": "3"},
			wantDownload: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotParams, gotDownload := parseArgs(tt.args)

			if gotDownload != tt.wantDownload {
				t.Errorf("parseArgs(%v) download = %v, want %v",
					tt.args, gotDownload, tt.wantDownload)
			}
			if len(gotParams) != len(tt.wantParams) {
				t.Errorf("parseArgs(%v) params len = %d, want %d\n  got:  %v\n  want: %v",
					tt.args, len(gotParams), len(tt.wantParams), gotParams, tt.wantParams)
				return
			}
			for k, want := range tt.wantParams {
				if got := gotParams[k]; got != want {
					t.Errorf("parseArgs(%v) params[%q] = %q, want %q",
						tt.args, k, got, want)
				}
			}
		})
	}
}

func TestParseArgs_preserves_all_key_value_pairs(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-5 unique keys with values.
		n := rapid.IntRange(1, 5).Draw(t, "numPairs")
		keys := make([]string, 0, n)
		vals := make([]string, 0, n)
		seen := make(map[string]bool)
		for range n {
			// Keys: alphanumeric, no dashes, non-empty.
			k := rapid.StringMatching(`[a-z]{2,8}`).Draw(t, "key")
			if seen[k] {
				continue
			}
			seen[k] = true
			v := rapid.StringMatching(`[a-zA-Z0-9_.-]{1,20}`).Draw(t, "val")
			keys = append(keys, k)
			vals = append(vals, v)
		}

		// Build args: --key value pairs.
		var args []string
		for i := range keys {
			args = append(args, "--"+keys[i], vals[i])
		}

		params, download := parseArgs(args)

		// No --download in generated args.
		if download {
			t.Error("parseArgs: unexpected download=true with no --download flag")
		}

		// Every key-value pair should be in the result.
		for i := range keys {
			if got := params[keys[i]]; got != vals[i] {
				t.Errorf("parseArgs: params[%q] = %q, want %q", keys[i], got, vals[i])
			}
		}
	})
}

func TestParseSearchParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		params    map[string]string
		wantLang  string
		wantImdb  string
		wantTmdb  string
		wantTitle string
		wantPickN int
	}{
		{
			name:      "imdb only defaults lang to fr and pick to 1",
			params:    map[string]string{"imdb": "tt0903747"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantPickN: 1,
		},
		{
			name:      "explicit lang overrides default",
			params:    map[string]string{"imdb": "tt0903747", "lang": "en"},
			wantLang:  "en",
			wantImdb:  "tt0903747",
			wantPickN: 1,
		},
		{
			name:      "tmdb identifier",
			params:    map[string]string{"tmdb": "550"},
			wantLang:  "fr",
			wantTmdb:  "550",
			wantPickN: 1,
		},
		{
			name:      "title identifier",
			params:    map[string]string{"title": "Inception"},
			wantLang:  "fr",
			wantTitle: "Inception",
			wantPickN: 1,
		},
		{
			name:      "pick overrides default",
			params:    map[string]string{"imdb": "tt0903747", "pick": "3"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantPickN: 3,
		},
		{
			name:      "invalid pick falls back to 1",
			params:    map[string]string{"imdb": "tt0903747", "pick": "abc"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantPickN: 1,
		},
		{
			name:      "zero pick falls back to 1",
			params:    map[string]string{"imdb": "tt0903747", "pick": "0"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantPickN: 1,
		},
		{
			name:      "negative pick falls back to 1",
			params:    map[string]string{"imdb": "tt0903747", "pick": "-5"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantPickN: 1,
		},
		{
			name:      "all identifiers provided returns all",
			params:    map[string]string{"imdb": "tt0903747", "tmdb": "550", "title": "Fight Club"},
			wantLang:  "fr",
			wantImdb:  "tt0903747",
			wantTmdb:  "550",
			wantTitle: "Fight Club",
			wantPickN: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lang, imdbID, tmdbID, title, pickN, _ := parseSearchParams(tt.params)

			if lang != tt.wantLang {
				t.Errorf("parseSearchParams(%v) lang = %q, want %q",
					tt.params, lang, tt.wantLang)
			}
			if imdbID != tt.wantImdb {
				t.Errorf("parseSearchParams(%v) imdbID = %q, want %q",
					tt.params, imdbID, tt.wantImdb)
			}
			if tmdbID != tt.wantTmdb {
				t.Errorf("parseSearchParams(%v) tmdbID = %q, want %q",
					tt.params, tmdbID, tt.wantTmdb)
			}
			if title != tt.wantTitle {
				t.Errorf("parseSearchParams(%v) title = %q, want %q",
					tt.params, title, tt.wantTitle)
			}
			if pickN != tt.wantPickN {
				t.Errorf("parseSearchParams(%v) pickN = %d, want %d",
					tt.params, pickN, tt.wantPickN)
			}
		})
	}
}
