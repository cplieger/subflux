package search

import (
	"strings"

	"github.com/cplieger/subflux/internal/subflux"
)

// parseExternalSubPath extracts language and variant flags from an external
// subtitle filename. The middle segment between base+"." and ext is split on
// dots: the first part is the language code, subsequent parts are flag tags
// (hi/sdh → HI, forced/foreign → Forced).
func parseExternalSubPath(path, base, ext string) externalSub {
	middle := strings.TrimPrefix(path, base+".")
	middle = strings.TrimSuffix(middle, ext)

	parts := strings.Split(middle, ".")

	sub := externalSub{
		Path: path,
		Lang: parts[0],
	}

	for _, p := range parts[1:] {
		switch strings.ToLower(p) {
		case string(subflux.VariantHI), subflux.VariantAliasSDH:
			sub.HI = true
		case string(subflux.VariantForced), subflux.VariantAliasForeign:
			sub.Forced = true
		}
	}

	return sub
}

// globEscape escapes glob metacharacters in s so filepath.Glob treats them
// as literal characters.
func globEscape(s string) string {
	if !strings.ContainsAny(s, `*?[\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	// Byte-wise, not rune-wise: paths may not be valid UTF-8, and ranging
	// by rune would rewrite an invalid byte as U+FFFD, corrupting the
	// pattern. The glob metacharacters are all ASCII.
	for i := range len(s) {
		c := s[i]
		switch c {
		case '*', '?', '[', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}
