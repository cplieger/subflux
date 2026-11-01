package language

import (
	"slices"
	"testing"
)

func FuzzParse_roundtrip(f *testing.F) {
	for _, s := range seedInputs {
		f.Add(s)
	}
	for k := range readExceptions {
		f.Add(k)
	}
	for raw, folded := range foldExamples {
		f.Add(raw)
		f.Add(folded)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		tag, ok := Parse(raw)
		if !ok {
			if tag != (Tag{}) {
				t.Fatalf("Parse(%q) refused with a non-zero tag %q", raw, tag)
			}
			return
		}
		if again, ok := Parse(tag.String()); !ok || again != tag {
			t.Fatalf("Parse(%q) = %q; Parse of its text = %q, %v", raw, tag, again, ok)
		}
		name := FilenameTag(tag)
		if !filenameSafe(tag.String()) || !filenameSafe(name) {
			t.Fatalf("Parse(%q) = %q, written %q: not at most 12 bytes of [A-Za-z0-9-]", raw, tag, name)
		}
		if slices.Contains(collidingTags, tag.String()) {
			return
		}
		if back, ok := ReadFilename(name); !ok || back != tag {
			t.Fatalf("ReadFilename(FilenameTag(%q)) = %q, %v", tag, back, ok)
		}
	})
}

func filenameSafe(s string) bool {
	if len(s) > 12 {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}
