package mediasource

import (
	"cmp"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// Mapper maps a path from the From side of its mappings to the To side.
type Mapper struct{ maps []PathMapping }

// NewMapper sorts a copy of ms by From length, longest first, so the first
// match by component is the longest.
func NewMapper(ms []PathMapping) Mapper {
	sorted := slices.Clone(ms)
	slices.SortStableFunc(sorted, func(a, b PathMapping) int { return cmp.Compare(len(b.From), len(a.From)) })
	return Mapper{maps: sorted}
}

var errPathRule = errors.New("is not an absolute, clean, single-line UTF-8 path of at most 4096 bytes")

// Map maps a reported path under the longest From containing it by
// component; an unmatched path passes through unless a To contains it, which
// is an *AliasedPathError. A path breaking the path rule, given or mapped,
// is refused.
func (m Mapper) Map(reported string) (string, error) {
	if !validPath(reported) {
		return "", errPathRule
	}
	for _, pm := range m.maps {
		if rel, ok := under(pm.From, reported); ok {
			local := filepath.Join(pm.To, rel)
			if !validPath(local) {
				return "", errPathRule
			}
			return local, nil
		}
	}
	for _, pm := range m.maps {
		if _, ok := under(pm.To, reported); ok {
			return "", &AliasedPathError{Path: reported, To: pm.To}
		}
	}
	return reported, nil
}

// under answers p relative to root when root contains p by component, so
// /data contains /data/..foo but not /datax.
func under(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// Injective answers the index of the first mapping whose To range overlaps
// an earlier one's by component, unless the two nest exactly (one From
// under the other, by the same relative path as their To values); -1 when
// none does, so no two From paths map to one To path.
func Injective(ms []PathMapping) int {
	for j := range ms {
		for i := range j {
			if overlaps(ms[i].To, ms[j].To) && !nestsExactly(ms[i], ms[j]) {
				return j
			}
		}
	}
	return -1
}

func overlaps(a, b string) bool {
	_, ab := under(a, b)
	_, ba := under(b, a)
	return ab || ba
}

func nestsExactly(a, b PathMapping) bool {
	if relTo, ok := under(a.To, b.To); ok {
		relFrom, ok := under(a.From, b.From)
		return ok && relFrom == relTo
	}
	relTo, _ := under(b.To, a.To)
	relFrom, ok := under(b.From, a.From)
	return ok && relFrom == relTo
}
