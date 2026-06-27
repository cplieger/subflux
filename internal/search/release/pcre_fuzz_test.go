package release

import (
	"strings"
	"testing"
)

// FuzzSplitTopLevelAlternation exercises the manual regex alternation parser
// with arbitrary pattern strings including unbalanced parens and escapes.
// The parser must never panic on out-of-range indexing (trailing backslash,
// unbalanced brackets, deep nesting), and joining the parts with "|" must
// reconstruct the original input exactly (round-trip).
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
		if rejoined := strings.Join(parts, "|"); rejoined != pattern {
			t.Fatalf("roundtrip failed: got %q, want %q", rejoined, pattern)
		}
	})
}
