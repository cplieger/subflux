package mediasource

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cplieger/runesafe/v3"
)

// validPath is the path rule every reported and configured path meets:
// absolute, clean, at most maxPathBytes, valid UTF-8 and free of every rune
// runesafe.IsUnsafeSingleLine reports. A path breaking it is refused, never
// rewritten, since a rewritten path names another file.
func validPath(p string) bool {
	if len(p) > maxPathBytes || !filepath.IsAbs(p) || filepath.Clean(p) != p || !utf8.ValidString(p) {
		return false
	}
	return !strings.ContainsFunc(p, runesafe.IsUnsafeSingleLine)
}

// validNativeID reports whether id is 1 to maxNativeID bytes of
// [A-Za-z0-9._:-].
func validNativeID(id NativeID) bool {
	if id == "" || len(id) > maxNativeID {
		return false
	}
	for i := range len(id) {
		switch c := id[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}
