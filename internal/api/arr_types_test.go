package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// --- HistoryEventType.UnmarshalJSON ---

func TestHistoryEventType_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    HistoryEventType
		wantErr bool
	}{
		{"sonarr integer imported", `3`, HistoryImported, false},
		{"sonarr integer deleted", `5`, historyFileDeleted, false},
		{"sonarr integer zero", `0`, 0, false},
		{"sonarr integer negative", `-1`, 0, false}, // negative ints normalize to 0 (treat as unknown)
		{"sonarr integer large", `999`, 999, false},
		{"radarr string imported", `"downloadFolderImported"`, HistoryImported, false},
		{"radarr string deleted", `"movieFileDeleted"`, historyFileDeleted, false},
		{"radarr string unknown", `"someOtherEvent"`, 0, false},
		{"radarr string empty", `""`, 0, false},
		{"json null unmarshals as zero", `null`, 0, false},
		{"invalid json bool", `true`, 0, true},
		{"invalid json bool false", `false`, 0, true},
		{"invalid json float", `3.14`, 0, true},
		{"invalid json array", `[1]`, 0, true},
		{"invalid json object", `{}`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got HistoryEventType
			err := got.UnmarshalJSON([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("UnmarshalJSON(%s) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON(%s) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestHistoryEventType_UnmarshalJSON_via_struct(t *testing.T) {
	t.Parallel()

	// Verify it works through json.Unmarshal on a containing struct.
	type wrapper struct {
		EventType HistoryEventType `json:"eventType"`
	}

	t.Run("sonarr integer in struct", func(t *testing.T) {
		t.Parallel()
		var w wrapper
		err := json.Unmarshal([]byte(`{"eventType":3}`), &w)
		if err != nil {
			t.Fatalf("json.Unmarshal error: %v", err)
		}
		if w.EventType != HistoryImported {
			t.Errorf("EventType = %d, want %d", w.EventType, HistoryImported)
		}
	})

	t.Run("radarr string in struct", func(t *testing.T) {
		t.Parallel()
		var w wrapper
		err := json.Unmarshal([]byte(`{"eventType":"downloadFolderImported"}`), &w)
		if err != nil {
			t.Fatalf("json.Unmarshal error: %v", err)
		}
		if w.EventType != HistoryImported {
			t.Errorf("EventType = %d, want %d", w.EventType, HistoryImported)
		}
	})
}

// --- HistoryEntry.ImportedPath / FileID ---

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
			h := &HistoryEntry{Data: tt.data}

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

// --- Series.OriginalLangCode ---

func TestSeries_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *LanguageInfo
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &LanguageInfo{Name: "English", ID: 1}, "en"},
		{"unknown language", &LanguageInfo{Name: "Klingon", ID: 99}, ""},
		{"empty name", &LanguageInfo{Name: "", ID: 0}, ""},
		{"two-letter code", &LanguageInfo{Name: "fr", ID: 2}, "fr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Series{OriginalLanguage: tt.lang}

			got := s.OriginalLangCode()

			if got != tt.want {
				t.Errorf("Series.OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Movie.OriginalLangCode ---

func TestMovie_OriginalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang *LanguageInfo
		want string
	}{
		{"nil language", nil, ""},
		{"known language", &LanguageInfo{Name: "French", ID: 2}, "fr"},
		{"unknown language", &LanguageInfo{Name: "Dothraki", ID: 99}, ""},
		{"empty name", &LanguageInfo{Name: "", ID: 0}, ""},
		{"two-letter code", &LanguageInfo{Name: "de", ID: 3}, "de"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Movie{OriginalLanguage: tt.lang}

			got := m.OriginalLangCode()

			if got != tt.want {
				t.Errorf("Movie.OriginalLangCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Series.SeasonEpisodeFileCount ---

func TestSeries_SeasonEpisodeFileCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		seasons   []SeasonInfo
		seasonNum int
		want      int
	}{
		{"no seasons", nil, 1, 0},
		{"season found with stats", []SeasonInfo{
			{SeasonNumber: 1, Statistics: &SeasonStatistics{EpisodeFileCount: 10}},
			{SeasonNumber: 2, Statistics: &SeasonStatistics{EpisodeFileCount: 5}},
		}, 1, 10},
		{"season found nil stats", []SeasonInfo{
			{SeasonNumber: 1, Statistics: nil},
		}, 1, 0},
		{"season not found", []SeasonInfo{
			{SeasonNumber: 1, Statistics: &SeasonStatistics{EpisodeFileCount: 10}},
		}, 99, 0},
		{"season zero (specials)", []SeasonInfo{
			{SeasonNumber: 0, Statistics: &SeasonStatistics{EpisodeFileCount: 3}},
		}, 0, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Series{Seasons: tt.seasons}

			got := s.SeasonEpisodeFileCount(tt.seasonNum)

			if got != tt.want {
				t.Errorf("SeasonEpisodeFileCount(%d) = %d, want %d",
					tt.seasonNum, got, tt.want)
			}
		})
	}
}

// --- EpisodeFile.AudioLanguages ---

func TestEpisodeFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *EpisodeFile
		want []string
	}{
		{"nil media info", &EpisodeFile{MediaInfo: nil}, nil},
		{"empty audio languages", &EpisodeFile{
			MediaInfo: &MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &EpisodeFile{
			MediaInfo: &MediaInfo{AudioLanguages: "English"},
		}, []string{"en"}},
		{"multiple languages", &EpisodeFile{
			MediaInfo: &MediaInfo{AudioLanguages: "English/Japanese"},
		}, []string{"en", "ja"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.file.AudioLanguages()

			if !slices.Equal(got, tt.want) {
				t.Errorf("EpisodeFile.AudioLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- MovieFile.AudioLanguages ---

func TestMovieFile_AudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file *MovieFile
		want []string
	}{
		{"nil media info", &MovieFile{MediaInfo: nil}, nil},
		{"empty audio languages", &MovieFile{
			MediaInfo: &MediaInfo{AudioLanguages: ""},
		}, nil},
		{"single language", &MovieFile{
			MediaInfo: &MediaInfo{AudioLanguages: "French"},
		}, []string{"fr"}},
		{"multiple languages", &MovieFile{
			MediaInfo: &MediaInfo{AudioLanguages: "English,French,German"},
		}, []string{"en", "fr", "de"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.file.AudioLanguages()

			if !slices.Equal(got, tt.want) {
				t.Errorf("MovieFile.AudioLanguages() = %v, want %v", got, tt.want)
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

func TestHistoryEventType_UnmarshalJSON_sonarr_round_trip(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 1000).Draw(t, "event_type")
		data, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("json.Marshal(%d) error: %v", n, err)
		}

		var got HistoryEventType
		err = got.UnmarshalJSON(data)
		if err != nil {
			t.Fatalf("UnmarshalJSON(%s) error: %v", data, err)
		}
		if int(got) != n {
			t.Errorf("UnmarshalJSON(%s) = %d, want %d", data, got, n)
		}
	})

	// Negative ints normalize to 0 (treat as unknown event type) rather than
	// round-tripping. The arr APIs only emit non-negative event types.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(-1000, -1).Draw(t, "negative_event_type")
		data, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("json.Marshal(%d) error: %v", n, err)
		}
		var got HistoryEventType
		if err := got.UnmarshalJSON(data); err != nil {
			t.Fatalf("UnmarshalJSON(%s) error: %v", data, err)
		}
		if got != 0 {
			t.Errorf("UnmarshalJSON(%s) = %d, want 0 (negative-normalize)", data, got)
		}
	})
}

func TestHistoryEventType_UnmarshalJSON_never_panics(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.SliceOfN(rapid.Byte(), 0, 200).Draw(t, "raw")

		var got HistoryEventType
		_ = got.UnmarshalJSON(raw) // must not panic
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

// TestHistoryEventType_UnmarshalJSON_logs_unknown_event_only_when_nonempty
// pins the empty-string guard around the unknown-event DEBUG log: an unknown
// non-empty event string is reported once, while an empty event string is
// dropped silently. Not t.Parallel: captureSlog swaps the default logger.
func TestHistoryEventType_UnmarshalJSON_logs_unknown_event_only_when_nonempty(t *testing.T) {
	prev := loggedUnknownEvents
	loggedUnknownEvents = newLogOnce(256) // fresh dedup set so first() returns true
	t.Cleanup(func() { loggedUnknownEvents = prev })

	// Unknown non-empty event string: logged once at DEBUG, resolves to 0.
	buf := captureSlog(t)
	var h HistoryEventType
	if err := h.UnmarshalJSON([]byte(`"someUnknownEvent"`)); err != nil {
		t.Fatalf("UnmarshalJSON(unknown string) error = %v, want nil", err)
	}
	if h != 0 {
		t.Errorf("HistoryEventType(unknown string) = %d, want 0", int(h))
	}
	if !strings.Contains(buf.String(), "unknown event type") {
		t.Errorf("non-empty unknown event: expected DEBUG log, got %q", buf.String())
	}

	// Empty event string: the guard skips logging entirely.
	buf2 := captureSlog(t)
	var h2 HistoryEventType
	if err := h2.UnmarshalJSON([]byte(`""`)); err != nil {
		t.Fatalf("UnmarshalJSON(empty string) error = %v, want nil", err)
	}
	if buf2.Len() != 0 {
		t.Errorf("empty event string: expected no log, got %q", buf2.String())
	}
}
