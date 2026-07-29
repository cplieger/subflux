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

		dAB := editDistance(a, b)
		dBA := editDistance(b, a)
		dAA := editDistance(a, a)
		dAC := editDistance(a, c)
		dBC := editDistance(b, c)

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

// The properties below were promoted from internal/subsync/anchors_test.go
// along with the tables in anchors_test.go. The four editDistance metric
// properties that came with them (symmetry, identity, upper bound, triangle
// inequality) are not repeated: TestEditDistance_metric_properties above
// already asserts all four over the same rapid.String() generators.

// TestExtractAnchors_never_panics asserts the anchor extractor is total over
// arbitrary cue text and that its Numbers feature only ever carries digits —
// the invariant countShared relies on when it compares number lists across
// languages.
func TestExtractAnchors_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "input")
		a := extractAnchors(input)
		if a.WordCount < 0 {
			t.Errorf("extractAnchors(%q).WordCount = %d, want >= 0", input, a.WordCount)
		}
		if a.CharLen < 0 {
			t.Errorf("extractAnchors(%q).CharLen = %d, want >= 0", input, a.CharLen)
		}
		for i, n := range a.Numbers {
			for _, r := range n {
				if r < '0' || r > '9' {
					t.Errorf("extractAnchors(%q).Numbers[%d] = %q contains non-digit", input, i, n)
				}
			}
		}
	})
}

// TestIsCognate_reflexive asserts every word of cognate-eligible length is its
// own cognate. FuzzIsCognateSymmetric covers symmetry from committed seeds;
// reflexivity has no fuzz twin.
func TestIsCognate_reflexive(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "word")
		if !isCognate(s, s) {
			t.Errorf("isCognate(%q, %q) = false, want true (reflexive)", s, s)
		}
	})
}

// TestIsCognate_symmetric is the generative twin of FuzzIsCognateSymmetric:
// the fuzz target only re-explores its committed seeds on each run, so this
// property is the invariant net that draws fresh word pairs every run.
func TestIsCognate_symmetric(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "a")
		b := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "b")
		ab := isCognate(a, b)
		ba := isCognate(b, a)
		if ab != ba {
			t.Errorf("isCognate(%q,%q)=%v != isCognate(%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}

// TestCountShared_upper_bound bounds the exact-match counter by the smaller
// list. countShared has no fuzz target, so this is its only bound guard: a
// count above min(|a|,|b|) would push the anchor score above 1.0.
func TestCountShared_upper_bound(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.SliceOf(rapid.StringMatching(`[a-z]{1,5}`)).Draw(t, "a")
		b := rapid.SliceOf(rapid.StringMatching(`[a-z]{1,5}`)).Draw(t, "b")
		got := countShared(a, b)
		upper := min(len(a), len(b))
		if got > upper {
			t.Errorf("countShared(%v, %v) = %d, want <= min(%d, %d) = %d",
				a, b, got, len(a), len(b), upper)
		}
	})
}

// TestCountSharedFold_upper_bound is the generative twin of
// FuzzCountSharedFoldBounded, drawing fresh mixed-case lists each run.
func TestCountSharedFold_upper_bound(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.SliceOf(rapid.StringMatching(`[a-zA-Z]{1,5}`)).Draw(t, "a")
		b := rapid.SliceOf(rapid.StringMatching(`[a-zA-Z]{1,5}`)).Draw(t, "b")
		got := countSharedFold(a, b)
		upper := min(len(a), len(b))
		if got > upper {
			t.Errorf("countSharedFold(%v, %v) = %d, want <= min(%d, %d) = %d",
				a, b, got, len(a), len(b), upper)
		}
	})
}
