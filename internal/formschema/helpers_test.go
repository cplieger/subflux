package formschema

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// section parses src as one YAML document and returns its root mapping.
func section(t testing.TB, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	if len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// valueNode is the value of key in mapping m.
func valueNode(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func errPaths(errs []FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Path)
	}
	return out
}
