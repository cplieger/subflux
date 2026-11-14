package formschema

import (
	"slices"

	"go.yaml.in/yaml/v4"
)

const msgBootOnly = "changes only at startup: edit config.yaml and restart subflux"

// BootOnlyChanges refuses each BootOnly field whose value in next differs
// from its value in stored, at section-relative paths in schema order. Each
// node is a section's mapping, nil when absent, read for those keys alone:
// an absent, null or unreadable value counts as the field's default, so a
// stored section that fails to decode still compares.
func (s Schema) BootOnlyChanges(stored, next *yaml.Node) []FieldError {
	var errs []FieldError
	for i := range s.Fields {
		f := &s.Fields[i]
		if f.BootOnly && !bootValue(f, stored).equal(bootValue(f, next)) {
			errs = append(errs, FieldError{f.Key, msgBootOnly})
		}
	}
	return errs
}

// bootValue is f's value in n, or its default where n gives it none that
// decodes; the first of a repeated key is read.
func bootValue(f *Field, n *yaml.Node) Value {
	if v := firstValue(n, f.Key); v != nil && !IsNull(v) && !sharesNodes(v) {
		if val, errs := decodeField(f, v); len(errs) == 0 {
			return val
		}
	}
	vals := Values{f.Key: {kind: f.Kind}}
	_ = Schema{}.absent(f, vals)
	return vals[f.Key]
}

// firstValue is the value of key's first occurrence in n, nil when n is no
// mapping or lacks it.
func firstValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func (v Value) equal(w Value) bool {
	return v.kind == w.kind && v.text == w.text && v.i == w.i && v.d == w.d && v.b == w.b && slices.Equal(v.list, w.list)
}
