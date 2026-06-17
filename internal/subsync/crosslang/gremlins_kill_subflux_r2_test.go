package crosslang

// Round-2 mutant-killing test for internal/subsync/crosslang.
//
// Kills anchors.go:329:13 INCREMENT_DECREMENT (`freq[key]--` -> `freq[key]++`)
// in countSharedFold. The multiset-intersection count decrements a remaining
// budget so each element of b is matched at most once; incrementing instead
// lets a single b-element be matched repeatedly, over-counting duplicates in a.

import "testing"

func TestGkSubfluxR2_CountSharedFoldConsumesEachMatch(t *testing.T) {
	// b has one "x"; a has two. Only one match is possible (the budget is
	// consumed). The `++` mutant grows the budget and counts 2.
	if got := CountSharedFold([]string{"x", "x"}, []string{"x"}); got != 1 {
		t.Errorf("CountSharedFold([x x], [x]) = %d, want 1", got)
	}
}
