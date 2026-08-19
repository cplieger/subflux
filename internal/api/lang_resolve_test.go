package api

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestLogUnknownLang_logs_only_nonempty_name pins the empty-input guard in
// logUnknownLang: a non-empty unmapped name is reported once at DEBUG, while a
// name that trims to empty is dropped silently. This must not be t.Parallel
// because captureSlog swaps the process-wide default logger.
func TestLogUnknownLang_logs_only_nonempty_name(t *testing.T) {
	prev := loggedUnknownLangs
	loggedUnknownLangs = newLogOnce(256) // fresh dedup set so first() returns true
	t.Cleanup(func() { loggedUnknownLangs = prev })

	// Non-empty unmapped name: proceeds past the guard and logs once.
	buf := captureSlog(t)
	logUnknownLang("klingon")
	if !strings.Contains(buf.String(), "unmapped name") {
		t.Errorf("non-empty name: expected DEBUG log, got %q", buf.String())
	}

	// Whitespace-only name trims to empty: the guard returns early, no log.
	buf2 := captureSlog(t)
	logUnknownLang("   ")
	if buf2.Len() != 0 {
		t.Errorf("empty (trimmed) name: expected no log, got %q", buf2.String())
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
		{"three letter code is canonicalized", "eng", "en"},
		{"unassigned two letter code rejected", "bb", ""},
		{"unknown language name", "klingon", ""},
		{"single character", "e", ""},
		{"numeric string", "42", ""},
		{"special characters", "en!", ""},
		{"whitespace only", " ", ""},
		// Brazilian Portuguese is a separate subtitle target from European
		// Portuguese, so the arr name must resolve to the internal "pb" code;
		// answering "pt" matched the wrong language rule.
		{"regional variant with parens", "Portuguese (Brazil)", "pb"},
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
		{"regional variant with parens", "English/Portuguese (Brazil)", []string{"en", "pb"}},
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

// A two-letter code that names a real language resolves to its canonical
// spelling; one the IANA registry does not assign is rejected rather than echoed
// back. Echoing it back put a non-language in the code space, where it became a
// filename segment and a state key matching no configured target.
// A two-letter code that names a real language resolves to its canonical
// spelling; one the IANA registry does not assign is rejected rather than echoed
// back. Echoing it back put a non-language in the code space, where it became a
// filename segment and a state key matching no configured target.
func TestLangNameToISO_two_letter_code_handling(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		code := rapid.StringMatching(`[a-z]{2}`).Draw(t, "code")

		got := LangNameToISO(code)

		if want := CanonicalLangCode(code); got != want {
			t.Errorf("LangNameToISO(%q) = %q, want %q", code, got, want)
		}
		if got != "" && len(got) != 2 {
			t.Errorf("LangNameToISO(%q) = %q (len %d), want len 2", code, got, len(got))
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
