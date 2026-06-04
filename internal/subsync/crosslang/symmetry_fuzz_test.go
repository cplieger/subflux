package crosslang

import "testing"

// FuzzEditDistanceSymmetry verifies that EditDistance(a, b) == EditDistance(b, a)
// (symmetry property of the Levenshtein metric).
func FuzzEditDistanceSymmetry(f *testing.F) {
	f.Add("kitten", "sitting")
	f.Add("", "abc")
	f.Add("hello", "hello")
	f.Fuzz(func(t *testing.T, a, b string) {
		dAB := EditDistance(a, b)
		dBA := EditDistance(b, a)
		if dAB != dBA {
			t.Fatalf("EditDistance(%q,%q)=%d != EditDistance(%q,%q)=%d", a, b, dAB, b, a, dBA)
		}
		if dAB < 0 {
			t.Fatalf("negative distance: %d", dAB)
		}
		// Triangle inequality with identity: d(a,a) == 0
		if a == b && dAB != 0 {
			t.Fatalf("EditDistance(%q,%q) should be 0, got %d", a, b, dAB)
		}
	})
}

// FuzzIsCognateSymmetry verifies that IsCognate(a, b) == IsCognate(b, a)
// (the cognate relation is symmetric).
func FuzzIsCognateSymmetry(f *testing.F) {
	f.Add("information", "informazione")
	f.Add("cat", "dog")
	f.Fuzz(func(t *testing.T, a, b string) {
		ab := IsCognate(a, b)
		ba := IsCognate(b, a)
		if ab != ba {
			t.Fatalf("IsCognate(%q,%q)=%v != IsCognate(%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}
