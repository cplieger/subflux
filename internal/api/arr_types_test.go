package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi"
	"pgregory.net/rapid"
)

// --- HistoryEntry.ImportedPath (promoted from arrapi.HistoryRecord via alias) ---

func TestHistoryEntry_ImportedPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data map[string]string
		want string
	}{
		{"present", map[string]string{"importedPath": "/media/movie.mkv"}, "/media/movie.mkv"},
		{"absent", map[string]string{"other": "value"}, ""},
		{"nil data", nil, ""},
		{"empty string", map[string]string{"importedPath": ""}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &arrapi.HistoryRecord{Data: tt.data}

			got := h.ImportedPath()

			if got != tt.want {
				t.Errorf("ImportedPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- LangNameToISO ---

func TestLangNameToISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"known lowercase", "danish", "da"},
		{"known mixed case", "Danish", "da"},
		{"known uppercase", "DANISH", "da"},
		{"another known", "finnish", "fi"},
		{"another known 2", "turkish", "tr"},
		{"another known 3", "swedish", "sv"},
		{"two letter code passthrough", "en", "en"},
		{"two letter code uppercase", "EN", "en"},
		{"two letter code mixed", "Fr", "fr"},
		{"three letter string not in map", "eng", ""},
		{"unknown language name", "klingon", ""},
		{"single character", "e", ""},
		{"numeric string", "42", ""},
		{"special characters", "en!", ""},
		{"whitespace only", " ", ""},
		{"regional variant with parens", "Portuguese (Brazil)", "pt"},
		{"regional variant spanish latino", "Spanish (Latino)", "es"},
		{"alias maps to same code as primary", "flemish", "nl"},
		{"two letter non-ascii rejected", "\u00f1\u00e9", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := LangNameToISO(tt.input)

			if got != tt.want {
				t.Errorf("LangNameToISO(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- ParseAudioLangs ---

func TestParseAudioLangs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"single known language", "English", []string{"en"}},
		{"single two-letter code", "en", []string{"en"}},
		{"single unknown language", "Klingon", nil},
		{"empty string", "", nil},
		{"slash separated", "English/French", []string{"en", "fr"}},
		{"comma separated", "English,French", []string{"en", "fr"}},
		{"mixed separators", "English/French,German", []string{"en", "fr", "de"}},
		{"with whitespace", " English / French , German ", []string{"en", "fr", "de"}},
		{"duplicate languages deduplicated", "English/English/French", []string{"en", "fr"}},
		{"all unknown with separator", "Klingon/Elvish", nil},
		{"mixed known and unknown", "English/Klingon/French", []string{"en", "fr"}},
		{"two-letter codes with slash", "en/fr/de", []string{"en", "fr", "de"}},
		{"single with trailing slash", "English/", []string{"en"}},
		{"single with leading slash", "/English", []string{"en"}},
		{"only separators", "/,/", nil},
		{"regional variant with parens", "English/Portuguese (Brazil)", []string{"en", "pt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ParseAudioLangs(tt.raw)

			if !slices.Equal(got, tt.want) {
				t.Errorf("ParseAudioLangs(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// --- OriginalLangCode (free function over an arr language reference) ---

func TestSeries_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *arrapi.Language
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &arrapi.Language{Name: "English", ID: 1}, "en"},
		{"unknown language", &arrapi.Language{Name: "Klingon", ID: 99}, ""},
		{"empty name", &arrapi.Language{Name: "", ID: 0}, ""},
		{"two-letter code", &arrapi.Language{Name: "fr", ID: 2}, "fr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := OriginalLangCode(tt.lang)

			if got != tt.want {
				t.Errorf("OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMovie_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *arrapi.Language
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &arrapi.Language{Name: "French", ID: 2}, "fr"},
		{"unknown language", &arrapi.Language{Name: "Dothraki", ID: 99}, ""},
		{"empty name", &arrapi.Language{Name: "", ID: 0}, ""},
		{"two-letter code", &arrapi.Language{Name: "de", ID: 3}, "de"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := OriginalLangCode(tt.lang)

			if got != tt.want {
				t.Errorf("OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- SeasonEpisodeFileCount (free function) ---

func TestSeries_SeasonEpisodeFileCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		seasons   []arrapi.Season
		seasonNum int
		want      int
	}{
		{"no seasons", nil, 1, 0},
		{"season found with stats", []arrapi.Season{
			{SeasonNumber: 1, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 10}},
			{SeasonNumber: 2, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 5}},
		}, 1, 10},
		{"season found nil stats", []arrapi.Season{
			{SeasonNumber: 1, Statistics: nil},
		}, 1, 0},
		{"season not found", []arrapi.Season{
			{SeasonNumber: 1, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 10}},
		}, 99, 0},
		{"season zero (specials)", []arrapi.Season{
			{SeasonNumber: 0, Statistics: &arrapi.SeasonStatistics{EpisodeFileCount: 3}},
		}, 0, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := SeasonEpisodeFileCount(&arrapi.Series{Seasons: tt.seasons}, tt.seasonNum)

			if got != tt.want {
				t.Errorf("SeasonEpisodeFileCount(%d) = %d, want %d",
					tt.seasonNum, got, tt.want)
			}
		})
	}
}

// --- AudioLanguages (free function over a file's MediaInfo) ---

func TestEpisodeFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *arrapi.EpisodeFile
		want []string
	}{
		{"nil media info", &arrapi.EpisodeFile{MediaInfo: nil}, nil},
		{"empty audio languages", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English"},
		}, []string{"en"}},
		{"multiple languages", &arrapi.EpisodeFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English/Japanese"},
		}, []string{"en", "ja"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := AudioLanguages(tt.file.MediaInfo)

			if !slices.Equal(got, tt.want) {
				t.Errorf("AudioLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMovieFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *arrapi.MovieFile
		want []string
	}{
		{"nil media info", &arrapi.MovieFile{MediaInfo: nil}, nil},
		{"empty audio languages", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "French"},
		}, []string{"fr"}},
		{"multiple languages", &arrapi.MovieFile{
			MediaInfo: &arrapi.MediaInfo{AudioLanguages: "English,French,German"},
		}, []string{"en", "fr", "de"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := AudioLanguages(tt.file.MediaInfo)

			if !slices.Equal(got, tt.want) {
				t.Errorf("AudioLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Property-based tests ---

func TestLangNameToISO_known_names_always_return_two_letter_code(t *testing.T) {
	t.Parallel()

	knownNames := make([]string, 0, len(langNameMap))
	for name := range langNameMap {
		knownNames = append(knownNames, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(knownNames).Draw(t, "lang_name")

		code := LangNameToISO(name)

		if len(code) != 2 {
			t.Errorf("LangNameToISO(%q) = %q, want 2-letter code", name, code)
		}
	})
}

func TestLangNameToISO_two_letter_ascii_passthrough(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		code := rapid.StringMatching(`[a-z]{2}`).Draw(t, "code")

		got := LangNameToISO(code)

		if got != code {
			t.Errorf("LangNameToISO(%q) = %q, want %q (passthrough)", code, got, code)
		}
	})
}

func TestLangNameToISO_case_insensitive(t *testing.T) {
	t.Parallel()

	// Draw from the map keys to avoid hardcoding language names
	// that would trigger goconst.
	knownNames := make([]string, 0, len(langNameMap))
	for name := range langNameMap {
		knownNames = append(knownNames, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		base := rapid.SampledFrom(knownNames).Draw(t, "name")
		// Test uppercase variant.
		upper := strings.ToUpper(base)

		got := LangNameToISO(upper)

		if got == "" {
			t.Errorf("LangNameToISO(%q) = empty, want non-empty (base=%q)", upper, base)
		}
	})
}

func TestParseAudioLangs_never_contains_duplicates(t *testing.T) {
	t.Parallel()

	knownNames := make([]string, 0, len(langNameMap))
	for name := range langNameMap {
		knownNames = append(knownNames, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		count := rapid.IntRange(1, 6).Draw(t, "count")
		parts := make([]string, count)
		for i := range count {
			parts[i] = rapid.SampledFrom(knownNames).Draw(t, "lang")
		}
		sep := rapid.SampledFrom([]string{"/", ","}).Draw(t, "sep")
		raw := strings.Join(parts, sep)

		codes := ParseAudioLangs(raw)

		seen := make(map[string]bool)
		for _, code := range codes {
			if seen[code] {
				t.Errorf("ParseAudioLangs(%q) contains duplicate %q", raw, code)
			}
			seen[code] = true
		}
	})
}

func TestParseAudioLangs_output_always_two_letter_lowercase(t *testing.T) {
	t.Parallel()

	knownNames := make([]string, 0, len(langNameMap))
	for name := range langNameMap {
		knownNames = append(knownNames, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		count := rapid.IntRange(1, 6).Draw(t, "count")
		parts := make([]string, count)
		for i := range count {
			parts[i] = rapid.SampledFrom(knownNames).Draw(t, "lang")
		}
		sep := rapid.SampledFrom([]string{"/", ","}).Draw(t, "sep")
		raw := strings.Join(parts, sep)

		codes := ParseAudioLangs(raw)

		for _, code := range codes {
			if len(code) != 2 {
				t.Errorf("ParseAudioLangs(%q) contains non-2-letter code %q", raw, code)
			}
			if code != strings.ToLower(code) {
				t.Errorf("ParseAudioLangs(%q) contains non-lowercase code %q", raw, code)
			}
		}
	})
}

func TestLangNameToISO_idempotent(t *testing.T) {
	t.Parallel()

	knownNames := make([]string, 0, len(langNameMap))
	for name := range langNameMap {
		knownNames = append(knownNames, name)
	}

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom(knownNames).Draw(t, "lang_name")

		first := LangNameToISO(name)
		second := LangNameToISO(first)

		if second != first {
			t.Errorf("LangNameToISO not idempotent: %q -> %q -> %q", name, first, second)
		}
	})
}

func TestLangNameToISO_all_map_entries(t *testing.T) {
	t.Parallel()

	for name, wantCode := range langNameMap {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := LangNameToISO(name)

			if got != wantCode {
				t.Errorf("LangNameToISO(%q) = %q, want %q", name, got, wantCode)
			}
		})
	}
}
