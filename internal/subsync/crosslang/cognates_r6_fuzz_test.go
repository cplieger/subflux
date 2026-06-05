package crosslang

import (
	"strings"
	"testing"
)

// FuzzCountCognatesBounded verifies that CountCognates result is always
// in [0, min(len(a), len(b))] and that exact matches are always counted.
//
// Bug class: bitmap index overflow or greedy matching bug could produce
// counts exceeding min(|a|,|b|), causing the downstream confidence ratio
// (cognates/total) to exceed 1.0 and corrupt alignment scoring.
func FuzzCountCognatesBounded(f *testing.F) {
	f.Add("hello,world", "hallo,welt")
	f.Add("", "")
	f.Add("a", "a")
	f.Add("test,case,here", "TEST,CASE")
	f.Add("café,naïve", "cafe,naive")
	f.Add("x,y,z", "a,b,c")

	f.Fuzz(func(t *testing.T, csvA, csvB string) {
		a := splitWords(csvA)
		b := splitWords(csvB)
		got := CountCognates(a, b)
		smaller := min(len(a), len(b))
		if got < 0 || got > smaller {
			t.Fatalf("CountCognates(%v, %v) = %d; want in [0, %d]", a, b, got, smaller)
		}
	})
}

func splitWords(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
