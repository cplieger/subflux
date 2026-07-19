package provider

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// P14: NormalizeSettings makes the schema declaration the single source of
// setting defaults — the registry fills absent declared fields before a
// factory ever sees the map.

func TestNormalizeSettings_fills_absent_declared_defaults(t *testing.T) {
	t.Parallel()
	fields := []api.ProviderSchemaField{
		{Key: "use_hash", Type: "bool", Default: "true"},
		{Key: "mode", Type: "text", Default: "static"},
		{Key: "api_key", Type: "secret"}, // no default: never injected
	}
	got := NormalizeSettings(fields, map[string]any{"passkey": "abc"})

	if v, ok := got["use_hash"].(bool); !ok || !v {
		t.Errorf("use_hash = %v (%T), want bool true from declared default", got["use_hash"], got["use_hash"])
	}
	if got["mode"] != "static" {
		t.Errorf("mode = %v, want declared text default", got["mode"])
	}
	if _, present := got["api_key"]; present {
		t.Errorf("api_key injected despite having no declared default")
	}
	if got["passkey"] != "abc" {
		t.Errorf("undeclared passthrough key lost: %v", got["passkey"])
	}
}

func TestNormalizeSettings_never_overwrites_user_values(t *testing.T) {
	t.Parallel()
	fields := []api.ProviderSchemaField{
		{Key: "use_hash", Type: "bool", Default: "true"},
		{Key: "mode", Type: "text", Default: "static"},
	}
	raw := map[string]any{"use_hash": false, "mode": "error"}
	got := NormalizeSettings(fields, raw)

	if v, ok := got["use_hash"].(bool); !ok || v {
		t.Errorf("use_hash = %v, want the user's explicit false preserved", got["use_hash"])
	}
	if got["mode"] != "error" {
		t.Errorf("mode = %v, want the user's explicit value preserved", got["mode"])
	}
	// The input map itself is never mutated.
	if len(raw) != 2 {
		t.Errorf("input map mutated: %v", raw)
	}
}

func TestNormalizeSettings_no_declarations_is_identity(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"anything": 1}
	if got := NormalizeSettings(nil, raw); len(got) != 1 || got["anything"] != 1 {
		t.Errorf("NormalizeSettings(nil, raw) = %v, want raw unchanged", got)
	}
}

func TestNormalizeSettings_typed_extraction_end_to_end(t *testing.T) {
	t.Parallel()
	// The normalized map reads back through the same typed accessors the
	// factories use: the declared default is indistinguishable from a
	// user-set value.
	fields := []api.ProviderSchemaField{{Key: "use_hash", Type: "bool", Default: "true"}}
	ps := FromMap(NormalizeSettings(fields, map[string]any{"username": "u"}))
	if !ps.UseHash {
		t.Errorf("FromMap over normalized map: UseHash = false, want declared true")
	}
	if ps.Username != "u" {
		t.Errorf("Username = %q, want u", ps.Username)
	}
}
