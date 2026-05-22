package crosslang

import (
	"testing"

	"pgregory.net/rapid"
)

func TestEditDistance_metric_properties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.String().Draw(t, "a")
		b := rapid.String().Draw(t, "b")
		c := rapid.String().Draw(t, "c")

		dAB := EditDistance(a, b)
		dBA := EditDistance(b, a)
		dAA := EditDistance(a, a)
		dAC := EditDistance(a, c)
		dBC := EditDistance(b, c)

		// Non-negativity.
		if dAB < 0 {
			t.Fatalf("d(%q, %q) = %d < 0", a, b, dAB)
		}
		// Identity.
		if dAA != 0 {
			t.Fatalf("d(%q, %q) = %d, want 0", a, a, dAA)
		}
		// Symmetry.
		if dAB != dBA {
			t.Fatalf("d(%q, %q) = %d != d(%q, %q) = %d", a, b, dAB, b, a, dBA)
		}
		// Triangle inequality.
		if dAC > dAB+dBC {
			t.Fatalf("triangle inequality violated: d(%q,%q)=%d > d(%q,%q)=%d + d(%q,%q)=%d",
				a, c, dAC, a, b, dAB, b, c, dBC)
		}
		// Upper bound: d(a,b) <= max(len(a), len(b)).
		maxLen := len([]rune(a))
		if bl := len([]rune(b)); bl > maxLen {
			maxLen = bl
		}
		if dAB > maxLen {
			t.Fatalf("d(%q, %q) = %d > max(len(a), len(b)) = %d", a, b, dAB, maxLen)
		}
	})
}
