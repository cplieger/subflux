package manualops

import (
	"strings"
	"testing"
)

// FuzzIsValidLangCode_validImpliesSafe pins the security postcondition of the
// language-code validator: any code it ACCEPTS must be free of every sequence
// the validator exists to reject (path separators, ".." traversal, control
// characters) and within the documented length bound. IsValidLangCode guards a
// value that flows into on-disk subtitle paths, so an accepted "../" or a code
// carrying a NUL/control byte would be a path-traversal or truncation bypass.
//
// This asserts the "accepted implies safe" direction (a postcondition), which
// catches a regression that loosens any single check, rather than merely
// confirming the function does not panic.
func FuzzIsValidLangCode_validImpliesSafe(f *testing.F) {
	f.Add("en")
	f.Add("pt-BR")
	f.Add("")
	f.Add("../../etc/passwd")
	f.Add("a\x00b")
	f.Add("en US")
	f.Add(strings.Repeat("a", MaxLangCodeLen))

	f.Fuzz(func(t *testing.T, lang string) {
		if !IsValidLangCode(lang) {
			return // only accepted codes carry obligations
		}
		if n := len(lang); n == 0 || n > MaxLangCodeLen {
			t.Fatalf("accepted %q but length %d is outside [1,%d]", lang, n, MaxLangCodeLen)
		}
		if strings.ContainsAny(lang, `/\`) {
			t.Fatalf("accepted %q but it contains a path separator", lang)
		}
		if strings.Contains(lang, "..") {
			t.Fatalf("accepted %q but it contains a traversal sequence", lang)
		}
		for _, r := range lang {
			if r < 0x20 {
				t.Fatalf("accepted %q but it contains control char %#x", lang, r)
			}
		}
	})
}
