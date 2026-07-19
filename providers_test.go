package main

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/confighandlers"
)

func TestNewProviderRegistry_registers_all_providers(t *testing.T) {
	t.Parallel()

	r := newProviderRegistry()
	names := r.ProviderNames()

	want := []string{
		"animetosho",
		"betaseries",
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
	for _, k := range confighandlers.SecretKeyNames() {
		knownKeys[k] = true
	}

	for _, name := range r.ProviderNames() {
		_, fields := r.Schema(name)
		for _, f := range fields {
			if f.Secret && !knownKeys[f.Key] {
				t.Errorf("provider %q has Secret field %q not in secretKeyNames; "+
					"add it to secretKeyNames in confighandlers/secrets.go and "+
					"secretKeyRe to prevent credential leakage via GET /api/config",
					name, f.Key)
			}
		}
	}
}

// TestProviderDefaults_declared_once_in_schema pins the P14 single-source
// property at the REAL declaration: the registry's schema entry for
// opensubtitles declares use_hash default true, and NormalizeSettings (the
// registry's pre-factory step) materializes it into an empty settings map.
// The factory side of the property — that opensubtitles no longer re-encodes
// the default — is pinned by that package's TestFactory_options "bare map"
// case, so the default exists in exactly one place: providerEntries.
func TestProviderDefaults_declared_once_in_schema(t *testing.T) {
	t.Parallel()
	r := newProviderRegistry()

	_, fields := r.Schema(api.ProviderNameOpenSubtitles)
	var declared *api.ProviderSchemaField
	for i := range fields {
		if fields[i].Key == "use_hash" {
			declared = &fields[i]
			break
		}
	}
	if declared == nil {
		t.Fatal("opensubtitles schema no longer declares use_hash; the single-source default moved or vanished")
	}
	if declared.Default != "true" || declared.Type != "bool" {
		t.Fatalf("use_hash declaration = %+v, want bool default true", declared)
	}

	normalized := provider.NormalizeSettings(fields, nil)
	if v, ok := normalized["use_hash"].(bool); !ok || !v {
		t.Errorf("NormalizeSettings over the real declaration: use_hash = %v (%T), want true",
			normalized["use_hash"], normalized["use_hash"])
	}
}

// TestProviderSchemaDefaults_all_normalize verifies every Default declared in
// providerEntries materializes through NormalizeSettings with the type its
// field declares — the whole table stays decodable as declarations change.
func TestProviderSchemaDefaults_all_normalize(t *testing.T) {
	t.Parallel()
	r := newProviderRegistry()
	for _, name := range r.ProviderNames() {
		_, fields := r.Schema(name)
		normalized := provider.NormalizeSettings(fields, nil)
		for _, f := range fields {
			if f.Default == "" {
				continue
			}
			v, present := normalized[f.Key]
			if !present {
				t.Errorf("%s.%s: declared default %q not materialized", name, f.Key, f.Default)
				continue
			}
			if f.Type == "bool" {
				if _, ok := v.(bool); !ok {
					t.Errorf("%s.%s: declared bool default materialized as %T", name, f.Key, v)
				}
			}
		}
	}
}
