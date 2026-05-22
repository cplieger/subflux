package main

import (
	"testing"

	"subflux/internal/api"
	"subflux/internal/server"
)

func TestNewProviderRegistry_registers_all_providers(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()
	names := r.ProviderNames()

	want := []string{
		"animetosho",
		"betaseries",
		"embedded",
		"gestdown",
		"hdbits",
		"mock",
		"opensubtitles",
		"subdl",
		"subsource",
		"yifysubtitles",
	}

	if len(names) != len(want) {
		t.Fatalf("newProviderRegistry() registered %d providers, want %d\n  got:  %v\n  want: %v",
			len(names), len(want), names, want)
	}
	for i, name := range names {
		if string(name) != want[i] {
			t.Errorf("newProviderRegistry() provider[%d] = %q, want %q", i, name, want[i])
		}
	}
}

func TestNewProviderRegistry_all_providers_have_schemas(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()

	for _, name := range r.ProviderNames() {
		label, _ := r.Schema(name)
		if label == "" {
			t.Errorf("newProviderRegistry() provider %q has empty schema label", name)
		}
	}
}

func TestNewProviderRegistry_schema_labels(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()

	tests := []struct {
		name      api.ProviderID
		wantLabel string
	}{
		{"embedded", "Embedded"},
		{"hdbits", "HDBits"},
		{"opensubtitles", "OpenSubtitles"},
		{"betaseries", "BetaSeries"},
		{"gestdown", "Gestdown"},
		{"mock", "Mock (Testing)"},
		{"subsource", "SubSource"},
		{"subdl", "SubDL"},
		{"animetosho", "AnimeTosho"},
		{"yifysubtitles", "YIFY Subtitles"},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			t.Parallel()

			label, _ := r.Schema(tt.name)
			if label != tt.wantLabel {
				t.Errorf("Schema(%q) label = %q, want %q", tt.name, label, tt.wantLabel)
			}
		})
	}
}

func TestNewProviderRegistry_secret_fields_marked(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()

	// Providers with secret fields and their expected secret key names.
	wantSecrets := map[api.ProviderID][]string{
		"hdbits":        {"passkey"},
		"opensubtitles": {"password", "api_key"},
		"betaseries":    {"token"},
		"subsource":     {"api_key"},
		"subdl":         {"api_key"},
		"animetosho":    {"anidb_client_key"},
	}

	for provName, wantKeys := range wantSecrets {
		_, fields := r.Schema(provName)
		secretKeys := make(map[string]bool)
		for _, f := range fields {
			if f.Secret {
				secretKeys[f.Key] = true
			}
		}
		for _, wantKey := range wantKeys {
			if !secretKeys[wantKey] {
				t.Errorf("Schema(%q) field %q should be marked Secret", provName, wantKey)
			}
		}
	}
}

func TestNewProviderRegistry_secret_type_implies_secret_flag(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()

	for _, name := range r.ProviderNames() {
		_, fields := r.Schema(name)
		for _, f := range fields {
			if f.Type == "secret" && !f.Secret {
				t.Errorf("provider %q field %q has Type=%q but Secret=false; "+
					"set Secret: true to ensure redaction in GET /api/config",
					name, f.Key, f.Type)
			}
		}
	}
}

func TestSecretKeysCoverProviderSchemas(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()
	knownKeys := make(map[string]bool)
	for _, k := range server.SecretKeyNames() {
		knownKeys[k] = true
	}

	for _, name := range r.ProviderNames() {
		_, fields := r.Schema(name)
		for _, f := range fields {
			if f.Secret && !knownKeys[f.Key] {
				t.Errorf("provider %q has Secret field %q not in secretKeyNames; "+
					"add it to secretKeyNames in config_handlers.go and "+
					"secretKeyRe to prevent credential leakage via GET /api/config",
					name, f.Key)
			}
		}
	}
}
