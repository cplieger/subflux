package confighandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
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
		LoadConfig: func(data []byte) (*config.Config, error) {
			return config.LoadFromBytes(t.Context(), data)
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
	req := httptest.NewRequestWithContext(t.Context(),
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
		LoadConfig: func(data []byte) (*config.Config, error) {
			// context.Background(): no *testing.T in scope in this helper.
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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
	req := httptest.NewRequestWithContext(t.Context(),
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
	req := httptest.NewRequestWithContext(t.Context(),
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
	req := httptest.NewRequestWithContext(t.Context(),
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

// TestHandleSaveConfig_body_size_gate pins both sides of the request-body
// cap: a body exactly at the cap must reach validation, and one byte over
// must be refused before anything is parsed. The at-cap body is not valid
// config, so it can only be asserted to have got PAST the size gate.
func TestHandleSaveConfig_body_size_gate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		size         int
		wantTooLarge bool
		want         string
	}{
		{name: "exactly_at_cap", size: 1 << 20, wantTooLarge: false, want: "any status but 413"},
		{name: "one_byte_over_cap", size: (1 << 20) + 1, wantTooLarge: true, want: "413"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := writeTestFile(configPath, "key: old\n"); err != nil {
				t.Fatalf("write: %v", err)
			}
			h := newPathHandler(configPath)

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/api/config", bytes.NewReader(make([]byte, tt.size)))
			rec := httptest.NewRecorder()
			h.HandleSaveConfig(rec, req)

			if got := rec.Code == http.StatusRequestEntityTooLarge; got != tt.wantTooLarge {
				t.Errorf("HandleSaveConfig(%d-byte body) status = %d, want %s",
					tt.size, rec.Code, tt.want)
			}
		})
	}
}

// TestHandleGetConfig_successful_write_is_silent pins the guard on the
// response write: the diagnostic belongs to a FAILED write, so an ordinary
// GET must log nothing at all. Serial (swaps the default logger).
func TestHandleGetConfig_successful_write_is_silent(t *testing.T) {
	buf := captureSlog(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeTestFile(configPath, "search:\n  scan_interval: 24h\n"); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	h := newPathHandler(configPath)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetConfig() status = %d, want 200", rec.Code)
	}
	if got := buf.String(); strings.Contains(got, `msg="write response failed"`) {
		t.Errorf("HandleGetConfig() logged a write failure on a successful write: %s", got)
	}
}

// captureSlog redirects the default logger into a buffer for the duration of
// the test. A test using it must NOT call t.Parallel: the default logger is
// process-wide, so a parallel sibling's lines would land in this buffer.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// --- arr connectivity gate ---

// recordingPinger records every Ping and answers with a fixed error.
type recordingPinger struct {
	pings *int
	err   error
}

func (p recordingPinger) Ping(context.Context) error {
	*p.pings++
	return p.err
}

// arrPair is the live-config view the connectivity check compares against.
type arrPair struct{ sonarr, radarr subflux.ArrConfig }

func (a arrPair) Sonarr() subflux.ArrConfig { return a.sonarr }
func (a arrPair) Radarr() subflux.ArrConfig { return a.radarr }

// TestHandleSaveConfig_arr_connectivity_gate pins the whole connectivity
// gate: which instance is pinged, with which credentials, whether the save
// survives the answer, and the cases that must not ping at all (an arr with
// no URL, and one whose URL and key are unchanged from the live config). A
// save that pings the wrong instance would validate the operator's new
// sonarr credentials against radarr, so the recorded constructor arguments
// are asserted per role, not just counted.
func TestHandleSaveConfig_arr_connectivity_gate(t *testing.T) {
	t.Parallel()
	const validTail = "languages:\n  default:\n    - code: en\n"
	tests := []struct {
		name       string
		body       string
		live       arrPair
		pingErr    error
		newErr     error
		wantStatus int
		wantSonarr []string // constructor arguments, "url|apikey" per call
		wantRadarr []string
		wantPings  int
	}{
		{
			name:       "changed_sonarr_is_pinged_with_the_new_credentials",
			body:       "sonarr:\n  url: \"http://new:8989\"\n  api_key: \"k-new\"\n" + validTail,
			live:       arrPair{sonarr: subflux.ArrConfig{URL: "http://old:8989", APIKey: "k-old"}},
			wantStatus: http.StatusOK,
			wantSonarr: []string{"http://new:8989|k-new"},
			wantPings:  1,
		},
		{
			name:       "changed_radarr_is_pinged_with_the_new_credentials",
			body:       "radarr:\n  url: \"http://new:7878\"\n  api_key: \"r-new\"\n" + validTail,
			live:       arrPair{radarr: subflux.ArrConfig{URL: "http://old:7878", APIKey: "r-old"}},
			wantStatus: http.StatusOK,
			wantRadarr: []string{"http://new:7878|r-new"},
			wantPings:  1,
		},
		{
			name:       "unreachable_arr_rejects_the_save",
			body:       "sonarr:\n  url: \"http://new:8989\"\n  api_key: \"k-new\"\n" + validTail,
			live:       arrPair{sonarr: subflux.ArrConfig{URL: "http://old:8989", APIKey: "k-old"}},
			pingErr:    errors.New("connection refused"),
			wantStatus: http.StatusBadRequest,
			wantSonarr: []string{"http://new:8989|k-new"},
			wantPings:  1,
		},
		{
			name:       "unbuildable_arr_client_rejects_the_save",
			body:       "sonarr:\n  url: \"http://new:8989\"\n  api_key: \"k-new\"\n" + validTail,
			live:       arrPair{sonarr: subflux.ArrConfig{URL: "http://old:8989", APIKey: "k-old"}},
			newErr:     errors.New("bad base url"),
			wantStatus: http.StatusBadRequest,
			wantSonarr: []string{"http://new:8989|k-new"},
			wantPings:  0,
		},
		{
			name:       "arr_without_a_url_is_not_pinged",
			body:       "radarr:\n  url: \"http://new:7878\"\n  api_key: \"r-new\"\n" + validTail,
			live:       arrPair{},
			pingErr:    errors.New("must not be reached"),
			wantStatus: http.StatusBadRequest, // the radarr ping fails; sonarr was skipped
			wantRadarr: []string{"http://new:7878|r-new"},
			wantPings:  1,
		},
		{
			name:       "unchanged_arr_is_not_pinged",
			body:       "sonarr:\n  url: \"http://same:8989\"\n  api_key: \"k-same\"\n" + validTail,
			live:       arrPair{sonarr: subflux.ArrConfig{URL: "http://same:8989", APIKey: "k-same"}},
			pingErr:    errors.New("must not be reached"),
			wantStatus: http.StatusOK,
			wantPings:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			pings := 0
			var gotSonarr, gotRadarr []string
			record := func(dst *[]string) func(string, string) (ArrPinger, error) {
				return func(baseURL, apiKey string) (ArrPinger, error) {
					*dst = append(*dst, baseURL+"|"+apiKey)
					if tt.newErr != nil {
						return nil, tt.newErr
					}
					return recordingPinger{pings: &pings, err: tt.pingErr}, nil
				}
			}
			h := New(&Deps{
				LoadConfig: func(data []byte) (*config.Config, error) {
					return config.LoadFromBytes(t.Context(), data)
				},
				NewSonarr:  record(&gotSonarr),
				NewRadarr:  record(&gotRadarr),
				HotReload:  func(context.Context, *config.Config) error { return nil },
				State:      func() StateView { return StateView{Cfg: tt.live} },
				ConfigPath: func() string { return configPath },
			})

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPut, "/api/config", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.HandleSaveConfig(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("HandleSaveConfig() status = %d, want %d; body %s",
					rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !slices.Equal(gotSonarr, tt.wantSonarr) {
				t.Errorf("sonarr client built with %v, want %v", gotSonarr, tt.wantSonarr)
			}
			if !slices.Equal(gotRadarr, tt.wantRadarr) {
				t.Errorf("radarr client built with %v, want %v", gotRadarr, tt.wantRadarr)
			}
			if pings != tt.wantPings {
				t.Errorf("Ping calls = %d, want %d", pings, tt.wantPings)
			}
		})
	}
}

// --- HandleResetConfig ---

func TestHandleResetConfig_rejects_when_configured(t *testing.T) {
	t.Parallel()
	h := New(&Deps{
		Configured: func() bool { return true },
		ConfigPath: func() string { return "" },
	})

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
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

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleResetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("HandleResetConfig() status = %d, want %d", rec.Code, http.StatusOK)
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

// schemaStubProvider implements provider.Provider for schema registry setup.
type schemaStubProvider struct {
	name string
}

func (p *schemaStubProvider) Name() subflux.ProviderID { return subflux.ProviderID(p.name) }

func (p *schemaStubProvider) Search(_ context.Context, _ *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (p *schemaStubProvider) Download(_ context.Context, _ *subflux.Subtitle) ([]byte, error) {
	return nil, nil
}

func TestHandleConfigSchema_returns_json(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("gestdown", func(_ context.Context, _ map[string]any) (provider.Provider, error) {
		return &schemaStubProvider{name: "gestdown"}, nil
	})

	h := New(&Deps{
		SchemaFunc: schema.Sections,
		Registry:   reg,
	})

	req := httptest.NewRequestWithContext(t.Context(),
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

	var sections []subflux.SchemaSection
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
		SchemaFunc: schema.Sections,
		Registry:   provider.NewRegistry(),
	})

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/config/schema", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleConfigSchema(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleConfigSchema(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

// TestHandleValidatePath_traversal_guard pins both directions of the
// validate-path traversal rule. The endpoint judges the path AS WRITTEN
// (pathinside.HasDotDot, before cleaning), because the value the settings UI
// keeps is the string the admin typed: validating only the cleaned form would
// report "<dir>/sub/../.." valid on the strength of a different path than the
// one that lands in the config. The rule is per COMPONENT, so a directory
// whose name merely begins with or contains two dots stays valid.
func TestHandleValidatePath_traversal_guard(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sep := string(filepath.Separator)
	dotsDir := filepath.Join(dir, "..extras")
	if err := os.MkdirAll(dotsDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	infixDotsDir := filepath.Join(dir, "season.1..2")
	if err := os.MkdirAll(infixDotsDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cases := []struct {
		name      string
		path      string
		wantValid bool
		wantErr   string
	}{
		// Accepted: real directories whose names contain two dots but no
		// ".." component. A substring test would refuse all three.
		{name: "plain directory", path: dir, wantValid: true},
		{name: "directory beginning with double dots", path: dotsDir, wantValid: true},
		{name: "double dots inside directory name", path: infixDotsDir, wantValid: true},
		// Refused: a ".." component, wherever it sits. The middle case is the
		// one a cleaning predicate would collapse and accept: it resolves
		// back to an existing directory, so only the as-written test refuses it.
		{
			name:    "traversal segment refused even when it resolves inside",
			path:    subDir + sep + ".." + sep + "sub",
			wantErr: "path must not contain a '..' segment",
		},
		{
			name:    "traversal escaping the tree refused",
			path:    dir + sep + ".." + sep + ".." + sep + "etc",
			wantErr: "path must not contain a '..' segment",
		},
		{
			name:    "trailing traversal segment refused",
			path:    dir + sep + "..",
			wantErr: "path must not contain a '..' segment",
		},
		// Pre-existing refusals, unchanged by the traversal rule.
		{name: "empty path refused", path: "   ", wantErr: "path is empty"},
		{name: "relative path refused", path: "media" + sep + "tv", wantErr: "path must be absolute"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]string{"path": tc.path})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/api/config/validate-path", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			HandleValidatePath(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("HandleValidatePath(%q) status = %d, want %d", tc.path, rec.Code, http.StatusOK)
			}
			var got PathValidationResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("Unmarshal(%q): %v", rec.Body.String(), err)
			}
			if got.Valid != tc.wantValid {
				t.Errorf("HandleValidatePath(%q) valid = %v (error %q), want %v",
					tc.path, got.Valid, got.Error, tc.wantValid)
			}
			if got.Error != tc.wantErr {
				t.Errorf("HandleValidatePath(%q) error = %q, want %q", tc.path, got.Error, tc.wantErr)
			}
		})
	}
}
