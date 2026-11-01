package language

import (
	"fmt"
	"slices"
	"unicode/utf8"
)

// maxWireBytes bounds a provider's language label, far above any real one.
const maxWireBytes = 64

// WirePair is one provider language label and the tag it means.
type WirePair struct {
	Wire string
	Tag  Tag
}

// ProviderMap is one provider's two-way table between its language labels
// and tags. Lookups are exact: normalizing a label is the adapter's job. The
// zero ProviderMap is empty.
type ProviderMap struct{ m *providerMap }

type providerMap struct {
	byWire map[string]Tag
	byTag  map[Tag]string
	tags   []Tag
}

// NewProviderMap builds a map from pairs, refusing an empty, over-64-byte or
// invalid UTF-8 label, a zero tag, and a label or tag listed twice; the error
// names the first bad pair.
func NewProviderMap(pairs []WirePair) (ProviderMap, error) {
	m := &providerMap{byWire: map[string]Tag{}, byTag: map[Tag]string{}, tags: make([]Tag, 0, len(pairs))}
	for i, p := range pairs {
		var problem string
		_, wireSeen := m.byWire[p.Wire]
		_, tagSeen := m.byTag[p.Tag]
		switch {
		case p.Wire == "" || len(p.Wire) > maxWireBytes || !utf8.ValidString(p.Wire):
			problem = fmt.Sprintf("label must be 1 to %d bytes of UTF-8", maxWireBytes)
		case p.Tag == (Tag{}):
			problem = "tag is the zero tag"
		case wireSeen:
			problem = "label is listed twice"
		case tagSeen:
			problem = "tag is listed twice"
		}
		if problem != "" {
			return ProviderMap{}, fmt.Errorf("language: provider map pair %d (%q, %q): %s", i, p.Wire, p.Tag, problem)
		}
		m.byWire[p.Wire], m.byTag[p.Tag] = p.Tag, p.Wire
		m.tags = append(m.tags, p.Tag)
	}
	return ProviderMap{m}, nil
}

// MustProviderMap is NewProviderMap for package-level adapter tables; it
// panics on NewProviderMap's error.
func MustProviderMap(pairs []WirePair) ProviderMap {
	m, err := NewProviderMap(pairs)
	if err != nil {
		panic(err)
	}
	return m
}

// Wire is the provider's label for t.
func (m ProviderMap) Wire(t Tag) (string, bool) {
	if m.m == nil {
		return "", false
	}
	w, ok := m.m.byTag[t]
	return w, ok
}

// Tag is the tag the provider means by wire.
func (m ProviderMap) Tag(wire string) (Tag, bool) {
	if m.m == nil {
		return Tag{}, false
	}
	t, ok := m.m.byWire[wire]
	return t, ok
}

// Tags is every mapped tag in pair order, in a slice the caller owns.
func (m ProviderMap) Tags() []Tag {
	if m.m == nil {
		return nil
	}
	return slices.Clone(m.m.tags)
}
