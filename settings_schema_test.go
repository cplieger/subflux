package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/subflux"
)

// The settings form's vitest suite and confighandlers' save replay both read
// fixtures this file writes, because only this package can build the real
// provider registry: the rendered schema, and the structured GET of
// settings-config.yaml through the real handler.
const settingsFixtureDir = "internal/server/static-src/testdata"

const updateFixturesEnv = "SUBFLUX_UPDATE_FIXTURES"

func realSchema() []subflux.SchemaSection {
	return schema.Sections(confighandlers.BuildProviderSchemas(
		newProviderRegistry(), string(subflux.ProviderNameSynthetic),
	))
}

func TestSettingsFixtures_match_the_real_schema_and_structured_get(t *testing.T) {
	t.Parallel()
	cfgYAML, err := os.ReadFile(filepath.Join(settingsFixtureDir, "settings-config.yaml"))
	if err != nil {
		t.Fatalf("Setup: read fixture config: %v", err)
	}
	if _, err := config.LoadFromBytes(t.Context(), cfgYAML); err != nil {
		t.Fatalf("settings-config.yaml does not load: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, cfgYAML, 0o600); err != nil {
		t.Fatalf("Setup: write config: %v", err)
	}

	schemaJSON, err := json.MarshalIndent(realSchema(), "", "  ")
	if err != nil {
		t.Fatalf("Setup: encode schema: %v", err)
	}

	h := confighandlers.New(&confighandlers.Deps{
		Registry:   newProviderRegistry(),
		SchemaFunc: schema.Sections,
		ConfigPath: func() string { return cfgPath },
	})
	rec := httptest.NewRecorder()
	h.HandleGetConfigStructured(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/config/structured", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetConfigStructured(settings-config.yaml) = %d, want 200: %s", rec.Code, rec.Body)
	}
	var structuredJSON bytes.Buffer
	if err := json.Indent(&structuredJSON, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatalf("Setup: indent structured body: %v", err)
	}

	for name, want := range map[string][]byte{
		"settings-schema.json":     append(schemaJSON, '\n'),
		"settings-structured.json": append(structuredJSON.Bytes(), '\n'),
	} {
		path := filepath.Join(settingsFixtureDir, name)
		if os.Getenv(updateFixturesEnv) == "1" {
			if err := os.WriteFile(path, want, 0o600); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s (regenerate with %s=1 go test -run TestSettingsFixtures .): %v",
				path, updateFixturesEnv, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; regenerate with %s=1 go test -run TestSettingsFixtures . "+
				"and re-run config.roundtrip.test.ts with -u", path, updateFixturesEnv)
		}
	}
}

func TestSettingsSchema_flags_every_secret_typed_field(t *testing.T) {
	t.Parallel()
	var walk func(path string, fields []subflux.SchemaField)
	walk = func(path string, fields []subflux.SchemaField) {
		for _, f := range fields {
			if f.Type == "secret" && !f.Secret {
				t.Errorf("rendered schema field %s.%s has type secret but secret = false", path, f.Key)
			}
			walk(path+"."+f.Key, f.Fields)
		}
	}
	for _, s := range realSchema() {
		walk(s.Key, s.Fields)
		for _, p := range s.Providers {
			walk(s.Key+"."+p.Name+".settings", p.Settings)
		}
	}
}

// TestSettingsSchema_conn_test_set_matches_the_checkers pins which settings
// headers carry a credential-check control: both arrs plus every provider whose
// implementation can check its credentials.
func TestSettingsSchema_conn_test_set_matches_the_checkers(t *testing.T) {
	t.Parallel()
	placeholder := map[string]any{
		"username": "placeholder-u", "password": "placeholder-p", "api_key": "placeholder-k",
		"passkey": "placeholder-pk", "token": "placeholder-t", "anidb_client_key": "placeholder-c",
	}
	want := map[string]bool{"sonarr": true, "radarr": true}
	for _, e := range providerEntries {
		if e.name == subflux.ProviderNameSynthetic {
			continue
		}
		p, err := e.factory(t.Context(), placeholder)
		if err != nil {
			t.Fatalf("Setup: %s factory: %v", e.name, err)
		}
		if _, ok := p.(provider.CredentialChecker); ok {
			want[string(e.name)] = true
		}
	}

	got := map[string]bool{}
	for _, s := range realSchema() {
		if s.ConnTest {
			got[s.Key] = true
		}
		for _, p := range s.Providers {
			if p.ConnTest {
				got[p.Name] = true
			}
		}
	}
	if !maps.Equal(got, want) {
		t.Errorf("schema conn_test set = %v, want %v",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}
