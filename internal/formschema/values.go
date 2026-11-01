package formschema

import (
	"slices"
	"time"
)

// Value is one decoded field.
type Value struct {
	text string
	list []string
	i    int64
	d    time.Duration
	kind Kind
	b    bool
}

// Values holds a section's decoded fields by key, defaults included; an
// absent key without a default has no entry.
type Values map[string]Value

func (vs Values) get(key string, kinds ...Kind) (Value, bool) {
	v, ok := vs[key]
	if !ok {
		return Value{}, false
	}
	if !slices.Contains(kinds, v.kind) {
		return Value{}, false
	}
	return v, true
}

// String answers a String or Enum field's text.
func (vs Values) String(key string) (string, bool) {
	v, ok := vs.get(key, String, Enum)
	return v.text, ok
}

// Secret answers a Secret field's text.
func (vs Values) Secret(key string) (string, bool) {
	v, ok := vs.get(key, Secret)
	return v.text, ok
}

// Bool answers a Bool field.
func (vs Values) Bool(key string) (value, ok bool) {
	v, ok := vs.get(key, Bool)
	return v.b, ok
}

// Int answers an Int field.
func (vs Values) Int(key string) (int64, bool) {
	v, ok := vs.get(key, Int)
	return v.i, ok
}

// Duration answers a DurationMs field.
func (vs Values) Duration(key string) (time.Duration, bool) {
	v, ok := vs.get(key, DurationMs)
	return v.d, ok
}

// StringList answers a copy of a StringList field's elements.
func (vs Values) StringList(key string) ([]string, bool) {
	v, ok := vs.get(key, StringList)
	if !ok {
		return nil, false
	}
	return append([]string(nil), v.list...), true
}
