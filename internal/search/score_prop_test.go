package search

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestNormalizeTitle_properties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.StringMatching(`[A-Za-z0-9._\- :]{0,50}`).Draw(t, "title")
		result := normalizeTitle(input)

		// Result is always lowercase.
		if result != strings.ToLower(result) {
			t.Fatalf("normalizeTitle(%q) = %q is not lowercase", input, result)
		}

		// Result never contains consecutive spaces.
		if strings.Contains(result, "  ") {
			t.Fatalf("normalizeTitle(%q) = %q has consecutive spaces", input, result)
		}

		// Result never contains dots, dashes, underscores, or colons.
		for _, c := range []string{".", "-", "_", ":"} {
			if strings.Contains(result, c) {
				t.Fatalf("normalizeTitle(%q) = %q still contains %q", input, result, c)
			}
		}
	})
}
