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

	"github.com/cplieger/envx/yamlenv"
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

// --- Decode-error sanitization (yamlenv.SanitizeDecodeError) ---

// TestLoadFromBytes_decode_error_redacts_expanded_secret pins the leak fix:
// a ${VAR} secret expanded into a scalar that then fails the typed decode
// must NOT survive into the returned error, which reaches the startup log
// and the PUT /api/config response body verbatim. Pre-fix, the yaml.v3
// TypeError entry embedded the expanded value as a backtick-quoted excerpt.
func TestLoadFromBytes_decode_error_redacts_expanded_secret(t *testing.T) {
	// t.Setenv: cannot be parallel.
	const secret = "hunter2-expanded-secret-value"
	t.Setenv("SUBFLUX_TEST_SECRET", secret)
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
  opensubtitles:
    enabled: true
    priority: ${SUBFLUX_TEST_SECRET}
    settings:
      api_key: "test"
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected decode error for non-numeric priority")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("LoadFromBytes() error leaks the expanded secret: %q", err)
	}
	if !strings.Contains(err.Error(), "parse YAML") {
		t.Errorf("LoadFromBytes() error = %q, want the parse YAML prefix kept", err)
	}
}

// TestLoadFromBytes_syntax_error_withholds_document_text pins the same
// redact-everything stance on the pre-expansion parse: a syntax error in a
// document holding a pasted literal secret keeps only value-independent
// structure (the line locator), never document text.
func TestLoadFromBytes_syntax_error_withholds_document_text(t *testing.T) {
	t.Parallel()
	data := "sonarr:\n  api_key: \"pasted-literal-secret\"\n\t\tbad: tab-indent\n"
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected syntax error for tab indentation")
	}
	if strings.Contains(err.Error(), "pasted-literal-secret") {
		t.Errorf("LoadFromBytes() error leaks document text: %q", err)
	}
}

// --- Strict pre-decode checks (yamlenv.CheckSingleDocument / checkUnknownKeys) ---

// TestLoadFromBytes_unknown_key_rejected pins the fail-loud unknown-key
// contract: a misspelled top-level key errors instead of being silently
// ignored while the intended setting stays at its default. The error keeps
// the redact-everything stance of the file's sanitize path: the key name is
// withheld (a misindented paste can put a secret in key position), only the
// structural unknown-key vocabulary and locator survive.
func TestLoadFromBytes_unknown_key_rejected(t *testing.T) {
	t.Parallel()
	data := minimalValidYAML() + "poll_intervall: 30s\n"
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes(misspelled key) expected error")
	}
	if !strings.Contains(err.Error(), "unknown configuration key") {
		t.Errorf("LoadFromBytes() error = %q, want the unknown-key vocabulary", err)
	}
	if strings.Contains(err.Error(), "poll_intervall") {
		t.Errorf("LoadFromBytes() error = %q, must withhold the key name (sanitize path)", err)
	}
}

// TestLoadFromBytes_env_var_in_duration_field_passes_probe pins the probe
// filter's abort branch: the strict unknown-key probe runs on the RAW
// pre-expansion bytes, where a literal ${VAR} in a Duration field makes
// Duration.UnmarshalYAML abort the probe decode with its own non-TypeError
// error. That is a pre-expansion artifact — the value is legal once
// expanded (Duration decodes the scalar as a string, so the kept !!str tag
// is no obstacle) — and must not reject the load.
func TestLoadFromBytes_env_var_in_duration_field_passes_probe(t *testing.T) {
	// Not parallel: t.Setenv modifies process environment.
	t.Setenv("SUBFLUX_TEST_SCAN_DELAY", "6s")
	data := minimalValidYAML() + `search:
  scan_delay: ${SUBFLUX_TEST_SCAN_DELAY}
`
	cfg, err := LoadFromBytes(context.Background(), []byte(data))
	if err != nil {
		t.Fatalf("LoadFromBytes(${VAR} in Duration field) unexpected error: %v", err)
	}
	if cfg.SearchCfg.ScanDelay.D != 6*time.Second {
		t.Errorf("ScanDelay = %v, want 6s (expanded from ${VAR})", cfg.SearchCfg.ScanDelay.D)
	}
}

// TestLoadFromBytes_unknown_key_detected_beside_var_int pins the probe
// filter's entry discrimination: a ${VAR} in an int field raises a
// wrong-type entry in the same probe TypeError as a genuine unknown-key
// finding. The filter must drop the wrong-type noise (the post-expansion
// decode owns value diagnostics) and still report the unknown key.
func TestLoadFromBytes_unknown_key_detected_beside_var_int(t *testing.T) {
	t.Parallel()
	data := minimalValidYAML() + `search:
  min_score: ${SUBFLUX_TEST_UNSET_MIN_SCORE}
  scan_intervall: 24h
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes(unknown key beside ${VAR} int) expected error")
	}
	if !strings.Contains(err.Error(), "unknown configuration key") {
		t.Errorf("LoadFromBytes() error = %q, want the unknown-key finding kept", err)
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("LoadFromBytes() error = %q, want the wrong-type probe entry dropped", err)
	}
}

// TestLoadFromBytes_multi_document_rejected pins the document-multiplicity
// guard: the single-document decode pipeline reads only the first document,
// so everything under a stray "---" separator would be silently ignored —
// rejected loudly instead, with the static (content-free) sentinel.
func TestLoadFromBytes_multi_document_rejected(t *testing.T) {
	t.Parallel()
	data := minimalValidYAML() + "---\nproviders:\n  opensubtitles:\n    enabled: false\n"
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes(multi-document) expected error")
	}
	if !errors.Is(err, yamlenv.ErrMultipleDocuments) {
		t.Errorf("LoadFromBytes() error = %q, want yamlenv.ErrMultipleDocuments", err)
	}
}

// TestLoadFromBytes_duration_error_vocabulary_passes_through pins the
// sanitizer gate: errors from this package's own UnmarshalYAML
// implementations are app-owned vocabulary and keep reaching the operator
// (only yaml.v3's own excerpt-bearing errors are rebuilt) — but the
// offending value itself is withheld at the source: after yamlenv.Expand
// it may be an expanded ${VAR} secret, and this error reaches the startup
// log and the PUT /api/config response body
// (see TestDuration_YAML_unmarshal_invalid_duration_string).
func TestLoadFromBytes_duration_error_vocabulary_passes_through(t *testing.T) {
	t.Parallel()
	data := minimalValidYAML() + `search:
  provider_timeout: "not_a_duration"
`
	_, err := LoadFromBytes(context.Background(), []byte(data))
	if err == nil {
		t.Fatal("LoadFromBytes() expected error for invalid duration")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("LoadFromBytes() error = %q, want app-owned 'invalid duration' kept", err)
	}
	if strings.Contains(err.Error(), "not_a_duration") {
		t.Errorf("LoadFromBytes() error = %q, must withhold the offending value (may be an expanded secret)", err)
	}
}
