package txutil

import (
	"strings"
	"testing"
)

// FuzzLikeEscaperNoRawWildcards verifies that LikeEscaper output never
// contains unescaped SQL LIKE wildcards (injection prevention invariant).
// An unescaped wildcard is a % or _ NOT preceded by \.
func FuzzLikeEscaperNoRawWildcards(f *testing.F) {
	f.Add("normal")
	f.Add("100%")
	f.Add("under_score")
	f.Add(`back\slash`)
	f.Add("%_%\\%")
	f.Add("")
	f.Add("\x00")

	f.Fuzz(func(t *testing.T, input string) {
		escaped := LikeEscaper.Replace(input)

		// Walk the escaped string checking for unescaped wildcards.
		for i := range len(escaped) {
			c := escaped[i]
			if c == '%' || c == '_' {
				// Must be preceded by backslash.
				if i == 0 || escaped[i-1] != '\\' {
					t.Fatalf("unescaped %q at index %d in %q (input: %q)",
						string(c), i, escaped, input)
				}
			}
		}
	})
}

// FuzzAppendPrefixFilterStructural verifies that AppendPrefixFilter:
// - returns query unchanged when prefix is empty
// - appends a LIKE clause when prefix is non-empty
// - appended args include the escaped prefix with trailing %
func FuzzAppendPrefixFilterStructural(f *testing.F) {
	f.Add("SELECT * FROM t WHERE x = ?", "tvdb-123-")
	f.Add("SELECT 1", "")
	f.Add("Q", "100%_done")
	f.Add("Q", `back\slash`)

	f.Fuzz(func(t *testing.T, baseQuery, prefix string) {
		baseArgs := []any{"existing"}
		q, args := AppendPrefixFilter(baseQuery, baseArgs, prefix, "col")

		if prefix == "" {
			// No modification.
			if q != baseQuery {
				t.Fatalf("empty prefix modified query: %q -> %q", baseQuery, q)
			}
			if len(args) != len(baseArgs) {
				t.Fatalf("empty prefix changed args count: %d -> %d", len(baseArgs), len(args))
			}
			return
		}

		// Non-empty prefix: query must contain LIKE.
		if !strings.Contains(q, "LIKE") {
			t.Fatalf("non-empty prefix but no LIKE in query: %q", q)
		}
		// Args must have one more element.
		if len(args) != len(baseArgs)+1 {
			t.Fatalf("expected %d args, got %d", len(baseArgs)+1, len(args))
		}
		// Last arg must be string ending with %.
		lastArg, ok := args[len(args)-1].(string)
		if !ok {
			t.Fatalf("last arg is not string: %T", args[len(args)-1])
		}
		if !strings.HasSuffix(lastArg, "%") {
			t.Fatalf("last arg does not end with %%: %q", lastArg)
		}
	})
}
