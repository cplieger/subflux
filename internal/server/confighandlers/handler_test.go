package confighandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/provider"
)

// TestHandleSaveConfig_response_redacts_expanded_secret pins the HTTP
// surface of the config decode-error sanitization: HandleSaveConfig echoes
// the loader's error into the 400 body ("invalid configuration: ..."), so a
// ${VAR} secret expanded into a scalar that fails the typed decode must
// arrive redacted. Wires the real config.LoadFromBytes exactly as the
// composition root does (main.go newConfigLoader).
func TestHandleSaveConfig_response_redacts_expanded_secret(t *testing.T) {
	// t.Setenv: cannot be parallel.
	const secret = "hunter2-expanded-secret-value"
	t.Setenv("SUBFLUX_TEST_SECRET", secret)

	h := New(&Deps{
		LoadConfig: func(data []byte) (api.ConfigProvider, error) {
			return config.LoadFromBytes(context.Background(), data)
		},
		// Nonexistent path: a true empty baseline, MergeSecrets leaves the body as-is.
		ConfigPath: func() string { return filepath.Join(t.TempDir(), "config.yaml") },
	})

	body := `
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
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleSaveConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HandleSaveConfig() status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Body.String(); strings.Contains(got, secret) {
		t.Errorf("HandleSaveConfig() response leaks the expanded secret: %q", got)
	}
}

// --- Raw-YAML config surface (GET/PUT /api/config) ---
//
// Migrated from the root server package's delegate-era tests
// (server_config_test.go, pure_config_test.go): the Handler is constructed
// directly with a per-test ConfigPath closure instead of the old package-
// level cfgFilePath variable, so these run in parallel.

// writeTestFile is a helper to write content to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// newPathHandler builds a Handler that reads/writes the given config path
// and validates bodies with the real config loader (as main.go wires it).
func newPathHandler(configPath string) *Handler {
	return New(&Deps{
		LoadConfig: func(data []byte) (api.ConfigProvider, error) {
			return config.LoadFromBytes(context.Background(), data)
		},
		ConfigPath: func() string { return configPath },
	})
}

func TestHandleGetConfig_reads_file(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := "search:\n  scan_interval: 24h\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	h := newPathHandler(configPath)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/yaml" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/yaml")
	}
	if body := rec.Body.String(); body != content {
		t.Errorf("HandleGetConfig() body = %q, want %q", body, content)
	}
}

func TestHandleGetConfig_missing_file_returns_500(t *testing.T) {
	t.Parallel()
	h := newPathHandler("/nonexistent/config.yaml")

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleGetConfig(missing file) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetConfig_oversized_file_returns_500(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Create a file larger than 1 MB.
	bigContent := strings.Repeat("x", 1<<20+1)
	if err := writeTestFile(configPath, bigContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	h := newPathHandler(configPath)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleGetConfig(oversized) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// Kills CONDITIONALS_BOUNDARY on the maxBodySize read bound in
// HandleGetConfig (size > max vs >= max). A file exactly at 1MB must be
// accepted (200), not rejected as "too large" (500).
func TestHandleGetConfig_file_exactly_at_max_size(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Create a file exactly 1 MB (1 << 20 bytes).
	exactContent := strings.Repeat("x", 1<<20)
	if err := writeTestFile(configPath, exactContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	h := newPathHandler(configPath)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	// File at exactly 1MB should be accepted (200), not rejected (500).
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("HandleGetConfig(exactly 1MB) status = %d, want 200 (not rejected as too large)",
			rec.Code)
	}
}

func TestHandleGetConfig_directory_returns_500(t *testing.T) {
	t.Parallel()
	h := newPathHandler(t.TempDir()) // Point at a directory, not a file.

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleGetConfig(directory) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetConfig_redacts_secrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := "providers:\n  os:\n    api_key: my-secret-key\n    enabled: true\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	h := newPathHandler(configPath)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if strings.Contains(body, "my-secret-key") {
		t.Error("HandleGetConfig() response contains unredacted secret")
	}
	if !strings.Contains(body, "********") {
		t.Error("HandleGetConfig() response missing redaction placeholder")
	}
}

func TestHandleSaveConfig_invalid_yaml_returns_400(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := newPathHandler(filepath.Join(dir, "config.yaml"))

	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleSaveConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleSaveConfig(invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// Kills CONDITIONALS_NEGATION on the ReadAll error check in
// HandleSaveConfig (err != nil vs err == nil). A valid PUT body must not
// return 400 "failed to read body"; the mutant would enter the error branch
// on successful ReadAll.
func TestHandleSaveConfig_valid_body_not_rejected_as_read_error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Write initial config so the file exists.
	if err := writeTestFile(configPath, "initial: true"); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	h := newPathHandler(configPath)

	// Send valid YAML that will fail LoadFromBytes validation (no arr configured),
	// but the ReadAll step must succeed. The error should be 400 with a validation
	// message, NOT "failed to read body".
	body := `languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
providers:
  os:
    enabled: true
`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleSaveConfig(rec, req)

	// Should be 400 (validation error), not 400 with "failed to read body".
	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "failed to read body") {
		t.Error("HandleSaveConfig returned 'failed to read body' for valid body; ReadAll negation mutant detected")
	}
}

func TestHandleSaveConfig_post_method_accepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := newPathHandler(filepath.Join(dir, "config.yaml"))

	// POST with invalid YAML should return 400 (validation error),
	// not 405 (method not allowed). This verifies POST is accepted.
	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleSaveConfig(rec, req)

	if rec.Code == http.StatusMethodNotAllowed {
		t.Errorf("HandleSaveConfig(POST) status = %d, want POST to be accepted (not 405)",
			rec.Code)
	}
	// Should be 400 (invalid YAML), confirming POST was accepted.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleSaveConfig(POST, invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSaveConfig_oversized_body_returns_413(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := writeTestFile(configPath, "key: old\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	h := newPathHandler(configPath)

	// Body exactly 1 byte over maxBodySize (1 MB + 1).
	body := bytes.NewReader(make([]byte, (1<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	rec := httptest.NewRecorder()

	h.HandleSaveConfig(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("HandleSaveConfig(oversized body) status = %d, want %d",
			rec.Code, http.StatusRequestEntityTooLarge)
	}
}

// --- HandleResetConfig ---

func TestHandleResetConfig_rejects_when_configured(t *testing.T) {
	t.Parallel()
	h := New(&Deps{
		Configured: func() bool { return true },
		ConfigPath: func() string { return "" },
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleResetConfig(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("HandleResetConfig(configured) status = %d, want %d",
			rec.Code, http.StatusConflict)
	}
}

func TestHandleResetConfig_no_default_config(t *testing.T) {
	t.Parallel()
	// Unconfigured, but defaultConfig is nil.
	h := New(&Deps{
		Configured: func() bool { return false },
		ConfigPath: func() string { return "" },
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleResetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleResetConfig(no default) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleResetConfig_writes_default(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	defaultCfg := []byte("# default config\nlanguages: [en]\n")
	// Unconfigured mode with an embedded default config.
	h := New(&Deps{
		DefaultConfig: defaultCfg,
		Configured:    func() bool { return false },
		ConfigPath:    func() string { return configPath },
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleResetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleResetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the file was written.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after reset: %v", err)
	}
	if !bytes.Equal(data, defaultCfg) {
		t.Errorf("config content = %q, want %q", string(data), string(defaultCfg))
	}
}

// --- HandleConfigSchema ---

// schemaStubProvider implements api.Provider for schema registry setup.
type schemaStubProvider struct {
	name string
}

func (p *schemaStubProvider) Name() api.ProviderID { return api.ProviderID(p.name) }

func (p *schemaStubProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (p *schemaStubProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
}

func TestHandleConfigSchema_returns_json(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("gestdown", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &schemaStubProvider{name: "gestdown"}, nil
	})

	h := New(&Deps{
		SchemaFunc: schema.Schema,
		Registry:   reg,
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/schema", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleConfigSchema(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleConfigSchema() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var sections []api.SchemaSection
	if err := json.NewDecoder(rec.Body).Decode(&sections); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sections) == 0 {
		t.Error("HandleConfigSchema() returned 0 sections, want > 0")
	}
}

func TestHandleConfigSchema_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := New(&Deps{
		SchemaFunc: schema.Schema,
		Registry:   provider.NewRegistry(),
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/schema", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleConfigSchema(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleConfigSchema(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}
