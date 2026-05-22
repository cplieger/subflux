package config

import (
	"context"
	"strconv"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// --- ResolveTargetsWithFallback ---

func TestResolveTargetsWithFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		rules        []AudioRule
		defaults     []yamlSubtitleTarget
		originalLang string
		audioLangs   []string
		wantCodes    []string
	}{
		{
			name:         "original_lang_priority",
			rules:        []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}, {Audio: "ja", Subtitles: []yamlSubtitleTarget{{Code: "en"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "en",
			audioLangs:   []string{"ja"},
			wantCodes:    []string{"fr"},
		},
		{
			name:         "audio_track_fallback",
			rules:        []AudioRule{{Audio: "ja", Subtitles: []yamlSubtitleTarget{{Code: "en"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "ko",
			audioLangs:   []string{"ja"},
			wantCodes:    []string{"en"},
		},
		{
			name:         "second_audio_track_matches",
			rules:        []AudioRule{{Audio: "ja", Subtitles: []yamlSubtitleTarget{{Code: "en"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "ko",
			audioLangs:   []string{"zh", "ja"},
			wantCodes:    []string{"en"},
		},
		{
			name:         "default",
			rules:        []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "ko",
			audioLangs:   []string{"zh"},
			wantCodes:    []string{"de"},
		},
		{
			name:         "empty_original_lang",
			rules:        []AudioRule{{Audio: "ja", Subtitles: []yamlSubtitleTarget{{Code: "en"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "",
			audioLangs:   []string{"ja"},
			wantCodes:    []string{"en"},
		},
		{
			name:         "empty_everything",
			rules:        []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "",
			audioLangs:   nil,
			wantCodes:    []string{"de"},
		},
		{
			name:         "matched_rule_with_nil_subtitles",
			rules:        []AudioRule{{Audio: "en", Subtitles: nil}},
			defaults:     []yamlSubtitleTarget{{Code: "de"}},
			originalLang: "en",
			audioLangs:   nil,
			wantCodes:    []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{
				Languages: LanguageRules{Rules: tt.rules, Default: tt.defaults},
			}
			targets := cfg.ResolveTargetsWithFallback(tt.originalLang, tt.audioLangs)
			if tt.name == "matched_rule_with_nil_subtitles" {
				if targets == nil {
					t.Fatal("ResolveTargetsWithFallback() = nil, want non-nil empty slice")
				}
				if len(targets) != 0 {
					t.Errorf("ResolveTargetsWithFallback() = %v, want empty", targets)
				}
				return
			}
			if len(targets) != len(tt.wantCodes) {
				t.Fatalf("ResolveTargetsWithFallback() returned %d targets, want %d", len(targets), len(tt.wantCodes))
			}
			for i, want := range tt.wantCodes {
				if targets[i].Code != want {
					t.Errorf("targets[%d].Code = %q, want %q", i, targets[i].Code, want)
				}
			}
		})
	}
}

// --- LanguageCodes ---

func TestLanguageCodes_deduplicates(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Languages: LanguageRules{
			Rules: []AudioRule{
				{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}, {Code: "de"}}},
				{Audio: "ja", Subtitles: []yamlSubtitleTarget{{Code: "en"}, {Code: "fr"}}},
			},
			Default: []yamlSubtitleTarget{{Code: "en"}, {Code: "fr"}},
		},
	}

	codes := cfg.LanguageCodes()
	seen := make(map[string]int)
	for _, c := range codes {
		seen[c]++
	}
	for code, count := range seen {
		if count > 1 {
			t.Errorf("LanguageCodes() has duplicate %q (%d times)", code, count)
		}
	}
	if len(codes) != 3 {
		t.Errorf("LanguageCodes() returned %d codes, want 3 (fr, de, en)", len(codes))
	}
}

func TestLanguageCodes_empty_config(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	codes := cfg.LanguageCodes()
	if len(codes) != 0 {
		t.Errorf("LanguageCodes() = %v, want empty", codes)
	}
}

func TestLanguageCodes_default_adds_new_code(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Languages: LanguageRules{
			Rules:   []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{{Code: "fr"}}}},
			Default: []yamlSubtitleTarget{{Code: "de"}},
		},
	}

	codes := cfg.LanguageCodes()
	if len(codes) != 2 {
		t.Fatalf("LanguageCodes() returned %d codes, want 2", len(codes))
	}
	if codes[0] != "fr" {
		t.Errorf("LanguageCodes()[0] = %q, want %q", codes[0], "fr")
	}
	if codes[1] != "de" {
		t.Errorf("LanguageCodes()[1] = %q, want %q", codes[1], "de")
	}
}

func TestLanguageCodes_default_only(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Languages: LanguageRules{
			Default: []yamlSubtitleTarget{{Code: "en"}, {Code: "fr"}},
		},
	}

	codes := cfg.LanguageCodes()
	if len(codes) != 2 {
		t.Fatalf("LanguageCodes() returned %d codes, want 2", len(codes))
	}
	if codes[0] != "en" {
		t.Errorf("LanguageCodes()[0] = %q, want %q", codes[0], "en")
	}
	if codes[1] != "fr" {
		t.Errorf("LanguageCodes()[1] = %q, want %q", codes[1], "fr")
	}
}

func TestLanguageCodes_never_has_duplicates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nRules := rapid.IntRange(0, 5).Draw(t, "nRules")
		rules := make([]AudioRule, nRules)
		for i := range nRules {
			nSubs := rapid.IntRange(1, 4).Draw(t, "nSubs")
			subs := make([]yamlSubtitleTarget, nSubs)
			for j := range nSubs {
				subs[j] = yamlSubtitleTarget{Code: rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "code")}
			}
			rules[i] = AudioRule{
				Audio:     rapid.StringMatching(`[a-z]{2}`).Draw(t, "audio"),
				Subtitles: subs,
			}
		}
		nDefaults := rapid.IntRange(0, 3).Draw(t, "nDefaults")
		defaults := make([]yamlSubtitleTarget, nDefaults)
		for i := range nDefaults {
			defaults[i] = yamlSubtitleTarget{Code: rapid.StringMatching(`[a-z]{2,3}`).Draw(t, "defCode")}
		}

		cfg := &Config{
			Languages: LanguageRules{Rules: rules, Default: defaults},
		}
		codes := cfg.LanguageCodes()

		seen := make(map[string]struct{})
		for _, c := range codes {
			if _, dup := seen[c]; dup {
				t.Errorf("LanguageCodes() has duplicate %q", c)
			}
			seen[c] = struct{}{}
		}
	})
}

// --- MinScoreForTarget ---

func TestMinScoreForTarget_per_target_override(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SearchCfg: yamlSearchConfig{MinScore: 50},
	}
	target := &api.SubtitleTarget{Code: "fr", MinScore: new(75)}

	got := cfg.MinScoreForTarget(target, "episode")
	if got != 75 {
		t.Errorf("MinScoreForTarget(override) = %d, want 75", got)
	}
}

func TestMinScoreForTarget_falls_back_to_global(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		SearchCfg: yamlSearchConfig{MinScore: 50},
	}
	target := &api.SubtitleTarget{Code: "fr"}

	got := cfg.MinScoreForTarget(target, "episode")
	if got != 50 {
		t.Errorf("MinScoreForTarget(fallback) = %d, want 50", got)
	}
}

// --- Scoring weights ---

func TestScores_defaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	got := cfg.Scores()
	if got != api.DefaultScores {
		t.Errorf("Scores() = %+v, want defaults", got)
	}
}

func TestScores_custom(t *testing.T) {
	t.Parallel()
	custom := api.Scores{Hash: 999}
	cfg := &Config{
		Scoring: ScoringConfig{
			Weights: &custom,
		},
	}
	got := cfg.Scores()
	if got.Hash != 999 {
		t.Errorf("Scores().Hash = %d, want 999", got.Hash)
	}
}

// --- LanguageRulesForUI ---

func TestLanguageRulesForUI_rules_and_defaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Languages: LanguageRules{
			Rules: []AudioRule{
				{Audio: "en", Subtitles: []yamlSubtitleTarget{
					{Code: "fr", Variant: "standard"},
					{Code: "fr", Variant: "forced", MinScore: new(80)},
				}},
				{Audio: "ja", Subtitles: []yamlSubtitleTarget{
					{Code: "en", Providers: []api.ProviderID{"opensubtitles"}},
				}},
			},
			Default: []yamlSubtitleTarget{
				{Code: "en", Exclude: []api.ProviderID{"hdbits"}},
			},
		},
	}

	got := cfg.LanguageRulesForUI()

	if len(got.Rules) != 2 {
		t.Fatalf("LanguageRulesForUI().Rules has %d entries, want 2", len(got.Rules))
	}
	if got.Rules[0].Audio != "en" {
		t.Errorf("Rules[0].Audio = %q, want %q", got.Rules[0].Audio, "en")
	}
	if len(got.Rules[0].Subtitles) != 2 {
		t.Fatalf("Rules[0].Subtitles has %d entries, want 2", len(got.Rules[0].Subtitles))
	}
	if got.Rules[0].Subtitles[0].Code != "fr" {
		t.Errorf("Rules[0].Subtitles[0].Code = %q, want %q", got.Rules[0].Subtitles[0].Code, "fr")
	}
	if got.Rules[0].Subtitles[0].Variant != "standard" {
		t.Errorf("Rules[0].Subtitles[0].Variant = %q, want %q", got.Rules[0].Subtitles[0].Variant, "standard")
	}
	if got.Rules[0].Subtitles[1].MinScore == nil || *got.Rules[0].Subtitles[1].MinScore != 80 {
		t.Errorf("Rules[0].Subtitles[1].MinScore = %v, want 80", got.Rules[0].Subtitles[1].MinScore)
	}
	if got.Rules[1].Audio != "ja" {
		t.Errorf("Rules[1].Audio = %q, want %q", got.Rules[1].Audio, "ja")
	}
	if len(got.Rules[1].Subtitles[0].Providers) != 1 || got.Rules[1].Subtitles[0].Providers[0] != "opensubtitles" {
		t.Errorf("Rules[1].Subtitles[0].Providers = %v, want [opensubtitles]", got.Rules[1].Subtitles[0].Providers)
	}

	if len(got.Default) != 1 {
		t.Fatalf("LanguageRulesForUI().Default has %d entries, want 1", len(got.Default))
	}
	if got.Default[0].Code != "en" {
		t.Errorf("Default[0].Code = %q, want %q", got.Default[0].Code, "en")
	}
	if len(got.Default[0].Exclude) != 1 || got.Default[0].Exclude[0] != "hdbits" {
		t.Errorf("Default[0].Exclude = %v, want [hdbits]", got.Default[0].Exclude)
	}
}

func TestLanguageRulesForUI_empty_config(t *testing.T) {
	t.Parallel()
	cfg := &Config{}

	got := cfg.LanguageRulesForUI()
	if len(got.Rules) != 0 {
		t.Errorf("LanguageRulesForUI().Rules = %v, want empty", got.Rules)
	}
	if len(got.Default) != 0 {
		t.Errorf("LanguageRulesForUI().Default = %v, want empty", got.Default)
	}
}

// --- BenchmarkResolveTargetsWithFallback ---

func BenchmarkResolveTargetsWithFallback(b *testing.B) {
	makeConfig := func(nRules int) *Config {
		rules := make([]AudioRule, nRules)
		for i := range rules {
			rules[i] = AudioRule{
				Audio:     "lang" + strconv.Itoa(i),
				Subtitles: []yamlSubtitleTarget{{Code: "sub" + strconv.Itoa(i)}},
			}
		}
		cfg := &Config{
			Languages: LanguageRules{
				Rules:   rules,
				Default: []yamlSubtitleTarget{{Code: "en"}},
			},
		}
		// Trigger cache build by calling once.
		cfg.buildCaches(context.Background())
		return cfg
	}

	for _, nRules := range []int{1, 5, 20} {
		b.Run(strconv.Itoa(nRules)+"_rules", func(b *testing.B) {
			cfg := makeConfig(nRules)
			audioLangs := []string{"lang0", "lang1"}
			b.ResetTimer()
			for range b.N {
				cfg.ResolveTargetsWithFallback("lang0", audioLangs)
			}
		})
	}
}
