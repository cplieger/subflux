package providertest

import (
	"testing"

	"github.com/cplieger/subflux/internal/provider"
	"go.yaml.in/yaml/v4"
)

// Config decodes values, a YAML mapping of e's settings ("" for none), by
// e's schema into an enabled Config, failing t on a parse or field error.
func Config(t testing.TB, e provider.Entry, values string) provider.Config { //nolint:gocritic // hugeParam: tests pass NewEntry() inline
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(values), &doc); err != nil {
		t.Fatalf("providertest.Config: %v", err)
	}
	var n *yaml.Node
	if len(doc.Content) > 0 {
		n = doc.Content[0]
	}
	vals, errs := e.Schema.Decode(n)
	if len(errs) > 0 {
		t.Fatalf("providertest.Config: %s: %v", e.ID, errs)
	}
	return provider.Config{ID: e.ID, Enabled: true, Values: vals}
}

// Build is provider.Build over Config(t, e, values), failing t on its error.
func Build(t testing.TB, e provider.Entry, values string) provider.Adapter { //nolint:gocritic // hugeParam: tests pass NewEntry() inline
	t.Helper()
	a, err := provider.Build(e, Config(t, e, values))
	if err != nil {
		t.Fatalf("providertest.Build: %v", err)
	}
	return a
}
