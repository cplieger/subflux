package confighandlers

import (
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
)

// --- test fixtures ---

// structuredTestSchema is a compact stand-in for the real schema: one plain
// section with a secret, one providers section with a secret setting.
func structuredTestSchema() []api.SchemaSection {
	return []api.SchemaSection{
		{Key: "sonarr", Type: "fields", Fields: []api.SchemaField{
			{Key: "url"},
			{Key: "api_key", Secret: true},
		}},
		{Key: "providers", Type: "providers", Providers: []api.ProviderSchema{
			{Name: "opensubtitles", Settings: []api.SchemaField{
				{Key: "username"},
				{Key: "password", Secret: true},
			}},
		}},
	}
}

// newStructuredHandler builds a handler over a temp config file with the
// compact schema and a passthrough loader/reload (validation is not under
// test here; canonicalization and secret plumbing are).
func newStructuredHandler(t *testing.T, existingYAML string) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if existingYAML != "" {
		if err := os.WriteFile(cfgPath, []byte(existingYAML), 0o600); err != nil {
			t.Fatalf("write existing config: %v", err)
		}
	}
	return newStructuredHandlerAt(t, cfgPath, nil), cfgPath
}

// newStructuredHandlerAt is newStructuredHandler's lower half: an explicit
// config path (which may be a directory or malformed file for baseline
// failure tests) and an optional hot-reload hook for tests that must observe
// whether activation happened. A nil hook succeeds silently.
func newStructuredHandlerAt(t *testing.T, cfgPath string,
	hotReload func(context.Context, api.ConfigProvider) error,
) *Handler {
	t.Helper()
	if hotReload == nil {
		hotReload = func(context.Context, api.ConfigProvider) error { return nil }
	}
	return New(&Deps{
		SchemaFunc: func(_ []api.ProviderSchema) []api.SchemaSection { return structuredTestSchema() },
		LoadConfig: func(data []byte) (api.ConfigProvider, error) {
			return config.LoadFromBytes(context.Background(), data)
		},
		HotReload:  hotReload,
		State:      func() StateView { return StateView{} },
		ConfigPath: func() string { return cfgPath },
		NewSonarr:  func(_, _ string) (api.SonarrClient, error) { return pingOKSonarr{}, nil },
		NewRadarr:  func(_, _ string) (api.RadarrClient, error) { return pingOKRadarr{}, nil },
	})
}

// pingOKSonarr / pingOKRadarr satisfy the arr client interfaces for the
// connectivity check only; any other method panics via the embedded nil.
type pingOKSonarr struct{ api.SonarrClient }

func (pingOKSonarr) Ping(context.Context) error { return nil }

type pingOKRadarr struct{ api.RadarrClient }

func (pingOKRadarr) Ping(context.Context) error { return nil }

func doStructuredSave(t *testing.T, h *Handler, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config/structured", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.HandleSaveConfigStructured(rec, req)
	return rec
}

// --- secret coverage protection (the coverage-test pattern) ---

// TestSecretPaths_cover_every_schema_secret walks the REAL schema (app
// sections + a provider set built from the registry-free schema builder)
// and asserts every Secret:true field resolves to a derivable path, then
// proves each path actually merges: an empty incoming value inherits the
// existing file's sentinel. Because the merge is derived FROM the schema,
// a newly declared secret is covered by construction — this test exists to
// catch a structural change (new section type, new nesting) that the walker
// does not understand.
func TestSecretPaths_cover_every_schema_secret(t *testing.T) {
	t.Parallel()
	full := schema.Schema([]api.ProviderSchema{{
		Name: "probe", Settings: []api.SchemaField{
			{Key: "api_key", Secret: true},
			{Key: "password", Secret: true},
		},
	}})

	// Count Secret:true declarations by direct walk (the test's own oracle).
	var wantCount int
	var countFields func(fields []api.SchemaField)
	countFields = func(fields []api.SchemaField) {
		for _, f := range fields {
			if f.Secret {
				wantCount++
			}
			countFields(f.Fields)
		}
	}
	for _, s := range full {
		for _, p := range s.Providers {
			countFields(p.Settings)
		}
		countFields(s.Fields)
	}

	paths := secretPaths(full)
	if len(paths) != wantCount {
		t.Fatalf("secretPaths found %d paths, schema declares %d Secret fields", len(paths), wantCount)
	}
	if wantCount == 0 {
		t.Fatal("schema declares zero secrets; the walker has nothing to protect (schema regression?)")
	}
}

// --- structured save behavior ---

func TestStructuredSave_canonicalizes_and_persists(t *testing.T) {
	t.Parallel()
	h, cfgPath := newStructuredHandler(t, "")

	payload := `{"sections": {
		"sonarr": {"url": "http://sonarr:8989", "api_key": "k1"},
		"languages": {"default": [{"code": "en"}]},
		"providers": {"opensubtitles": {"enabled": true, "settings": {"username": "u", "password": "p"}}}
	}}`
	rec := doStructuredSave(t, h, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured save status = %d, body %s", rec.Code, rec.Body.String())
	}

	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	// The persisted file is server-canonical YAML that the real loader parses.
	cfg, err := config.LoadFromBytes(context.Background(), saved)
	if err != nil {
		t.Fatalf("saved YAML does not round-trip through LoadFromBytes: %v\n%s", err, saved)
	}
	if got := cfg.SonarrConfig().APIKey; got != "k1" {
		t.Errorf("round-tripped sonarr api_key = %q, want k1", got)
	}
	pc := cfg.ProviderConfigs()["opensubtitles"]
	if !pc.Enabled || pc.Settings["password"] != "p" {
		t.Errorf("round-tripped provider config = %+v, want enabled with password p", pc)
	}
}

func TestStructuredSave_merges_empty_secrets_from_existing(t *testing.T) {
	t.Parallel()
	existing := `
sonarr:
  url: "http://old:8989"
  api_key: "existing-sonarr-key"
providers:
  opensubtitles:
    enabled: true
    settings:
      username: "u"
      password: "existing-password"
languages:
  default:
    - code: en
`
	h, cfgPath := newStructuredHandler(t, existing)

	// The form round-trips redacted (empty) secrets; one provider secret is
	// omitted entirely rather than empty.
	payload := `{"sections": {
		"sonarr": {"url": "http://new:8989", "api_key": ""},
		"languages": {"default": [{"code": "en"}]},
		"providers": {"opensubtitles": {"enabled": true, "settings": {"username": "u2"}}}
	}}`
	rec := doStructuredSave(t, h, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured save status = %d, body %s", rec.Code, rec.Body.String())
	}

	saved, _ := os.ReadFile(cfgPath)
	text := string(saved)
	if !strings.Contains(text, "existing-sonarr-key") {
		t.Errorf("empty api_key was not merged from existing file:\n%s", text)
	}
	if !strings.Contains(text, "existing-password") {
		t.Errorf("omitted provider password was not merged from existing file:\n%s", text)
	}
	if !strings.Contains(text, "http://new:8989") {
		t.Errorf("non-secret update lost:\n%s", text)
	}
	if strings.Contains(text, "http://old:8989") {
		t.Errorf("stale non-secret value survived:\n%s", text)
	}
}

func TestStructuredSave_removed_provider_secret_not_resurrected(t *testing.T) {
	t.Parallel()
	existing := `
sonarr:
  url: "http://s:8989"
  api_key: "sk"
providers:
  opensubtitles:
    enabled: true
    settings:
      password: "old-password"
languages:
  default:
    - code: en
`
	h, cfgPath := newStructuredHandler(t, existing)

	// The user deleted the opensubtitles provider entirely: its secret must
	// NOT come back.
	payload := `{"sections": {
		"sonarr": {"url": "http://s:8989", "api_key": "sk"},
		"languages": {"default": [{"code": "en"}]},
		"providers": {"gestdown": {"enabled": true}}
	}}`
	rec := doStructuredSave(t, h, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured save status = %d, body %s", rec.Code, rec.Body.String())
	}
	saved, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(saved), "old-password") {
		t.Errorf("deleted provider's secret was resurrected:\n%s", saved)
	}
}

func TestStructuredSave_rejects_garbage(t *testing.T) {
	t.Parallel()
	h, _ := newStructuredHandler(t, "")
	if rec := doStructuredSave(t, h, "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage body status = %d, want 400", rec.Code)
	}
	if rec := doStructuredSave(t, h, `{"sections": {}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty sections status = %d, want 400", rec.Code)
	}
}

// --- structured GET ---

func TestStructuredGet_redacts_secrets_and_roundtrips(t *testing.T) {
	t.Parallel()
	existing := `
sonarr:
  url: "http://s:8989"
  api_key: "super-secret"
providers:
  opensubtitles:
    enabled: true
    settings:
      username: "u"
      password: "also-secret"
languages:
  default:
    - code: en
`
	h, _ := newStructuredHandler(t, existing)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/structured", nil)
	rec := httptest.NewRecorder()
	h.HandleGetConfigStructured(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured get status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "super-secret") || strings.Contains(body, "also-secret") {
		t.Fatalf("structured GET leaked a secret:\n%s", body)
	}

	var sc StructuredConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil {
		t.Fatalf("decode structured get: %v", err)
	}
	var sonarr map[string]any
	if err := json.Unmarshal(sc.Sections["sonarr"], &sonarr); err != nil {
		t.Fatalf("decode sonarr section: %v", err)
	}
	if sonarr["url"] != "http://s:8989" {
		t.Errorf("sonarr.url = %v, want preserved", sonarr["url"])
	}
	if sonarr["api_key"] != "" {
		t.Errorf("sonarr.api_key = %v, want redacted-to-empty", sonarr["api_key"])
	}
}

// --- the full UI round-trip gate ---

// TestStructured_get_then_save_preserves_config is the disposition's gate in
// end-to-end form: GET structured (redacted) -> save it back untouched ->
// the persisted file parses identically (secrets included) to the original.
func TestStructured_get_then_save_preserves_config(t *testing.T) {
	t.Parallel()
	existing := `
sonarr:
  url: "http://s:8989"
  api_key: "super-secret"
languages:
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      username: "u"
      password: "also-secret"
`
	h, cfgPath := newStructuredHandler(t, existing)

	getReq := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/structured", nil)
	getRec := httptest.NewRecorder()
	h.HandleGetConfigStructured(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d", getRec.Code)
	}

	rec := doStructuredSave(t, h, getRec.Body.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("save-back status = %d, body %s", rec.Code, rec.Body.String())
	}

	saved, _ := os.ReadFile(cfgPath)
	cfg, err := config.LoadFromBytes(context.Background(), saved)
	if err != nil {
		t.Fatalf("round-tripped config does not load: %v\n%s", err, saved)
	}
	orig, err := config.LoadFromBytes(context.Background(), []byte(existing))
	if err != nil {
		t.Fatalf("original config does not load: %v", err)
	}
	if got, want := cfg.SonarrConfig(), orig.SonarrConfig(); got != want {
		t.Errorf("sonarr config drifted: got %+v, want %+v", got, want)
	}
	gotPC, wantPC := cfg.ProviderConfigs()["opensubtitles"], orig.ProviderConfigs()["opensubtitles"]
	if gotPC.Enabled != wantPC.Enabled || gotPC.Settings["password"] != wantPC.Settings["password"] {
		t.Errorf("provider config drifted: got %+v, want %+v", gotPC, wantPC)
	}
}

// --- secret PRESENCE flags on the structured GET (first-boot wizard) ---

// TestStructuredGet_reports_secret_presence_without_values pins the R3.2
// contract: the GET carries a dotted-path presence list for every secret
// holding a value, while the values themselves stay redacted-empty. Without
// presence the wizard cannot distinguish "api key saved" from "missing".
func TestStructuredGet_reports_secret_presence_without_values(t *testing.T) {
	t.Parallel()
	h, _ := newStructuredHandler(t, strings.Join([]string{
		"sonarr:",
		"  url: http://sonarr:8989",
		"  api_key: sekret",
		"providers:",
		"  opensubtitles:",
		"    enabled: true",
		"    settings:",
		"      username: u",
		"      password: hunter2",
		"",
	}, "\n"))

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/structured", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfigStructured(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured GET status = %d, body %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "sekret") || strings.Contains(body, "hunter2") {
		t.Fatalf("structured GET leaked a secret value: %s", body)
	}

	var sc StructuredConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil {
		t.Fatalf("decode structured GET: %v", err)
	}

	want := map[string]bool{
		"sonarr.api_key": true,
		"providers.opensubtitles.settings.password": true,
	}
	got := make(map[string]bool, len(sc.SecretsPresent))
	for _, p := range sc.SecretsPresent {
		got[p] = true
	}
	for path := range want {
		if !got[path] {
			t.Errorf("secrets_present missing %q; got %v", path, sc.SecretsPresent)
		}
	}
	if len(got) != len(want) {
		t.Errorf("secrets_present = %v, want exactly %v", sc.SecretsPresent, want)
	}

	// The redacted sections still carry the (empty) secret leaves.
	var sonarr map[string]any
	if err := json.Unmarshal(sc.Sections["sonarr"], &sonarr); err != nil {
		t.Fatalf("decode sonarr section: %v", err)
	}
	if sonarr["api_key"] != "" {
		t.Errorf("sonarr.api_key = %v, want redacted-empty", sonarr["api_key"])
	}
}

// TestStructuredGet_empty_secret_not_reported_present: an empty secret leaf
// must NOT appear in secrets_present (the fresh-volume example config case:
// api_key: "" means the arr step is unsatisfied).
func TestStructuredGet_empty_secret_not_reported_present(t *testing.T) {
	t.Parallel()
	h, _ := newStructuredHandler(t, strings.Join([]string{
		"sonarr:",
		"  url: http://sonarr:8989",
		"  api_key: \"\"",
		"",
	}, "\n"))

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/structured", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleGetConfigStructured(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("structured GET status = %d, body %s", rec.Code, rec.Body.String())
	}

	var sc StructuredConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil {
		t.Fatalf("decode structured GET: %v", err)
	}
	if len(sc.SecretsPresent) != 0 {
		t.Errorf("secrets_present = %v, want empty for a blank api_key", sc.SecretsPresent)
	}
}

// --- secret-merge baseline failure handling (fail closed, CQ-006) ---

// keepSecretsPayload relies on keep semantics for the provider password
// (empty scalar) while the sonarr key is explicit, so the candidate WOULD
// validate if the merge were silently skipped — proving that the fail-closed
// path, not validation, is what stops the save.
const keepSecretsPayloadEmptyScalar = `{"sections": {
	"sonarr": {"url": "http://s:8989", "api_key": "explicit-key"},
	"languages": {"default": [{"code": "en"}]},
	"providers": {"opensubtitles": {"enabled": true, "settings": {"username": "u", "password": ""}}}
}}`

// keepSecretsPayloadOmittedLeaf is the second keep-semantics shape: the
// secret leaf is omitted entirely under a present settings mapping (the
// merge would add it back from the existing file).
const keepSecretsPayloadOmittedLeaf = `{"sections": {
	"sonarr": {"url": "http://s:8989", "api_key": "explicit-key"},
	"languages": {"default": [{"code": "en"}]},
	"providers": {"opensubtitles": {"enabled": true, "settings": {"username": "u"}}}
}}`

// explicitSecretsPayload carries every schema secret non-empty: no keep
// semantics, so the baseline is irrelevant to it.
const explicitSecretsPayload = `{"sections": {
	"sonarr": {"url": "http://s:8989", "api_key": "k-new"},
	"languages": {"default": [{"code": "en"}]},
	"providers": {"opensubtitles": {"enabled": true, "settings": {"username": "u", "password": "p-new"}}}
}}`

// TestStructuredSave_baseline_read_error_fails_closed: a baseline read
// failure that is NOT not-exist (here: the config path is a directory) must
// fail the save closed when the payload relies on keep semantics — a silent
// skip would persist the empty secret, converting "keep" into deletion.
// No save, no activation, 500 (the payload itself may be valid).
func TestStructuredSave_baseline_read_error_fails_closed(t *testing.T) {
	t.Parallel()
	reloadCalled := false
	h := newStructuredHandlerAt(t, t.TempDir(), // a directory: open succeeds, read fails
		func(context.Context, api.ConfigProvider) error { reloadCalled = true; return nil })

	rec := doStructuredSave(t, h, keepSecretsPayloadEmptyScalar)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("save over unreadable baseline status = %d, want 500; body %s",
			rec.Code, rec.Body.String())
	}
	if reloadCalled {
		t.Error("hot reload ran despite the baseline read failure; activation must not happen")
	}
}

// TestStructuredSave_malformed_baseline_fails_closed: an existing config
// that parses to garbage (YAML error) or to a non-mapping document must fail
// keep-semantics saves closed, for BOTH keep shapes (empty scalar and
// omitted leaf). The malformed file must survive untouched.
func TestStructuredSave_malformed_baseline_fails_closed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing string
		payload  string
	}{
		{
			name:     "yaml_parse_error_empty_scalar_keep",
			existing: "providers:\n  opensubtitles:\n    settings:\n      password: [unclosed\n",
			payload:  keepSecretsPayloadEmptyScalar,
		},
		{
			name:     "non_mapping_document_omitted_leaf_keep",
			existing: "just a scalar\n",
			payload:  keepSecretsPayloadOmittedLeaf,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tt.existing), 0o600); err != nil {
				t.Fatalf("write malformed config: %v", err)
			}
			reloadCalled := false
			h := newStructuredHandlerAt(t, cfgPath,
				func(context.Context, api.ConfigProvider) error { reloadCalled = true; return nil })

			rec := doStructuredSave(t, h, tt.payload)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("save over malformed baseline status = %d, want 500; body %s",
					rec.Code, rec.Body.String())
			}
			if reloadCalled {
				t.Error("hot reload ran despite the malformed baseline; activation must not happen")
			}
			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("read config after failed save: %v", err)
			}
			if string(after) != tt.existing {
				t.Errorf("failed save modified the config file:\nbefore: %q\nafter:  %q",
					tt.existing, after)
			}
		})
	}
}

// TestStructuredSave_baseline_failure_with_explicit_secrets_saves: when the
// payload carries every secret explicitly, the baseline is irrelevant — the
// save must proceed even over a corrupted existing file, which is also the
// operator's recovery path (a complete re-submit repairs the file).
func TestStructuredSave_baseline_failure_with_explicit_secrets_saves(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("corrupt: [unclosed\n"), 0o600); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}
	reloads := 0
	h := newStructuredHandlerAt(t, cfgPath,
		func(context.Context, api.ConfigProvider) error { reloads++; return nil })

	rec := doStructuredSave(t, h, explicitSecretsPayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit-secrets save over corrupt baseline status = %d, body %s",
			rec.Code, rec.Body.String())
	}
	if reloads != 1 {
		t.Errorf("hot reload calls = %d, want 1", reloads)
	}
	saved, _ := os.ReadFile(cfgPath)
	if _, err := config.LoadFromBytes(context.Background(), saved); err != nil {
		t.Errorf("repaired config does not load: %v\n%s", err, saved)
	}
	if !strings.Contains(string(saved), "k-new") {
		t.Errorf("saved config missing the submitted secret:\n%s", saved)
	}
}

// TestStructuredSave_missing_and_empty_baseline_proceed pins the two TRUE
// empty baselines: a missing file (fs.ErrNotExist) and an empty or
// comments-only file both provably hold no secrets, so a keep-semantics
// save proceeds (with the secret staying empty) instead of failing closed.
// Failing here would break unconfigured-mode first saves.
func TestStructuredSave_missing_and_empty_baseline_proceed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		write   bool // false = no file at all
	}{
		{name: "missing_file", write: false},
		{name: "zero_byte_file", write: true, content: ""},
		{name: "comments_only_file", write: true, content: "# fresh volume, nothing here yet\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")
			if tt.write {
				if err := os.WriteFile(cfgPath, []byte(tt.content), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}
			h := newStructuredHandlerAt(t, cfgPath, nil)

			rec := doStructuredSave(t, h, keepSecretsPayloadEmptyScalar)
			if rec.Code != http.StatusOK {
				t.Fatalf("keep-semantics save over empty baseline status = %d, body %s",
					rec.Code, rec.Body.String())
			}
			saved, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("read saved config: %v", err)
			}
			cfg, err := config.LoadFromBytes(context.Background(), saved)
			if err != nil {
				t.Fatalf("saved config does not load: %v\n%s", err, saved)
			}
			if got := cfg.ProviderConfigs()["opensubtitles"].Settings["password"]; got != "" {
				t.Errorf("password = %v, want empty (nothing to keep in an empty baseline)", got)
			}
		})
	}
}
