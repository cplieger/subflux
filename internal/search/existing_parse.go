package search

import (
	"strings"

	"github.com/cplieger/subflux/internal/api"
)

// --- External subtitle path parsing ---

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
		case string(api.VariantHI), api.VariantAliasSDH:
			sub.HI = true
		case string(api.VariantForced), api.VariantAliasForeign:
			sub.Forced = true
		}
	}

	return sub
}

// globEscape escapes glob metacharacters in s so filepath.Glob treats them
// as literal characters. On Linux (where this runs), backslash-escaping works.
func globEscape(s string) string {
	if !strings.ContainsAny(s, `*?[\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	// Iterate over bytes, not runes: paths on Linux are arbitrary byte
	// strings that may not be valid UTF-8, and ranging by rune would
	// rewrite each invalid byte as U+FFFD — corrupting the pattern so it
	// no longer matches the real filename. The glob metacharacters are all
	// ASCII, so byte-wise escaping is both correct and lossless.
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '*', '?', '[', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// --- Ignored codec detection from config ---
