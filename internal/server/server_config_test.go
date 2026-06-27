package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleConfigParsed_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/parsed", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigParsed(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleConfigParsed(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetConfig_reads_file(t *testing.T) {
	// Uses t.Setenv, cannot be parallel.
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	content := "search:\n  scan_interval: 24h\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/yaml" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/yaml")
	}
	if body := rec.Body.String(); body != content {
		t.Errorf("handleGetConfig() body = %q, want %q", body, content)
	}
}

func TestHandleGetConfig_missing_file_returns_500(t *testing.T) {
	cfgFilePath = "/nonexistent/config.yaml"

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(missing file) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetConfig_oversized_file_returns_500(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Create a file larger than 1 MB.
	bigContent := strings.Repeat("x", 1<<20+1)
	if err := writeTestFile(configPath, bigContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(oversized) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleSaveConfig_invalid_yaml_returns_400(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleSaveConfig(invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// writeTestFile is a helper to write content to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// --- Mutant-killing boundary tests ---

// Kills CONDITIONALS_BOUNDARY at config_handlers.go:101 (fi.Size() > maxConfigSize → >= maxConfigSize).
// A file exactly at 1MB must be accepted (200), not rejected as "too large" (500).
func TestHandleGetConfig_file_exactly_at_max_size(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Create a file exactly 1 MB (1 << 20 bytes).
	exactContent := strings.Repeat("x", 1<<20)
	if err := writeTestFile(configPath, exactContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	// File at exactly 1MB should be accepted (200), not rejected (500).
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("handleGetConfig(exactly 1MB) status = %d, want 200 (not rejected as too large)",
			rec.Code)
	}
}

// Kills CONDITIONALS_NEGATION at config_handlers.go:137 (err != nil → err == nil on ReadAll).
// A valid PUT to /api/config must not return 400 "failed to read body".
// The mutant would enter the error branch on successful ReadAll.
func TestHandleSaveConfig_valid_body_not_rejected_as_read_error(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Write initial config so the file exists.
	if err := writeTestFile(configPath, "initial: true"); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

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
	s.handleSaveConfig(rec, req)

	// Should be 400 (validation error), not 400 with "failed to read body".
	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "failed to read body") {
		t.Error("handleSaveConfig returned 'failed to read body' for valid body; ReadAll negation mutant detected")
	}
}

func TestHandleGetConfig_directory_returns_500(t *testing.T) {
	// Uses t.Setenv, cannot be parallel.
	dir := t.TempDir()
	cfgFilePath = dir // Point at a directory, not a file.

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(directory) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// --- handleSaveConfig POST method ---

func TestHandleSaveConfig_post_method_accepted(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// POST with invalid YAML should return 400 (validation error),
	// not 405 (method not allowed). This verifies POST is accepted.
	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveConfig(rec, req)

	if rec.Code == http.StatusMethodNotAllowed {
		t.Errorf("handleSaveConfig(POST) status = %d, want POST to be accepted (not 405)",
			rec.Code)
	}
	// Should be 400 (invalid YAML), confirming POST was accepted.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleSaveConfig(POST, invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleGetConfig redacts secrets ---

func TestHandleGetConfig_redacts_secrets(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	content := "providers:\n  os:\n    api_key: my-secret-key\n    enabled: true\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if strings.Contains(body, "my-secret-key") {
		t.Error("handleGetConfig() response contains unredacted secret")
	}
	if !strings.Contains(body, "********") {
		t.Error("handleGetConfig() response missing redaction placeholder")
	}
}

func TestHandleSaveConfig_oversized_body_returns_413(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("key: old\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := cfgFilePath
	cfgFilePath = configPath
	t.Cleanup(func() { cfgFilePath = old })

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Body exactly 1 byte over maxConfigSize (1 MB + 1).
	body := bytes.NewReader(make([]byte, (1<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	rec := httptest.NewRecorder()

	s.handleSaveConfig(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("handleSaveConfig(oversized body) status = %d, want %d",
			rec.Code, http.StatusRequestEntityTooLarge)
	}
}
