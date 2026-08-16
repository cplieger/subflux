package config

import (
	"os"
	"strings"
	"testing"
)

// TestValidateLanguages_codes covers the language-code check added when subflux
// adopted langtag. Before it, a code was accepted as long as it was non-empty,
// so a typo in config.yaml matched nothing for the lifetime of the install
// without ever saying so.
func TestValidateLanguages_codes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		lang        LanguageRules
		errContains string
		wantErr     bool
	}{
		{
			name: "plain ISO 639-1 codes pass",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
		},
		{
			// pb predates the library and is documented in config.example.yaml,
			// so an existing config carrying it must keep loading. This is the
			// backwards-compatibility guarantee of the whole migration.
			name: "documented internal pb passes as a target",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "pb"}}}},
				Default: []yamlSubtitleTarget{{Code: "pb"}},
			},
		},
		{
			name: "internal pb passes as an audio code",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "pb", Subtitles: []yamlSubtitleTarget{{Code: "en"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
		},
		{
			name: "a rule with no subtitles still validates its audio code",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "es", Subtitles: []yamlSubtitleTarget{}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
		},
		{
			name: "typo in a target code is rejected",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fq"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: "not a known language code",
		},
		{
			name: "typo in the default code is rejected",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "engg"}},
			},
			wantErr:     true,
			errContains: "not a known language code",
		},
		{
			name: "typo in an audio code is rejected",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "zz", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: "not a known language code",
		},
		{
			// A code that names a real language but is not how the internal
			// space spells it cannot match either, because nothing canonicalizes
			// a configured code at match time. The error names the spelling to
			// use, so recovery is one edit.
			name: "alpha-3 code is rejected and the canonical form named",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "eng"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: `use "en"`,
		},
		{
			name: "pt-BR is rejected in favour of the internal pb",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "pt-BR"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: `use "pb"`,
		},
		{
			name: "uppercase code is rejected in favour of lowercase",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "FR"}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: `use "fr"`,
		},
		{
			name: "empty code is still reported as empty, not as unknown",
			lang: LanguageRules{
				Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: ""}}}},
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
			wantErr:     true,
			errContains: "cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lang := tt.lang
			err := validateLanguages(&lang)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateLanguages(%+v) = nil, want an error", tt.lang)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateLanguages(%+v) error = %q, want it to contain %q",
						tt.lang, err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("validateLanguages(%+v) error = %v, want nil", tt.lang, err)
			}
		})
	}
}

// The shipped example config is written to disk on a fresh boot and by "Reset to
// defaults", so its language codes have to survive their own validation. The
// example documents pb, which is exactly the value the langtag adoption could
// have broken. The file is read from the repo root rather than embedded, because
// the embed lives in package main.
func TestValidateLanguages_shippedExampleConfig(t *testing.T) {
	t.Parallel()

	const path = "../../config.example.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", path, err)
	}
	// The example ships empty API keys on purpose, and that alone fails
	// validation before it reaches the language section. Fill them so the load
	// exercises the codes this test is about.
	filled := strings.ReplaceAll(string(raw), `api_key: ""`, `api_key: "example-key"`)

	cfg, err := LoadFromBytes(t.Context(), []byte(filled))
	if err != nil {
		t.Fatalf("LoadFromBytes(%q, api keys filled) error = %v, want nil", path, err)
	}
	if err := validateLanguages(&cfg.Languages); err != nil {
		t.Errorf("validateLanguages(%q languages) error = %v, want nil", path, err)
	}
}
