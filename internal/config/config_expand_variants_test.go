package config

import (
	"context"
	"errors"
	"testing"
)

// --- expandVariants ---

func TestExpandVariants_expands_array(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          variants: [standard, forced]
  default:
    - code: en
providers:
  yify:
    enabled: true
    settings: {}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	subs := cfg.Languages.Rules[0].Subtitles
	if len(subs) != 2 {
		t.Fatalf("expected 2 expanded targets, got %d", len(subs))
	}
	if subs[0].Variant != "standard" {
		t.Errorf("subs[0].Variant = %q, want %q", subs[0].Variant, "standard")
	}
	if subs[1].Variant != "forced" {
		t.Errorf("subs[1].Variant = %q, want %q", subs[1].Variant, "forced")
	}
	if subs[0].Code != "fr" || subs[1].Code != "fr" {
		t.Errorf("expanded targets should preserve code=fr")
	}
}

func TestExpandVariants_inherits_providers_and_min_score(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          variants: [standard, forced]
          providers: [opensubtitles]
          min_score: 100
  default:
    - code: en
providers:
  yify:
    enabled: true
    settings: {}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	for i, sub := range cfg.Languages.Rules[0].Subtitles {
		if len(sub.Providers) != 1 || sub.Providers[0] != "opensubtitles" {
			t.Errorf("subs[%d].Providers = %v, want [opensubtitles]", i, sub.Providers)
		}
		if sub.MinScore == nil || *sub.MinScore != 100 {
			t.Errorf("subs[%d].MinScore = %v, want 100", i, sub.MinScore)
		}
	}
}

func TestExpandVariants_single_variant_unchanged(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          variant: forced
  default:
    - code: en
providers:
  yify:
    enabled: true
    settings: {}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	subs := cfg.Languages.Rules[0].Subtitles
	if len(subs) != 1 {
		t.Fatalf("expected 1 target, got %d", len(subs))
	}
	if subs[0].Variant != "forced" {
		t.Errorf("Variant = %q, want %q", subs[0].Variant, "forced")
	}
}

func TestExpandVariants_both_variant_and_variants_errors(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          variant: normal
          variants: [standard, forced]
  default:
    - code: en
providers:
  yify:
    enabled: true
    settings: {}
`
	_, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err == nil {
		t.Fatal("expected error for both variant and variants set")
	}
	if !errors.Is(err, ErrVariantConflict) {
		t.Errorf("error = %q, want ErrVariantConflict", err)
	}
}

func TestExpandVariants_default_targets(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  default:
    - code: en
      variants: [standard, hi]
providers:
  yify:
    enabled: true
    settings: {}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if len(cfg.Languages.Default) != 2 {
		t.Fatalf("expected 2 default targets, got %d", len(cfg.Languages.Default))
	}
	if cfg.Languages.Default[0].Variant != "standard" {
		t.Errorf("default[0].Variant = %q, want %q", cfg.Languages.Default[0].Variant, "standard")
	}
	if cfg.Languages.Default[1].Variant != "hi" {
		t.Errorf("default[1].Variant = %q, want %q", cfg.Languages.Default[1].Variant, "hi")
	}
}

func TestExpandVariants_no_variant_stays_empty(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	sub := cfg.Languages.Rules[0].Subtitles[0]
	if sub.Variant != "" {
		t.Errorf("Variant = %q, want empty (defaults to standard via EffectiveVariant)", sub.Variant)
	}
	apiSub := sub.toAPI()
	if apiSub.EffectiveVariant() != "standard" {
		t.Errorf("EffectiveVariant() = %q, want %q", apiSub.EffectiveVariant(), "standard")
	}
}

func TestExpandVariants_preserves_exclude(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
          variants: [standard, forced]
          exclude: [hdbits]
  default:
    - code: en
providers:
  yify:
    enabled: true
    settings: {}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	for i, sub := range cfg.Languages.Rules[0].Subtitles {
		if len(sub.Exclude) != 1 || sub.Exclude[0] != "hdbits" {
			t.Errorf("subs[%d].Exclude = %v, want [hdbits]", i, sub.Exclude)
		}
	}
}

// --- Coverage gap: expandVariants default-target error ---

func TestExpandVariants_both_variant_and_variants_in_default_errors(t *testing.T) {
	t.Parallel()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  default:
    - code: en
      variant: normal
      variants: [standard, forced]
providers:
  yify:
    enabled: true
    settings: {}
`
	_, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err == nil {
		t.Fatal("expected error for both variant and variants in default target")
	}
	if !errors.Is(err, ErrVariantConflict) {
		t.Errorf("error = %q, want ErrVariantConflict", err)
	}
}
