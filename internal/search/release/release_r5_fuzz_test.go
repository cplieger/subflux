package release

import (
	"strings"
	"testing"
)

// FuzzSplitTopLevelAlternation exercises the manual regex alternation parser
// with arbitrary pattern strings including unbalanced parens and escapes.
//
// Bug class: index-out-of-bounds panic on trailing backslash, unbalanced
// brackets, or deeply nested groups; joining results with "|" must reconstruct
// the original input.
func FuzzSplitTopLevelAlternation(f *testing.F) {
	f.Add("a|b|c")
	f.Add("(a|b)|c")
	f.Add("")
	f.Add(`\|escaped`)
	f.Add("[a|b]|c")
	f.Add(`\`)
	f.Add("((()))")

	f.Fuzz(func(t *testing.T, pattern string) {
		parts := SplitTopLevelAlternation(pattern)
		if len(parts) == 0 {
			t.Fatal("result must have at least one element")
		}
		rejoined := strings.Join(parts, "|")
		if rejoined != pattern {
			t.Fatalf("roundtrip failed: got %q, want %q", rejoined, pattern)
		}
	})
}
