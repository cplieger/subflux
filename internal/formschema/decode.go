package formschema

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const msgRequired = "required"

// Decode reads n, a section's mapping (nil when the section is absent), by
// the schema. Every scalar is read from its text by its field's Kind,
// whatever its YAML tag, and a null is absent. Unknown and repeated keys,
// anchors and aliases, and values failing their field's rules are field
// errors at section-relative paths. Each scalar it reads for a non-Secret
// field is retagged PlainTag in n, whatever errors other fields have.
func (s Schema) Decode(n *yaml.Node) (Values, []FieldError) {
	present, errs := s.collect(n)
	vals := Values{}
	for i := range s.Fields {
		f := &s.Fields[i]
		v, ok := present[f.Key]
		if !ok || IsNull(v) {
			errs = append(errs, s.absent(f, vals)...)
			continue
		}
		if sharesNodes(v) {
			errs = append(errs, FieldError{f.Key, "YAML anchors and aliases are not supported; repeat the value or use ${SUBFLUX_...}"})
			continue
		}
		val, ferrs := decodeField(f, v)
		if f.Kind != Secret {
			markScalars(f.Kind, v)
		}
		if len(ferrs) > 0 {
			errs = append(errs, ferrs...)
			continue
		}
		vals[f.Key] = val
	}
	return vals, errs
}

// collect indexes n's keys, refusing a non-mapping, unknown keys and repeats.
func (s Schema) collect(n *yaml.Node) (map[string]*yaml.Node, []FieldError) {
	present := map[string]*yaml.Node{}
	if n == nil || IsNull(n) {
		return present, nil
	}
	if n.Kind != yaml.MappingNode {
		return present, []FieldError{{"", "must be a mapping"}}
	}
	var errs []FieldError
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		switch {
		case k.Kind != yaml.ScalarNode:
			errs = append(errs, FieldError{"", "keys must be plain names"})
		case !slices.ContainsFunc(s.Fields, func(f Field) bool { return f.Key == k.Value }):
			errs = append(errs, FieldError{k.Value, "unknown key"})
		case present[k.Value] != nil:
			errs = append(errs, FieldError{k.Value, "key is set twice"})
		default:
			present[k.Value] = n.Content[i+1]
		}
	}
	return present, errs
}

// absent applies f's default, or refuses a required key.
func (Schema) absent(f *Field, vals Values) []FieldError {
	if f.Default == "" {
		if f.Required {
			return []FieldError{{f.Key, msgRequired}}
		}
		return nil
	}
	if f.Kind == StringList {
		return []FieldError{{f.Key, "a string list takes no default"}}
	}
	val, msg := parseText(f, f.Default)
	if msg != "" {
		return []FieldError{{f.Key, "invalid default: " + msg}}
	}
	vals[f.Key] = val
	return nil
}

func decodeField(f *Field, v *yaml.Node) (Value, []FieldError) {
	if f.Kind == StringList {
		return decodeList(f, v)
	}
	if v.Kind != yaml.ScalarNode {
		return Value{}, []FieldError{{f.Key, "must be a single value"}}
	}
	val, msg := parseText(f, scalarText(v))
	if msg != "" {
		return Value{}, []FieldError{{f.Key, msg}}
	}
	return val, nil
}

func decodeList(f *Field, v *yaml.Node) (Value, []FieldError) {
	if v.Kind != yaml.SequenceNode {
		return Value{}, []FieldError{{f.Key, "must be a list"}}
	}
	var errs []FieldError
	list := make([]string, 0, len(v.Content))
	for i, e := range v.Content {
		if e.Kind != yaml.ScalarNode || IsNull(e) {
			errs = append(errs, FieldError{fmt.Sprintf("%s[%d]", f.Key, i), "must be a single value"})
			continue
		}
		list = append(list, scalarText(e))
	}
	if f.Required && len(list) == 0 && len(errs) == 0 {
		errs = append(errs, FieldError{f.Key, msgRequired})
	}
	if len(errs) > 0 {
		return Value{}, errs
	}
	return Value{kind: StringList, list: list}, nil
}

// scalarText is a scalar's text as written: a tag never reinterprets it, so
// api_key: 0123 stays 0123.
func scalarText(n *yaml.Node) string { return n.Value }

// parseText reads text by f's kind; a non-empty message refuses it.
func parseText(f *Field, text string) (val Value, msg string) {
	switch f.Kind {
	case String, Secret:
		if f.Required && text == "" {
			return Value{}, msgRequired
		}
		return Value{kind: f.Kind, text: text}, ""
	case Enum:
		if !slices.Contains(f.Enum, text) {
			return Value{}, "must be one of " + strings.Join(f.Enum, ", ")
		}
		return Value{kind: Enum, text: text}, ""
	case Bool:
		if b, ok := ParseBool(text); ok {
			return Value{kind: Bool, b: b}, ""
		}
		return Value{}, "must be true or false"
	case Int:
		i, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return Value{}, "must be a whole number"
		}
		if msg := f.bounds(i); msg != "" {
			return Value{}, msg
		}
		return Value{kind: Int, i: i}, ""
	case DurationMs:
		return parseDuration(f, text)
	}
	return Value{}, "unknown field kind"
}

func parseDuration(f *Field, text string) (val Value, msg string) {
	d, err := time.ParseDuration(text)
	if err != nil {
		return Value{}, "must be a duration such as 500ms, 30s or 2h"
	}
	if d%time.Millisecond != 0 {
		return Value{}, "must be whole milliseconds"
	}
	if msg := f.bounds(d.Milliseconds()); msg != "" {
		return Value{}, msg
	}
	return Value{kind: DurationMs, d: d}, ""
}

func (f *Field) bounds(v int64) string {
	if (f.Min != 0 || f.Max != 0) && (v < f.Min || v > f.Max) {
		if f.Kind == DurationMs {
			return fmt.Sprintf("must be between %s and %s", time.Duration(f.Min)*time.Millisecond, time.Duration(f.Max)*time.Millisecond)
		}
		return fmt.Sprintf("must be between %d and %d", f.Min, f.Max)
	}
	return ""
}

// markScalars marks the scalars a decode of v by kind reads: v itself, or a
// list's scalar elements; a value of the wrong shape stays unmarked.
func markScalars(kind Kind, v *yaml.Node) {
	switch {
	case kind != StringList && v.Kind == yaml.ScalarNode:
		v.Tag = PlainTag
	case kind == StringList && v.Kind == yaml.SequenceNode:
		for _, e := range v.Content {
			if e.Kind == yaml.ScalarNode && !IsNull(e) {
				e.Tag = PlainTag
			}
		}
	}
}

// IsNull reports whether n, which must not be nil, is a YAML 1.2 core-schema
// null read by its text whatever its tag (a plain "", ~, null, Null or NULL),
// so a null retagged PlainTag still reads as absent.
func IsNull(n *yaml.Node) bool {
	if n.Kind != yaml.ScalarNode || n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return false
	}
	switch n.Value {
	case "", "~", "null", "Null", "NULL":
		return true
	}
	return false
}

// ParseBool reads a YAML 1.2 core-schema boolean's text (true, True, TRUE,
// false, False, FALSE); ok is false for every other text.
func ParseBool(text string) (value, ok bool) {
	switch text {
	case "true", "True", "TRUE":
		return true, true
	case "false", "False", "FALSE":
		return false, true
	}
	return false, false
}
