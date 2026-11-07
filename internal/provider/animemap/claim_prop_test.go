package animemap_test

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider/animemap"
	"pgregory.net/rapid"
)

// labelWord reports whether name carries, in any case, a whole token one of
// the labels is: their case is what tells "X OP 02 X" (a credit) from "X op
// 02 X" (episode 2), so the property leaves such names to TestReadClaim.
func labelWord(name string) bool {
	for _, tok := range strings.FieldsFunc(strings.ToUpper(name), func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		for _, l := range []string{"SPNCOP", "SPNCED", "NCOP", "NCED", "OP", "ED", "PV", "CM", "TRAILER"} {
			if rest, ok := strings.CutPrefix(tok, l); ok && strings.TrimLeft(rest, "0123456789") == "" {
				return true
			}
		}
	}
	return false
}

// TestReadClaim_case_insensitive: a claim of step 1 or 2 keeps its step and
// numbers whatever the name's case.
func TestReadClaim_case_insensitive(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[A-Za-z0-9 .-]{0,40}`).Draw(t, "name")
		if labelWord(name) {
			t.Skip("carries a label word")
		}
		lower := animemap.ReadEntry([]string{strings.ToLower(name)}, true)[0]
		upper := animemap.ReadEntry([]string{strings.ToUpper(name)}, true)[0]
		if (lower.Step == animemap.ByMarker || lower.Step == animemap.ByAbsolute || upper.Step == animemap.ByMarker || upper.Step == animemap.ByAbsolute) && !sameClaim(lower, upper) {
			t.Fatalf("%q: lower %+v, upper %+v", name, lower, upper)
		}
	})
}
