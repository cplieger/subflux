// Package formschema is the form-shaped half of every settings section: a
// schema of typed fields, the decoder that reads a section's YAML node by
// that schema, and the marks that tell the settings document which scalars
// are safe to show.
package formschema

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is how a field's scalar text is read.
type Kind uint8

// The kinds a Field's text is read as; the zero Kind is none of them.
const (
	String Kind = iota + 1
	Secret
	Bool
	Int
	DurationMs
	Enum
	StringList
)

var kindNames = []string{"string", "secret", "bool", "int", "duration_ms", "enum", "string_list"}

var errUnknownKind = errors.New("formschema: unknown field kind")

// MarshalText answers the kind's snake_case name and refuses the zero Kind.
func (k Kind) MarshalText() ([]byte, error) {
	if k < String || k > StringList {
		return nil, fmt.Errorf("%w: %d", errUnknownKind, k)
	}
	return []byte(kindNames[k-1]), nil
}

// UnmarshalText reads a snake_case kind name, refusing every other spelling.
func (k *Kind) UnmarshalText(b []byte) error {
	for v := String; v <= StringList; v++ {
		if kindNames[v-1] == string(b) {
			*k = v
			return nil
		}
	}
	return fmt.Errorf("%w: %q", errUnknownKind, b)
}

// Schema is a section's flat fields, in form order.
type Schema struct{ Fields []Field }

// Field is one key of a section.
type Field struct {
	Key, Label, Help string
	// Default is the value Decode applies when the key is absent, in the
	// key's own YAML spelling; "" means no default.
	Default string
	// Enum lists an Enum field's accepted values.
	Enum []string
	// Min and Max bound an Int, or a DurationMs in milliseconds, inclusively.
	// Set both, with Min <= Max, or neither: both zero leave it unbounded,
	// and one alone bounds the other side at zero.
	Min, Max int64
	Kind     Kind
	// Required refuses an absent key, and for String and Secret an empty one.
	Required bool
}

// FieldError is one refused value: Path is relative to the node Decode was
// given until Prefix makes it absolute.
type FieldError struct{ Path, Message string }

// PlainTag marks a value a schema decoded as safe to show; UnchangedTag is
// what a read shows and a Save sends in place of a value kept as stored.
const (
	PlainTag     = "!plain"
	UnchangedTag = "!unchanged"
)

// Prefix returns errs with each path made absolute under prefix: a path
// starting with an index joins without a dot, and an empty path is prefix.
func Prefix(prefix string, errs []FieldError) []FieldError {
	out := make([]FieldError, 0, len(errs))
	for _, e := range errs {
		out = append(out, FieldError{Path: joinPath(prefix, e.Path), Message: e.Message})
	}
	return out
}

func joinPath(prefix, path string) string {
	switch {
	case prefix == "":
		return path
	case path == "":
		return prefix
	case strings.HasPrefix(path, "["):
		return prefix + path
	default:
		return prefix + "." + path
	}
}
