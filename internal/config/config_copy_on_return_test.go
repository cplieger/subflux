package config

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// loadedConfig returns a Config that has been through Validate + buildCaches,
// so the accessors under test take their CACHED path — the one that used to
// hand out the live value while the recomputed path built a fresh one.
func loadedConfig(t *testing.T) *Config {
	t.Helper()

	cfg := &Config{
		SonarrCfg: yamlArrConfig{URL: "http://sonarr:8989", APIKey: "test-key"},
		Languages: LanguageRules{
			Rules: []AudioRule{{Audio: "en", Subtitles: []yamlSubtitleTarget{
				{Code: "fr", Variants: []string{"forced"}, Providers: []subflux.ProviderID{"a"}, Exclude: []subflux.ProviderID{"b"}},
			}}},
			Default: []yamlSubtitleTarget{
				{Code: "en", Variants: []string{"hi"}, Providers: []subflux.ProviderID{"c"}, Exclude: []subflux.ProviderID{"d"}},
			},
		},
		ProvidersCfg: map[subflux.ProviderID]yamlProviderCfg{
			"test": {Enabled: true, Settings: map[string]any{"token": "live"}},
		},
		TrustedProxies:  []string{"10.0.0.0/8"},
		PollIntervalCfg: Duration{D: 30 * time.Second},
		Cfg:             yamlSearchConfig{ScanDelay: minScanDelay, ScanInterval: Duration{D: time.Hour}, UpgradeWindowDays: 7},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	cfg.buildCaches(t.Context())
	return cfg
}

// The provider map's VALUES hold a settings map, so a shallow copy of the
// outer map would leave the aliasing that matters untouched: a caller editing
// a setting would be editing what every subsequent scan reads.
func TestProvidersCopiesInnerSettings(t *testing.T) {
	t.Parallel()

	cfg := loadedConfig(t)
	got := cfg.Providers()
	got["test"].Settings["token"] = "tampered"
	delete(got, "test")

	after := cfg.Providers()
	if _, ok := after["test"]; !ok {
		t.Error("deleting from the returned map removed the provider from the config")
	}
	if v := after["test"].Settings["token"]; v != "live" {
		t.Errorf("settings token = %v, want %q (caller edited the live config)", v, "live")
	}
}

// This set decides whether X-Forwarded-For is honoured, and net.IPNet's fields
// are byte slices: a caller must not be able to widen the trusted range, in
// place or by replacing an element.
func TestTrustedProxyNetsCopiesElements(t *testing.T) {
	t.Parallel()

	cfg := loadedConfig(t)
	got := cfg.TrustedProxyNets()
	if len(got) != 1 {
		t.Fatalf("TrustedProxyNets() len = %d, want 1", len(got))
	}
	// Widen the mask to /0 in place: every address would be trusted.
	for i := range got[0].Mask {
		got[0].Mask[i] = 0
	}
	got[0].IP = net.IPv4zero

	after := cfg.TrustedProxyNets()
	if ones, _ := after[0].Mask.Size(); ones != 8 {
		t.Errorf("mask = /%d, want /8 (caller widened the live trust set)", ones)
	}
	if !containsIP(after, "10.9.8.7") {
		t.Error("live set should still contain 10.9.8.7")
	}
	if containsIP(after, "8.8.8.8") {
		t.Error("live set trusts 8.8.8.8: the returned element aliased the cached one")
	}
}

func TestLanguageCodesCopiesOnReturn(t *testing.T) {
	t.Parallel()

	cfg := loadedConfig(t)
	got := cfg.LanguageCodes()
	want := slices.Clone(got)
	for i := range got {
		got[i] = "zz"
	}

	if after := cfg.LanguageCodes(); !slices.Equal(after, want) {
		t.Errorf("LanguageCodes() = %v, want %v (caller overwrote the cached codes)", after, want)
	}
}

// Targets carry a min-score pointer and three slices, so the copy has to reach
// inside each element as well as the slice holding them.
func TestResolveTargetsCopiesOnReturn(t *testing.T) {
	t.Parallel()

	cfg := loadedConfig(t)
	minScore := 42

	got := cfg.ResolveTargetsWithFallback("en", nil)
	if len(got) != 1 {
		t.Fatalf("ResolveTargetsWithFallback() len = %d, want 1", len(got))
	}
	got[0].Code = "zz"
	got[0].MinScore = &minScore
	got[0].Variants[0] = "tampered"
	got[0].Providers[0] = "tampered"
	got[0].Exclude[0] = "tampered"

	after := cfg.ResolveTargetsWithFallback("en", nil)
	if after[0].Code != "fr" {
		t.Errorf("rule target code = %q, want %q", after[0].Code, "fr")
	}
	if after[0].MinScore != nil {
		t.Errorf("rule target MinScore = %v, want nil", *after[0].MinScore)
	}
	if after[0].Variants[0] != "forced" {
		t.Errorf("rule target variant = %q, want %q", after[0].Variants[0], "forced")
	}
	if after[0].Providers[0] != "a" || after[0].Exclude[0] != "b" {
		t.Errorf("rule target providers/exclude = %v/%v, want a/b", after[0].Providers, after[0].Exclude)
	}

	// The default-targets path is cached separately and copies the same way.
	def := cfg.ResolveTargetsWithFallback("nl", nil)
	if len(def) != 1 {
		t.Fatalf("default targets len = %d, want 1", len(def))
	}
	def[0].Variants[0] = "tampered"
	if afterDef := cfg.ResolveTargetsWithFallback("nl", nil); afterDef[0].Variants[0] != "hi" {
		t.Errorf("default target variant = %q, want %q", afterDef[0].Variants[0], "hi")
	}
}

// The exclude-tag list is the only reference-typed field in SearchConfig, and
// the scan loop resolves it to arr tag IDs on every pass, so a caller that
// sorts or rewrites the returned slice in place would be rewriting what every
// later scan excludes. []string needs exactly one level of copy: a string is an
// immutable value, so the clone shares nothing further.
func TestSearchExcludeArrTagsCopiesOnReturn(t *testing.T) {
	t.Parallel()

	cfg := loadedConfig(t)
	cfg.Cfg.ExcludeArrTags = []string{"no-subflux", "skip-me"}

	got := cfg.Search().ExcludeArrTags
	if len(got) != 2 {
		t.Fatalf("Search().ExcludeArrTags len = %d, want 2", len(got))
	}
	got[0] = "tampered"
	got[1] = ""

	after := cfg.Search().ExcludeArrTags
	if !slices.Equal(after, []string{"no-subflux", "skip-me"}) {
		t.Errorf("Search().ExcludeArrTags = %v, want [no-subflux skip-me] (caller overwrote the live config)", after)
	}
}
