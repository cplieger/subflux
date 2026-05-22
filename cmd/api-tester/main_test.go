package main

import (
	"os"
	"testing"

	"subflux/internal/api"
)

func TestValidateResults_no_warnings_for_valid_results(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "en", ReleaseName: "Movie.2024.WEB-DL", DownloadURL: "https://example.com/dl/1"},
		{Language: "fr", ID: "sub-123", DownloadURL: "https://example.com/dl/2"},
	}

	warnings := validateResults(results, false)

	if len(warnings) != 0 {
		t.Errorf("validateResults(valid, false) = %v, want empty", warnings)
	}
}

func TestValidateResults_detects_empty_language(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "", ReleaseName: "Movie.2024", DownloadURL: "https://example.com/dl"},
	}

	warnings := validateResults(results, false)

	if len(warnings) != 1 {
		t.Fatalf("validateResults(empty lang) len = %d, want 1", len(warnings))
	}
	if warnings[0] != "[0] empty language" {
		t.Errorf("validateResults(empty lang)[0] = %q, want %q",
			warnings[0], "[0] empty language")
	}
}

func TestValidateResults_detects_no_release_or_id(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "en", ReleaseName: "", ID: "", DownloadURL: "https://example.com/dl"},
	}

	warnings := validateResults(results, false)

	if len(warnings) != 1 {
		t.Fatalf("validateResults(no release/id) len = %d, want 1", len(warnings))
	}
	if warnings[0] != "[0] no release or ID" {
		t.Errorf("validateResults(no release/id)[0] = %q, want %q",
			warnings[0], "[0] no release or ID")
	}
}

func TestValidateResults_detects_no_download_url(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "en", ReleaseName: "Movie.2024", DownloadURL: ""},
	}

	warnings := validateResults(results, false)

	if len(warnings) != 1 {
		t.Fatalf("validateResults(no download URL) len = %d, want 1", len(warnings))
	}
	if warnings[0] != "[0] no download URL" {
		t.Errorf("validateResults(no download URL)[0] = %q, want %q",
			warnings[0], "[0] no download URL")
	}
}

func TestValidateResults_skip_download_url_check(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "en", ReleaseName: "Movie.2024", DownloadURL: ""},
	}

	warnings := validateResults(results, true)

	if len(warnings) != 0 {
		t.Errorf("validateResults(skipDLURL=true) = %v, want empty", warnings)
	}
}

func TestValidateResults_checks_at_most_five_results(t *testing.T) {
	t.Parallel()

	// 7 results, all with empty language. Only first 5 should be checked.
	results := make([]api.Subtitle, 7)
	for i := range results {
		results[i] = api.Subtitle{ReleaseName: "sub", DownloadURL: "https://example.com"}
	}

	warnings := validateResults(results, false)

	if len(warnings) != 5 {
		t.Errorf("validateResults(7 bad results) len = %d, want 5", len(warnings))
	}
}

func TestValidateResults_empty_results(t *testing.T) {
	t.Parallel()

	warnings := validateResults(nil, false)

	if len(warnings) != 0 {
		t.Errorf("validateResults(nil) = %v, want empty", warnings)
	}
}

func TestValidateResults_multiple_issues_on_same_result(t *testing.T) {
	t.Parallel()

	results := []api.Subtitle{
		{Language: "", ReleaseName: "", ID: "", DownloadURL: ""},
	}

	warnings := validateResults(results, false)

	if len(warnings) != 3 {
		t.Errorf("validateResults(all bad) len = %d, want 3; got %v",
			len(warnings), warnings)
	}
}

func TestLoadSettings_env_vars_override_empty_config(t *testing.T) {
	t.Setenv("OPENSUBTITLES_API_KEY", "test-os-key")
	t.Setenv("SUBDL_API_KEY", "test-subdl-key")
	t.Setenv("BETASERIES_TOKEN", "test-beta-token")

	settings := loadSettings("")

	if got := settings["opensubtitles"]["api_key"]; got != "test-os-key" {
		t.Errorf("loadSettings(\"\")[opensubtitles][api_key] = %v, want %q", got, "test-os-key")
	}
	if got := settings["subdl"]["api_key"]; got != "test-subdl-key" {
		t.Errorf("loadSettings(\"\")[subdl][api_key] = %v, want %q", got, "test-subdl-key")
	}
	if got := settings["betaseries"]["token"]; got != "test-beta-token" {
		t.Errorf("loadSettings(\"\")[betaseries][token] = %v, want %q", got, "test-beta-token")
	}
}

func TestLoadSettings_no_env_vars_returns_empty_maps(t *testing.T) {
	// Clear all provider env vars to ensure clean state.
	for _, key := range []string{
		"OPENSUBTITLES_API_KEY", "OPENSUBTITLES_USERNAME", "OPENSUBTITLES_PASSWORD",
		"SUBSOURCE_API_KEY", "SUBDL_API_KEY", "BETASERIES_TOKEN",
		"HDBITS_USERNAME", "HDBITS_PASSKEY", "ANIDB_CLIENT_KEY",
	} {
		t.Setenv(key, "")
	}

	settings := loadSettings("")

	for name, s := range settings {
		for k, v := range s {
			if v != "" {
				t.Errorf("loadSettings(\"\")[%s][%s] = %v, want empty", name, k, v)
			}
		}
	}
}

func TestLoadSettings_multiple_env_vars_for_same_provider(t *testing.T) {
	t.Setenv("HDBITS_USERNAME", "testuser")
	t.Setenv("HDBITS_PASSKEY", "testpass")

	settings := loadSettings("")

	if got := settings["hdbits"]["username"]; got != "testuser" {
		t.Errorf("loadSettings(\"\")[hdbits][username] = %v, want %q", got, "testuser")
	}
	if got := settings["hdbits"]["passkey"]; got != "testpass" {
		t.Errorf("loadSettings(\"\")[hdbits][passkey] = %v, want %q", got, "testpass")
	}
}

func TestLoadSettings_nonexistent_config_file_uses_env_only(t *testing.T) {
	t.Setenv("SUBDL_API_KEY", "env-key")

	settings := loadSettings("/nonexistent/path/config.yaml")

	if got := settings["subdl"]["api_key"]; got != "env-key" {
		t.Errorf("loadSettings(nonexistent)[subdl][api_key] = %v, want %q", got, "env-key")
	}
}

func TestLoadConfigFile_valid_yaml(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `providers:
  gestdown:
    enabled: true
    settings:
      custom_key: custom_value
  subdl:
    enabled: true
    settings:
      api_key: yaml-key
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(map[string]map[string]any)
	loadConfigFile(path, result)

	if got, ok := result["gestdown"]["custom_key"]; !ok || got != "custom_value" {
		t.Errorf("loadConfigFile() gestdown[custom_key] = %v, want %q", got, "custom_value")
	}
	if got, ok := result["subdl"]["api_key"]; !ok || got != "yaml-key" {
		t.Errorf("loadConfigFile() subdl[api_key] = %v, want %q", got, "yaml-key")
	}
}

func TestLoadConfigFile_invalid_yaml(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.yaml"
	if err := os.WriteFile(path, []byte("not: [valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(map[string]map[string]any)
	loadConfigFile(path, result)

	// Invalid YAML should not populate result.
	if len(result) != 0 {
		t.Errorf("loadConfigFile(invalid yaml) populated %d entries, want 0", len(result))
	}
}

func TestLoadConfigFile_empty_file(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.yaml"
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(map[string]map[string]any)
	loadConfigFile(path, result)

	if len(result) != 0 {
		t.Errorf("loadConfigFile(empty) populated %d entries, want 0", len(result))
	}
}

func TestLoadConfigFile_file_too_large(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/huge.yaml"
	// maxConfigSize is 1<<20 (1MB). Create a file just over that.
	data := make([]byte, maxConfigSize+1)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(map[string]map[string]any)
	loadConfigFile(path, result)

	if len(result) != 0 {
		t.Errorf("loadConfigFile(too large) populated %d entries, want 0", len(result))
	}
}

func TestLoadConfigFile_providers_without_settings_ignored(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `providers:
  gestdown:
    enabled: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(map[string]map[string]any)
	loadConfigFile(path, result)

	// gestdown has no settings block, so it should not appear in result.
	if _, ok := result["gestdown"]; ok {
		t.Errorf("loadConfigFile() should not populate provider without settings")
	}
}

func TestHasCredentials_table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pd       providerDef
		settings map[string]any
		want     bool
	}{
		{
			name:     "needsKey false with nil settings",
			pd:       providerDef{needsKey: false},
			settings: nil,
			want:     true,
		},
		{
			name:     "needsKey false with empty settings",
			pd:       providerDef{needsKey: false},
			settings: map[string]any{},
			want:     true,
		},
		{
			name:     "needsKey true with nil settings",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: nil,
			want:     false,
		},
		{
			name:     "needsKey true with empty settings map",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: map[string]any{},
			want:     false,
		},
		{
			name:     "needsKey true with nil key value",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: map[string]any{"api_key": nil},
			want:     false,
		},
		{
			name:     "needsKey true with empty string key value",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: map[string]any{"api_key": ""},
			want:     false,
		},
		{
			name:     "needsKey true with valid key value",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: map[string]any{"api_key": "my-secret-key"},
			want:     true,
		},
		{
			name:     "needsKey true with wrong key field name",
			pd:       providerDef{needsKey: true, keyField: "token"},
			settings: map[string]any{"api_key": "my-secret-key"},
			want:     false,
		},
		{
			name:     "needsKey true with non-string value",
			pd:       providerDef{needsKey: true, keyField: "api_key"},
			settings: map[string]any{"api_key": 12345},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasCredentials(&tt.pd, tt.settings)
			if got != tt.want {
				t.Errorf("hasCredentials(%+v, %v) = %v, want %v",
					tt.pd, tt.settings, got, tt.want)
			}
		})
	}
}

func TestLoadSettings_env_vars_override_config_file(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `providers:
  subdl:
    enabled: true
    settings:
      api_key: yaml-key
  gestdown:
    enabled: true
    settings:
      custom_key: from-yaml
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Env var should override the YAML value for subdl api_key.
	t.Setenv("SUBDL_API_KEY", "env-override-key")

	settings := loadSettings(path)

	if got := settings["subdl"]["api_key"]; got != "env-override-key" {
		t.Errorf("loadSettings(config+env)[subdl][api_key] = %v, want %q",
			got, "env-override-key")
	}
	// gestdown should retain its YAML value (no env override).
	if got := settings["gestdown"]["custom_key"]; got != "from-yaml" {
		t.Errorf("loadSettings(config+env)[gestdown][custom_key] = %v, want %q",
			got, "from-yaml")
	}
}
