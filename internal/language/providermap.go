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
	byTag  map[Tag][]string
	tags   []Tag
}

// newProviderMap is MustProviderMap's build, its refusal returned.
func newProviderMap(pairs []WirePair) (ProviderMap, error) {
	m := &providerMap{byWire: map[string]Tag{}, byTag: map[Tag][]string{}, tags: make([]Tag, 0, len(pairs))}
	for i, p := range pairs {
		var problem string
		_, wireSeen := m.byWire[p.Wire]
		switch {
		case p.Wire == "" || len(p.Wire) > maxWireBytes || !utf8.ValidString(p.Wire):
			problem = fmt.Sprintf("label must be 1 to %d bytes of UTF-8", maxWireBytes)
		case p.Tag == (Tag{}):
			problem = "tag is the zero tag"
		case wireSeen:
			problem = "label is listed twice"
		}
		if problem != "" {
			return ProviderMap{}, fmt.Errorf("language: provider map pair %d (%q, %q): %s", i, p.Wire, p.Tag, problem)
		}
		if _, tagSeen := m.byTag[p.Tag]; !tagSeen {
			m.tags = append(m.tags, p.Tag)
		}
		m.byWire[p.Wire] = p.Tag
		m.byTag[p.Tag] = append(m.byTag[p.Tag], p.Wire)
	}
	return ProviderMap{m}, nil
}

// MustProviderMap builds a package-level adapter table's map from pairs. It
// panics on an empty, over-64-byte or invalid UTF-8 label, a zero tag or a
// label listed twice, naming the first bad pair; a tag may carry several
// labels, kept in pair order.
func MustProviderMap(pairs []WirePair) ProviderMap {
	m, err := newProviderMap(pairs)
	if err != nil {
		panic(err)
	}
	return m
}

// Wires is every label the provider files t under, in pair order, in a
// slice the caller owns; nil when t is not mapped.
func (m ProviderMap) Wires(t Tag) []string {
	if m.m == nil {
		return nil
	}
	return slices.Clone(m.m.byTag[t])
}

// Tag is the tag the provider means by wire.
func (m ProviderMap) Tag(wire string) (Tag, bool) {
	if m.m == nil {
		return Tag{}, false
	}
	t, ok := m.m.byWire[wire]
	return t, ok
}

// Tags is every mapped tag once, in first-pair order, in a slice the caller
// owns.
func (m ProviderMap) Tags() []Tag {
	if m.m == nil {
		return nil
	}
	return slices.Clone(m.m.tags)
}
