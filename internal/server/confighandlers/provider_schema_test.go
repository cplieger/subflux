package confighandlers

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
)

// These tests used to live in internal/server and exercise this function
// through internal/api, which implemented no registry and consumed no schema.
// The function moved to its one caller; the tests came with it.
//
// They drive a REAL provider.Registry rather than a two-method fake, because
// one of the assertions is that the output follows ProviderNames' ordering —
// a fake would let the test define the very ordering it claims to check.

// schemaStubProvider already exists in handler_test.go, which registers the
// same shape for the /api/config/schema endpoint test.

func registerStub(reg *provider.Registry, name string) {
	reg.Register(api.ProviderID(name), func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &schemaStubProvider{name: name}, nil
	})
}

func TestBuildProviderSchemas_empty_registry(t *testing.T) {
	t.Parallel()
	schemas := BuildProviderSchemas(provider.NewRegistry())
	if len(schemas) != 0 {
		t.Errorf("BuildProviderSchemas(empty) = %d schemas, want 0", len(schemas))
	}
}

func TestBuildProviderSchemas_with_providers(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	registerStub(reg, "gestdown")
	registerStub(reg, "opensubtitles")
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", []api.ProviderSchemaField{
		{Key: "api_key", Label: "API Key", Type: "secret", Secret: true},
		{Key: "username", Label: "Username", Type: "text"},
	})
	// gestdown has no schema registered; label should fall back to name.

	schemas := BuildProviderSchemas(reg)

	if len(schemas) != 2 {
		t.Fatalf("BuildProviderSchemas() = %d schemas, want 2", len(schemas))
	}

	// Schemas should be in sorted order (from ProviderNames).
	if schemas[0].Name != "gestdown" {
		t.Errorf("schemas[0].Name = %q, want %q", schemas[0].Name, "gestdown")
	}
	if schemas[1].Name != "opensubtitles" {
		t.Errorf("schemas[1].Name = %q, want %q", schemas[1].Name, "opensubtitles")
	}

	// gestdown: label falls back to name.
	if schemas[0].Label != "gestdown" {
		t.Errorf("gestdown.Label = %q, want %q (fallback to name)", schemas[0].Label, "gestdown")
	}

	if schemas[1].Label != "OpenSubtitles" {
		t.Errorf("opensubtitles.Label = %q, want %q", schemas[1].Label, "OpenSubtitles")
	}
	if len(schemas[1].Settings) != 2 {
		t.Fatalf("opensubtitles.Settings = %d fields, want 2", len(schemas[1].Settings))
	}
	if schemas[1].Settings[0].Key != "api_key" {
		t.Errorf("settings[0].Key = %q, want %q", schemas[1].Settings[0].Key, "api_key")
	}
	if !schemas[1].Settings[0].Secret {
		t.Error("settings[0].Secret = false, want true")
	}
}

func TestBuildProviderSchemas_excludes_mock_provider(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	registerStub(reg, "mock")
	reg.RegisterSchema("mock", "Mock Provider", nil)
	registerStub(reg, "opensubtitles")
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", nil)

	schemas := BuildProviderSchemas(reg, "mock")
	for _, s := range schemas {
		if s.Name == "mock" {
			t.Error("BuildProviderSchemas should exclude 'mock' provider")
		}
	}
	if len(schemas) != 1 {
		t.Errorf("BuildProviderSchemas len = %d, want 1 (mock excluded)", len(schemas))
	}
}
