package formschema

import (
	"fmt"
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

// Values holds a section's decoded fields by key: every field of the
// schema, an absent one as its default or its type's zero value. Presence is
// Schema.Required's and Field.Default's to own, never a reader's.
type Values map[string]Value

// get is key's value. Asking for a key the schema lacks, or by a Kind its
// field does not have, is a programming fault and panics.
func (vs Values) get(key string, kinds ...Kind) Value {
	v, ok := vs[key]
	if !ok {
		panic(fmt.Sprintf("formschema: no field %q", key))
	}
	if !slices.Contains(kinds, v.kind) {
		panic(fmt.Sprintf("formschema: field %q is %s, read as %s", key, kindName(v.kind), kindList(kinds)))
	}
	return v
}

func kindName(k Kind) string {
	if b, err := k.MarshalText(); err == nil {
		return string(b)
	}
	return fmt.Sprintf("kind %d", k)
}

func kindList(kinds []Kind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = kindName(k)
	}
	return fmt.Sprint(names)
}

// String answers a String or Enum field's text.
func (vs Values) String(key string) string { return vs.get(key, String, Enum).text }

// Secret answers a Secret field's text.
func (vs Values) Secret(key string) string { return vs.get(key, Secret).text }

// Bool answers a Bool field.
func (vs Values) Bool(key string) bool { return vs.get(key, Bool).b }

// Int answers an Int field.
func (vs Values) Int(key string) int64 { return vs.get(key, Int).i }

// Duration answers a DurationMs field.
func (vs Values) Duration(key string) time.Duration { return vs.get(key, DurationMs).d }

// StringList answers a copy of a StringList field's elements.
func (vs Values) StringList(key string) []string {
	return append([]string(nil), vs.get(key, StringList).list...)
}
