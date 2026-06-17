package config

// Tests in this file target surviving gremlins mutants in internal/config
// (unit subflux-u20). Each test asserts an observable outcome that differs
// between the original operator and its mutation. Tests only; no production
// code is modified.

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// gk_subflux_u20_captureLogs runs fn with the default slog logger swapped for
// a text handler writing to a buffer, and returns the captured output. The
// previous default logger is restored on return. These tests are not run in
// parallel so the global logger swap is safe.
func gk_subflux_u20_captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// --- config_accessors_search.go:51 (CONDITIONALS_NEGATION on "== 0") ---

func Test_gk_subflux_u20_SyncConfig_default_min_confidence(t *testing.T) {
	// Zero confidence is replaced by the default (0.6). A "!= 0" mutation would
	// leave it at 0 for this input.
	czero := &Config{}
	czero.PostProcessing.SyncMinConfidence = 0
	if got := czero.SyncConfig().SyncMinConfidence; got != 0.6 {
		t.Errorf("SyncConfig().SyncMinConfidence(input=0) = %v, want 0.6 (DefaultSyncMinConfidence)", got)
	}

	// A non-zero confidence must be preserved (the "== 0" branch must NOT fire).
	// A "!= 0" mutation would wrongly overwrite it with the default.
	cset := &Config{}
	cset.PostProcessing.SyncMinConfidence = 0.42
	if got := cset.SyncConfig().SyncMinConfidence; got != 0.42 {
		t.Errorf("SyncConfig().SyncMinConfidence(input=0.42) = %v, want 0.42", got)
	}
}

// --- config_accessors_search.go:59 (CONDITIONALS_NEGATION on "!= nil") ---

func Test_gk_subflux_u20_ProviderConfigs_uses_cache_when_present(t *testing.T) {
	c := &Config{}
	c.cachedProviderConfigs = map[api.ProviderID]api.ProviderCfg{
		api.ProviderID("gk_subflux_u20_cached"): {Priority: 7, Enabled: true},
	}
	// Distinct fallback source so original (cache) vs mutated (fallback) differ.
	c.Providers = map[api.ProviderID]yamlProviderCfg{
		api.ProviderID("gk_subflux_u20_fallback"): {Enabled: true, Priority: 9},
	}

	got := c.ProviderConfigs()
	if _, ok := got[api.ProviderID("gk_subflux_u20_cached")]; !ok {
		t.Errorf("ProviderConfigs() = %v, want the cached map (key gk_subflux_u20_cached present)", got)
	}
	if _, ok := got[api.ProviderID("gk_subflux_u20_fallback")]; ok {
		t.Errorf("ProviderConfigs() = %v, want the cached map, not the fallback built from Providers", got)
	}
}

// --- config_load.go:59 (CONDITIONALS_BOUNDARY on "ns > maxDurationFloat") ---

func Test_gk_subflux_u20_parseMul_boundary_no_overflow_at_max(t *testing.T) {
	// With unit=1ns and n=2^63, ns == maxDurationFloat (float64(1<<63-1) rounds
	// to 2^63) exactly. Original "ns > maxDurationFloat" is false -> no error.
	// A ">=" mutation would (wrongly) report overflow.
	if _, err := parseMul("9223372036854775808X", time.Duration(1), "gk_subflux_u20"); err != nil {
		t.Errorf("parseMul(ns == maxDurationFloat) = err %v, want nil (boundary is not overflow)", err)
	}
}

// --- config_load.go:168,169 (CONDITIONALS_NEGATION on the logged URL flags) ---

func Test_gk_subflux_u20_Load_logs_arr_flags(t *testing.T) {
	// minimalValidYAML configures sonarr (URL set) but not radarr, so the
	// "config loaded" line logs sonarr=true and radarr=false.
	path := writeConfig(t, minimalValidYAML())
	out := gk_subflux_u20_captureLogs(t, func() {
		cfg, err := Load(context.Background(), path)
		if err != nil {
			t.Fatalf("Load(minimalValidYAML) = err %v, want nil", err)
		}
		_ = cfg.Close()
	})

	if !strings.Contains(out, "sonarr=true") {
		t.Errorf("Load log = %q, want it to contain sonarr=true (SonarrConfig().URL != \"\")", out)
	}
	if !strings.Contains(out, "radarr=false") {
		t.Errorf("Load log = %q, want it to contain radarr=false (RadarrConfig().URL != \"\")", out)
	}
}

// --- config_load.go:302,309,313 (CONDITIONALS_NEGATION in buildCaches media-root loop) ---

func Test_gk_subflux_u20_buildCaches_opens_valid_media_root(t *testing.T) {
	cfg := &Config{MediaRootDirs: []string{t.TempDir()}}
	cfg.buildCaches(context.Background())
	defer func() { _ = cfg.Close() }()

	// Live ctx (302 "!= nil" false -> continue) + live ctx in the loop
	// (309 "!= nil" false -> no break) + successful OpenRoot
	// (313 "!= nil" false -> append) => exactly one cached root.
	// Each of the three "== nil" mutations would yield zero.
	if len(cfg.cachedRoots) != 1 {
		t.Fatalf("buildCaches(live ctx, 1 valid root): len(cachedRoots) = %d, want 1", len(cfg.cachedRoots))
	}
}

// --- config_validate.go:142,146,150 (CONDITIONALS_NEGATION in validate media-root loop) ---

func Test_gk_subflux_u20_validate_inaccessible_media_root_logged(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gk_subflux_u20_missing")
	cfg := &Config{MediaRootDirs: []string{missing}}

	out := gk_subflux_u20_captureLogs(t, func() {
		// validate returns other errors (no arr, etc.); we only care that it
		// reaches the media-root stat loop and logs the inaccessible warning.
		_ = validate(context.Background(), cfg)
	})

	// 142 mutated -> "media_roots not configured" branch (no stat).
	// 146 mutated -> break before stat.
	// 150 mutated -> no warn for the failing stat.
	// All three suppress this exact message.
	if !strings.Contains(out, "media root not accessible") {
		t.Errorf("validate(nonexistent media root) log = %q, want it to contain \"media root not accessible\"", out)
	}
}

func Test_gk_subflux_u20_validate_existing_media_root_no_warn(t *testing.T) {
	cfg := &Config{MediaRootDirs: []string{t.TempDir()}}
	out := gk_subflux_u20_captureLogs(t, func() {
		_ = validate(context.Background(), cfg)
	})

	// Existing root: os.Stat succeeds, so "150" (err != nil) is false -> no warn.
	// A "== nil" mutation at 150 would warn for the existing root.
	if strings.Contains(out, "media root not accessible") {
		t.Errorf("validate(existing media root) log = %q, want NO \"media root not accessible\"", out)
	}
}

// --- config_validate.go:188 (CONDITIONALS_BOUNDARY on "c.Retention < 1") ---

func Test_gk_subflux_u20_validateBackup_retention_boundary(t *testing.T) {
	// Retention exactly 1 is valid: "< 1" is false. A "<= 1" mutation rejects it.
	// Frequency == MinBackupFrequency (1h) keeps the duration check passing.
	c := &yamlBackupConfig{Enabled: true, Retention: 1, Frequency: Duration{D: time.Hour}}
	if err := validateBackup(c); err != nil {
		t.Errorf("validateBackup(retention=1, freq=1h) = %v, want nil", err)
	}
}

// --- config_validate.go:225,228:11,228:32 (CONDITIONALS_NEGATION in warnArrURLs) ---

func Test_gk_subflux_u20_warnArrURLs_url_only(t *testing.T) {
	// URL set, PublicURL empty: original logs the "public_url not set" warning.
	out := gk_subflux_u20_captureLogs(t, func() {
		warnArrURLs("sonarr", yamlArrConfig{URL: "http://sonarr:8989"})
	})

	// 225 mutated ("URL != \"\"") -> early return, no warn.
	// 228:11 mutated ("URL == \"\"") -> branch false, no warn.
	// 228:32 mutated ("PublicURL != \"\"") -> branch false, no warn.
	if !strings.Contains(out, "public_url not set") {
		t.Errorf("warnArrURLs(url-only) log = %q, want it to contain \"public_url not set\"", out)
	}
}

// --- config_validate.go:232:17,232:32 (CONDITIONALS_NEGATION in warnArrURLs) ---

func Test_gk_subflux_u20_warnArrURLs_public_url_only(t *testing.T) {
	// PublicURL set, URL empty: original logs the "falling back to public_url" warning.
	out := gk_subflux_u20_captureLogs(t, func() {
		warnArrURLs("radarr", yamlArrConfig{PublicURL: "http://radarr:7878"})
	})

	// 232:17 mutated ("PublicURL == \"\"") -> branch false, no warn.
	// 232:32 mutated ("URL != \"\"") -> branch false, no warn.
	if !strings.Contains(out, "falling back to public_url") {
		t.Errorf("warnArrURLs(public-url-only) log = %q, want it to contain \"falling back to public_url\"", out)
	}
}

// --- config_validate_search.go:20 (CONDITIONALS_BOUNDARY + CONDITIONALS_NEGATION on "<= 0") ---

func Test_gk_subflux_u20_validateSearch_download_max_attempts(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		// "<= 0" true -> defaulted to 3. "< 0" mutation: 0<0 false -> stays 0.
		// ">  0" mutation: 0>0 false -> stays 0. Either leaves 0 != 3.
		{"zero gets default", 0, 3},
		// "<= 0" false -> preserved. ">  0" mutation: 5>0 true -> overwritten to 3.
		{"positive preserved", 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := yamlSearchConfig{
				DownloadMaxAttempts: tc.in,
				ScanDelay:           Duration{D: 5 * time.Second},
				ScanInterval:        Duration{D: time.Hour},
			}
			_ = validateSearch(&s)
			if s.DownloadMaxAttempts != tc.want {
				t.Errorf("validateSearch(DownloadMaxAttempts=%d) -> %d, want %d", tc.in, s.DownloadMaxAttempts, tc.want)
			}
		})
	}
}
