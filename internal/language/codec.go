package language

import (
	"fmt"
	"strconv"
)

// MarshalText is the canonical text, empty for the zero Tag.
func (t Tag) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText decodes wire and config input strictly: empty text is the zero
// Tag, and any text other than a tag's canonical text is refused, naming the
// canonical spelling when it parses. Stored state decodes through Parse.
func (t *Tag) UnmarshalText(b []byte) error {
	s := string(b)
	tag, ok := Parse(s)
	switch {
	case !ok:
		return fmt.Errorf("language: %s is not a language tag", strconv.Quote(s))
	case tag.String() != s:
		return fmt.Errorf("language: %s is not canonical; use %s", strconv.Quote(s), strconv.Quote(tag.String()))
	}
	*t = tag
	return nil
}

var (
	variantNames   = []string{"standard", "hi", "forced"}
	closenessNames = []string{"identical", "same_language", "other_region", "intelligible", "other_script", "unrelated"}
	classNames     = []string{"exact", "substitute", "manual_only", "unrelated"}
)

// MarshalText is standard, hi or forced.
func (v Variant) MarshalText() ([]byte, error) { return marshalEnum(variantNames, "variant", v) }

// UnmarshalText refuses every spelling but MarshalText's.
func (v *Variant) UnmarshalText(b []byte) error { return unmarshalEnum(variantNames, "variant", b, v) }

// MarshalText is identical, same_language, other_region, intelligible,
// other_script or unrelated.
func (c Closeness) MarshalText() ([]byte, error) { return marshalEnum(closenessNames, "closeness", c) }

// UnmarshalText refuses every spelling but MarshalText's.
func (c *Closeness) UnmarshalText(b []byte) error {
	return unmarshalEnum(closenessNames, "closeness", b, c)
}

// MarshalText is exact, substitute, manual_only or unrelated.
func (c Class) MarshalText() ([]byte, error) { return marshalEnum(classNames, "class", c) }

// UnmarshalText refuses every spelling but MarshalText's.
func (c *Class) UnmarshalText(b []byte) error { return unmarshalEnum(classNames, "class", b, c) }

func marshalEnum[E ~uint8](names []string, kind string, v E) ([]byte, error) {
	if v == 0 || int(v) > len(names) {
		return nil, fmt.Errorf("language: invalid %s %d", kind, v)
	}
	return []byte(names[v-1]), nil
}

func unmarshalEnum[E ~uint8](names []string, kind string, b []byte, v *E) error {
	for i, n := range names {
		if string(b) == n {
			*v = E(i + 1)
			return nil
		}
	}
	return fmt.Errorf("language: unknown %s %s", kind, strconv.Quote(string(b)))
}
