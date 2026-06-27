package crosslang

import (
	"strings"
	"testing"
)

// FuzzIsCognateSymmetric asserts the IsCognate relation is symmetric:
// IsCognate(a,b) == IsCognate(b,a).
//
// Bug class: asymmetric cognate detection would cause non-deterministic
// alignment scores depending on which subtitle track was passed first
// (reference vs incorrect), producing inconsistent sync results.
func FuzzIsCognateSymmetric(f *testing.F) {
	f.Add("hello", "hallo")
	f.Add("world", "welt")
	f.Add("", "")
	f.Add("a", "a")
	f.Add("café", "cafe")
	f.Add("\xff\xfe", "ascii")

	f.Fuzz(func(t *testing.T, a, b string) {
		ab := IsCognate(a, b)
		ba := IsCognate(b, a)
		if ab != ba {
			t.Fatalf("not symmetric: IsCognate(%q,%q)=%v, IsCognate(%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}

// FuzzCountSharedFoldBounded asserts CountSharedFold's count never
// exceeds the size of the smaller slice.
//
// Bug class: counter overflow / off-by-one — if shared count exceeded
// min(|a|,|b|) the per-anchor confidence score (computed as count/total)
// could exceed 1.0 and skew downstream alignment thresholds.
func FuzzCountSharedFoldBounded(f *testing.F) {
	f.Add("a,b,c", "a,B,c")
	f.Add("", "")
	f.Add("x", "")
	f.Add("hello,world", "WORLD,HELLO")

	f.Fuzz(func(t *testing.T, csvA, csvB string) {
		a := splitCSV(csvA)
		b := splitCSV(csvB)
		got := CountSharedFold(a, b)
		smaller := min(len(a), len(b))
		if got < 0 || got > smaller {
			t.Fatalf("CountSharedFold(%v,%v)=%d; want in [0,%d]", a, b, got, smaller)
		}
	})
}

// FuzzIsLatinWordPureASCII asserts IsLatinWord agrees with a reference
// predicate for pure-ASCII letter inputs (where the answer is unambiguous).
//
// Bug class: incorrect Unicode classification would let non-Latin scripts
// pass as anchors, polluting cross-language alignment with non-cognate
// words and producing wrong offsets for non-English subtitle pairs.
func FuzzIsLatinWordPureASCII(f *testing.F) {
	f.Add("hello")
	f.Add("World")
	f.Add("")
	f.Add("123")
	f.Add("héllo")
	f.Add("a b")

	f.Fuzz(func(t *testing.T, s string) {
		got := IsLatinWord(s)
		// Reference for pure ASCII letters: at least one rune, all in [a-zA-Z].
		ref := s != ""
		if ref {
			for _, r := range s {
				if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
					ref = false
					break
				}
			}
		}
		// Implication: pure-ASCII-letters → IsLatinWord.
		if ref && !got {
			t.Fatalf("IsLatinWord(%q) = false; pure ASCII letters must qualify", s)
		}
	})
}

// FuzzCountCognatesBounded asserts CountCognates' count is always within
// [0, min(len(a), len(b))]: a greedy-matching or index-overflow bug could
// produce a count exceeding the smaller list, pushing the downstream
// cognates/total confidence ratio above 1.0 and corrupting alignment scoring.
func FuzzCountCognatesBounded(f *testing.F) {
	f.Add("hello,world", "hallo,welt")
	f.Add("", "")
	f.Add("a", "a")
	f.Add("test,case,here", "TEST,CASE")
	f.Add("café,naïve", "cafe,naive")
	f.Add("x,y,z", "a,b,c")

	f.Fuzz(func(t *testing.T, csvA, csvB string) {
		a := splitCSV(csvA)
		b := splitCSV(csvB)
		got := CountCognates(a, b)
		smaller := min(len(a), len(b))
		if got < 0 || got > smaller {
			t.Fatalf("CountCognates(%v, %v) = %d; want in [0, %d]", a, b, got, smaller)
		}
	})
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
