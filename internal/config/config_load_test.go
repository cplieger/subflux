package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- LoadFromBytes ---

func TestLoadFromBytes_minimal_valid(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.Sonarr.URL != "http://sonarr:8989" {
		t.Errorf("Sonarr.URL = %q, want %q", cfg.Sonarr.URL, "http://sonarr:8989")
	}
}

func TestLoadFromBytes_defaults_applied(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	if cfg.SearchCfg.ScanInterval.D != 24*time.Hour {
		t.Errorf("ScanInterval = %v, want 24h", cfg.SearchCfg.ScanInterval.D)
	}
	if cfg.SearchCfg.MinScore != 0 {
		t.Errorf("MinScore = %d, want 0", cfg.SearchCfg.MinScore)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("Logging.Format = %q, want %q", cfg.Logging.Format, "json")
	}
	if !cfg.AdaptiveCfg.Enabled {
		t.Error("AdaptiveCfg.Enabled = false, want true")
	}
	if cfg.AdaptiveCfg.InitialDelay.D != 7*24*time.Hour {
		t.Errorf("AdaptiveCfg.InitialDelay = %v, want 168h", cfg.AdaptiveCfg.InitialDelay.D)
	}
	if cfg.AdaptiveCfg.MaxDelay.D != 3*730*time.Hour {
		t.Errorf("AdaptiveCfg.MaxDelay = %v, want 2190h", cfg.AdaptiveCfg.MaxDelay.D)
	}
	if cfg.AdaptiveCfg.BackoffMultiplier != 2 {
		t.Errorf("AdaptiveCfg.BackoffMultiplier = %v, want 2", cfg.AdaptiveCfg.BackoffMultiplier)
	}
	if !cfg.SearchCfg.UpgradeEnabled {
		t.Error("SearchCfg.UpgradeEnabled = false, want true")
	}
	if cfg.SearchCfg.UpgradeWindowDays != 7 {
		t.Errorf("SearchCfg.UpgradeWindowDays = %d, want 7", cfg.SearchCfg.UpgradeWindowDays)
	}
	if cfg.SearchCfg.ProviderTimeout.D != time.Hour {
		t.Errorf("SearchCfg.ProviderTimeout = %v, want 1h", cfg.SearchCfg.ProviderTimeout.D)
	}
	if cfg.SearchCfg.ScanDelay.D != 5*time.Second {
		t.Errorf("SearchCfg.ScanDelay = %v, want 5s", cfg.SearchCfg.ScanDelay.D)
	}
	if cfg.SearchCfg.MaxSSEClients != 32 {
		t.Errorf("SearchCfg.MaxSSEClients = %d, want 32", cfg.SearchCfg.MaxSSEClients)
	}
	if len(cfg.SearchCfg.ExcludeArrTags) != 1 || cfg.SearchCfg.ExcludeArrTags[0] != "no-subflux" {
		t.Errorf("SearchCfg.ExcludeArrTags = %v, want [no-subflux]", cfg.SearchCfg.ExcludeArrTags)
	}
	if cfg.PollIntervalCfg.D != 30*time.Second {
		t.Errorf("PollIntervalCfg = %v, want 30s", cfg.PollIntervalCfg.D)
	}
}

func TestLoadFromBytes_invalid_yaml(t *testing.T) {
	t.Parallel()
	_, err := LoadFromBytes(context.Background(), []byte("{{invalid yaml"))
	if err == nil {
		t.Fatal("LoadFromBytes(invalid yaml) expected error")
	}
}

func TestLoadFromBytes_validation_failure(t *testing.T) {
	t.Parallel()
	// Valid YAML but fails validation (no arr configured).
	data := `
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected validation error")
	}
}

func TestLoadFromBytes_empty_input(t *testing.T) {
	t.Parallel()
	_, err := LoadFromBytes(context.Background(), []byte(""))
	if err == nil {
		t.Fatal("LoadFromBytes(context.Background(), empty) expected error")
	}
}

// Kills CONDITIONALS_BOUNDARY: data exactly at maxConfigSize must be accepted
// by the size check (> not >=), then fail on parse.
func TestLoadFromBytes_data_exactly_at_max_size(t *testing.T) {
	t.Parallel()
	data := make([]byte, maxConfigSize)
	for i := range data {
		data[i] = 'x'
	}
	_, err := LoadFromBytes(context.Background(), data)
	if err == nil {
		t.Fatal("LoadFromBytes(context.Background(), maxConfigSize) expected error (invalid YAML)")
	}
	// Must be a parse error, not a "too large" error.
	if errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("LoadFromBytes(context.Background(), maxConfigSize) = %q, want parse error not size error", err)
	}
}

func TestLoadFromBytes_data_one_over_max_size(t *testing.T) {
	t.Parallel()
	data := make([]byte, maxConfigSize+1)
	_, err := LoadFromBytes(context.Background(), data)
	if err == nil {
		t.Fatal("LoadFromBytes(context.Background(), maxConfigSize+1) expected error")
	}
	if !errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("LoadFromBytes(context.Background(), maxConfigSize+1) = %q, want 'too large' error", err)
	}
}

func TestLoadFromBytes_env_expansion(t *testing.T) {
	// Not parallel: t.Setenv modifies process environment.
	t.Setenv("SUBFLUX_TEST_URL", "http://expanded:8989")
	data := `
sonarr:
  url: "${SUBFLUX_TEST_URL}"
  api_key: "key"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.Sonarr.URL != "http://expanded:8989" {
		t.Errorf("Sonarr.URL = %q, want %q", cfg.Sonarr.URL, "http://expanded:8989")
	}
}

// TestLoadFromBytes_env_expansion_is_structure_safe pins the post-parse
// expansion contract (yamlenv): an environment value full of YAML syntax lands
// as an inert string value and can neither inject keys nor truncate the
// document — the weakness the former pre-parse os.Expand had.
func TestLoadFromBytes_env_expansion_is_structure_safe(t *testing.T) {
	// Not parallel: t.Setenv modifies process environment.
	evil := "k\"\nproviders:\n  os:\n    enabled: false\n# comment"
	t.Setenv("SUBFLUX_TEST_EVIL", evil)
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "${SUBFLUX_TEST_EVIL}"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.Sonarr.APIKey != evil {
		t.Errorf("Sonarr.APIKey = %q, want the raw environment value %q", cfg.Sonarr.APIKey, evil)
	}
	if p := cfg.Providers["os"]; !p.Enabled {
		t.Error("providers.os.enabled flipped to false: the expanded value rewrote document structure")
	}
}

func TestLoadFromBytes_env_expansion_unset_preserved(t *testing.T) {
	t.Parallel()
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "${SUBFLUX_TEST_UNSET_VAR_12345}"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	// Unset env vars are preserved as literal "${VAR}".
	if cfg.Sonarr.APIKey != "${SUBFLUX_TEST_UNSET_VAR_12345}" {
		t.Errorf("Sonarr.APIKey = %q, want literal ${SUBFLUX_TEST_UNSET_VAR_12345}", cfg.Sonarr.APIKey)
	}
}

// --- Load (file-based) ---

func TestLoad_reads_file(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, minimalValidYAML())

	cfg, err := Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load(%q) unexpected error: %v", path, err)
	}
	if cfg.Sonarr.URL != "http://sonarr:8989" {
		t.Errorf("Load().Sonarr.URL = %q, want %q", cfg.Sonarr.URL, "http://sonarr:8989")
	}
}

func TestLoad_applies_defaults(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, minimalValidYAML())

	cfg, err := Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load(%q) unexpected error: %v", path, err)
	}

	// Spot-check defaults survive the file-reading path.
	// Full default coverage is in TestLoadFromBytes_defaults_applied.
	if !cfg.AdaptiveCfg.Enabled {
		t.Error("Load().AdaptiveCfg.Enabled = false, want true")
	}
}

func TestLoad_missing_file(t *testing.T) {
	t.Parallel()
	_, err := Load(context.Background(), "/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("Load(context.Background(), missing) expected error")
	}
}

func TestLoad_invalid_yaml_file(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "{{invalid")

	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load(context.Background(), invalid yaml) expected error")
	}
}

func TestLoad_validation_failure_file(t *testing.T) {
	t.Parallel()
	// Valid YAML but no arr configured.
	data := `
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	path := writeConfig(t, data)

	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load(context.Background(), invalid config) expected error")
	}
}

func TestLoad_file_too_large(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.yaml")
	data := make([]byte, maxConfigSize+1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load(context.Background(), too large) expected error")
	}
}

// Kills CONDITIONALS_BOUNDARY: file exactly at maxConfigSize must be accepted
// by the size check (> not >=), then fail on parse.
func TestLoad_file_exactly_at_max_size(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := make([]byte, maxConfigSize)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load(context.Background(), maxConfigSize) expected error (invalid YAML)")
	}
	// The error must be a parse error, NOT a "too large" error.
	if errors.Is(err, ErrConfigTooLarge) {
		t.Errorf("Load(context.Background(), maxConfigSize) = %q, want parse error not size error", err)
	}
}

func TestLoad_unreadable_file(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based test unreliable on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod() unexpected error: %v", err)
	}
	_, err := Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load(context.Background(), unreadable) expected error")
	}
}

// --- isAllowedEnvVar ---

func TestExpandEnvSafe_blocks_disallowed_vars(t *testing.T) {
	// Not parallel: t.Setenv modifies process environment.
	t.Setenv("HOME", "/evil")
	t.Setenv("PATH", "/evil/bin")
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "${HOME}"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	// HOME is not in the allowed list, so it should be preserved as literal.
	if cfg.Sonarr.APIKey != "${HOME}" {
		t.Errorf("Sonarr.APIKey = %q, want literal ${HOME} (blocked)", cfg.Sonarr.APIKey)
	}
}

func TestExpandEnvSafe_allows_known_safe_vars(t *testing.T) {
	// Not parallel: t.Setenv modifies process environment.
	t.Setenv("CONFIG_ROOT", "/config")
	data := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  os:
    enabled: true
media_roots:
  - "${CONFIG_ROOT}/media"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if len(cfg.MediaRootDirs) != 1 || cfg.MediaRootDirs[0] != "/config/media" {
		t.Errorf("MediaRootDirs = %v, want [/config/media]", cfg.MediaRootDirs)
	}
}

func TestIsAllowedEnvVar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want bool
	}{
		{"SUBFLUX_URL", true},
		{"SUBFLUX_", true},
		{"SUBFLUX_ANYTHING", true},
		{"CONFIG_ROOT", true},
		{"MEDIA_FOLDER", true},
		{"PUID", true},
		{"PGID", true},
		{"TZ", true},
		{"LAN_IP", true},
		{"HOSTNAME", true},
		{"HOME", false},
		{"PATH", false},
		{"USER", false},
		{"AWS_SECRET_KEY", false},
		{"", false},
		{"SUBFLUX", false}, // no trailing underscore
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			got := isAllowedEnvVar(tt.key)
			if got != tt.want {
				t.Errorf("isAllowedEnvVar(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

// --- Load logging ---

func TestLoad_logs_arr_flags(t *testing.T) {
	// minimalValidYAML configures sonarr (URL set) but not radarr, so the
	// "config loaded" line logs sonarr=true and radarr=false.
	path := writeConfig(t, minimalValidYAML())
	out := captureLogs(t, func() {
		cfg, err := Load(context.Background(), path)
		if err != nil {
			t.Fatalf("Load(minimalValidYAML) = %v, want nil", err)
		}
		_ = cfg.Close()
	})

	if !strings.Contains(out, "sonarr=true") {
		t.Errorf("Load log = %q, want it to contain sonarr=true (SonarrConfig().URL != \"\")", out)
	}
	if !strings.Contains(out, "radarr=false") {
		t.Errorf("Load log = %q, want it to contain radarr=false (RadarrConfig().URL == \"\")", out)
	}
}

// --- buildCaches ---

func TestBuildCaches_opens_valid_media_root(t *testing.T) {
	cfg := &Config{MediaRootDirs: []string{t.TempDir()}}
	cfg.buildCaches(context.Background())
	defer func() { _ = cfg.Close() }()

	// A live context plus one accessible root yields exactly one cached handle.
	if len(cfg.cachedRoots) != 1 {
		t.Fatalf("buildCaches(live ctx, 1 valid root): len(cachedRoots) = %d, want 1", len(cfg.cachedRoots))
	}
}
