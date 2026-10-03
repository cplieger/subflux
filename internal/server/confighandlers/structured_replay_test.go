package confighandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/subflux"
	yaml "go.yaml.in/yaml/v3"
)

// The settings form's save payload, captured by config.roundtrip.test.ts from
// the form rendered against the real schema, replayed through the real save
// handler and loader. The fixtures are written by the root package's
// settings_schema_test.go, the only package that can build the real registry.
const settingsFixtures = "../static-src/testdata"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(settingsFixtures, name))
	if err != nil {
		t.Fatalf("Setup: read fixture %s: %v", name, err)
	}
	return data
}

// newReplayHandler serves the rendered real schema from its fixture, over a
// config file holding settings-config.yaml, with the real loader.
func newReplayHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	var sections []subflux.SchemaSection
	if err := json.Unmarshal(readFixture(t, "settings-schema.json"), &sections); err != nil {
		t.Fatalf("Setup: decode settings-schema.json: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, readFixture(t, "settings-config.yaml"), 0o600); err != nil {
		t.Fatalf("Setup: write config: %v", err)
	}
	h := New(&Deps{
		SchemaFunc: func([]subflux.ProviderSchema) []subflux.SchemaSection { return sections },
		LoadConfig: func(data []byte) (*config.Config, error) {
			return config.LoadFromBytes(t.Context(), data)
		},
		HotReload:  func(context.Context, *config.Config) error { return nil },
		State:      func() StateView { return StateView{} },
		ConfigPath: func() string { return cfgPath },
		NewSonarr:  func(_, _ string) (ArrPinger, error) { return pingOK{}, nil },
		NewRadarr:  func(_, _ string) (ArrPinger, error) { return pingOK{}, nil },
	})
	return h, cfgPath
}

// saveAndRead saves sections and returns the file the save wrote, decoded.
func saveAndRead(t *testing.T, h *Handler, cfgPath string, sections map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"sections": sections})
	if err != nil {
		t.Fatalf("Setup: encode payload: %v", err)
	}
	rec := doStructuredSave(t, h, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSaveConfigStructured(golden payload) = %d, want 200: %s", rec.Code, rec.Body)
	}
	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if _, err := config.LoadFromBytes(t.Context(), saved); err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
	return decodeYAMLMap(t, saved)
}

func decodeYAMLMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode YAML: %v", err)
	}
	return m
}

// providersEnabled sets every provider's enabled flag in a decoded config or
// payload and returns it, so two documents can be compared on everything else.
func providersEnabled(t *testing.T, doc map[string]any, enabled bool) map[string]any {
	t.Helper()
	providers, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("document has no providers mapping: %v", doc["providers"])
	}
	for name, block := range providers {
		b, ok := block.(map[string]any)
		if !ok {
			t.Fatalf("provider %s is not a mapping: %v", name, block)
		}
		b["enabled"] = enabled
	}
	return doc
}

func TestStructuredSave_replays_the_settings_form_payload(t *testing.T) {
	t.Parallel()
	h, cfgPath := newReplayHandler(t)
	var payload map[string]any
	if err := json.Unmarshal(readFixture(t, "settings-save-payload.golden.json"), &payload); err != nil {
		t.Fatalf("Setup: decode golden payload: %v", err)
	}

	first := saveAndRead(t, h, cfgPath, payload)

	// The form toggled every provider off; everything else, secrets included,
	// must come back as the fixture config holds it.
	want := providersEnabled(t, decodeYAMLMap(t, readFixture(t, "settings-config.yaml")), false)
	if !reflect.DeepEqual(first, want) {
		t.Errorf("first save of the golden payload wrote\n%v\nwant the fixture config with providers disabled\n%v",
			first, want)
	}

	second := saveAndRead(t, h, cfgPath, providersEnabled(t, payload, true))
	if !reflect.DeepEqual(providersEnabled(t, second, false), first) {
		t.Errorf("re-enabling every provider changed more than enabled:\nsecond %v\nfirst  %v", second, first)
	}
}

// TestStructuredGet_redacts_secrets_the_schema_types_as_secret runs the real
// schema, where the arr api_key and the OIDC client secret are declared by type.
func TestStructuredGet_redacts_secrets_the_schema_types_as_secret(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	existing := strings.Join([]string{
		"sonarr:",
		"  url: http://sonarr:8989",
		"  api_key: placeholder-sonarr-key",
		"auth:",
		"  oidc:",
		"    client_id: placeholder-client-id",
		"    client_secret: placeholder-client-secret",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("Setup: write config: %v", err)
	}
	h := New(&Deps{SchemaFunc: schema.Sections, ConfigPath: func() string { return cfgPath }})

	rec := httptest.NewRecorder()
	h.HandleGetConfigStructured(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/config/structured", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetConfigStructured = %d, want 200: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, "placeholder-sonarr-key") ||
		strings.Contains(body, "placeholder-client-secret") {
		t.Errorf("structured GET served a secret in clear: %s", body)
	}
	var sc StructuredConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil {
		t.Fatalf("decode structured GET: %v", err)
	}
	if want := []string{"sonarr.api_key", "auth.oidc.client_secret"}; !reflect.DeepEqual(sc.SecretsPresent, want) {
		t.Errorf("secrets_present = %v, want %v", sc.SecretsPresent, want)
	}
	if !strings.Contains(string(sc.Sections["auth"]), `"client_id":"placeholder-client-id"`) {
		t.Errorf("auth section = %s, want the non-secret client_id kept", sc.Sections["auth"])
	}
}
