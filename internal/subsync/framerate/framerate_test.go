package framerate

import (
	"math"
	"testing"
)

const tol = 1e-9

// assertApprox fails when got is not within tol of want.
// approxEq reports whether got is within tol of want. It returns the
// comparison rather than performing the assertion, so each call site keeps its
// own failure message and reads as the thing it is checking.
func approxEq(got, want float64) bool {
	return math.Abs(got-want) <= tol
}

// findRatio returns the RatioPair matching (from, to), if present.
func findRatio(pairs []RatioPair, from, to float64) (RatioPair, bool) {
	for _, p := range pairs {
		if p.From == from && p.To == to {
			return p, true
		}
	}
	return RatioPair{}, false
}

// Exactly two points on the perfect line y = 0.01*x + 5. Two points is the
// boundary of the "fewer than 2 points" guard — it must compute a real fit
// rather than early-return — and the non-zero slope/intercept pin the
// denominator subtraction and the intercept division.
//
//	denom     = 2*1e6 - 1e6 = 1e6
//	slope     = (2*15000 - 1000*20)/1e6 = 0.01
//	intercept = (sumY - slope*sumX)/n = (20 - 10)/2 = 5
//	perfect fit -> r2 = 1
func TestLinearRegression_twoPointPerfectLine(t *testing.T) {
	points := []DriftPoint{
		{TimeMs: 0, DriftMs: 5},
		{TimeMs: 1000, DriftMs: 15},
	}

	slope, intercept, r2 := LinearRegression(points)

	if !approxEq(slope, 0.01) {
		t.Errorf("slope = %v, want 0.01 (tol %v)", slope, tol)
	}
	if !approxEq(intercept, 5.0) {
		t.Errorf("intercept = %v, want 5.0 (tol %v)", intercept, tol)
	}
	if !approxEq(r2, 1.0) {
		t.Errorf("r2 = %v, want 1.0 (tol %v)", r2, tol)
	}
}

// Fewer than two points returns the zero result.
func TestLinearRegression_tooFewPoints(t *testing.T) {
	cases := []struct {
		name   string
		points []DriftPoint
	}{
		{"zero", nil},
		{"one", []DriftPoint{{TimeMs: 100, DriftMs: 5}}},
	}
	for _, tc := range cases {
		slope, intercept, r2 := LinearRegression(tc.points)
		if slope != 0 || intercept != 0 || r2 != 0 {
			t.Errorf("LinearRegression(%s) = (%v, %v, %v), want (0, 0, 0)",
				tc.name, slope, intercept, r2)
		}
	}
}

// Three points that do not lie on a line, so the fit is imperfect (ssRes != 0)
// and R² genuinely depends on meanY, the residual computation, and the
// ssRes/ssTot ratio.
//
//	slope     = (3*52000 - 3000*30)/(3*5e6 - 9e6) = 66000/6e6 = 0.011
//	intercept = (30 - 0.011*3000)/3 = -1
//	meanY     = 10 ; predicted = -1, 10, 21 ; residuals = 1, -2, 1 ; ssRes = 6
//	ssTot     = 100 + 4 + 144 = 248
//	r2        = 1 - 6/248 = 0.9758064516129032
func TestLinearRegression_threePointImperfectFit(t *testing.T) {
	points := []DriftPoint{
		{TimeMs: 0, DriftMs: 0},
		{TimeMs: 1000, DriftMs: 8},
		{TimeMs: 2000, DriftMs: 22},
	}

	slope, intercept, r2 := LinearRegression(points)

	if !approxEq(slope, 0.011) {
		t.Errorf("slope = %v, want 0.011 (tol %v)", slope, tol)
	}
	if !approxEq(intercept, -1.0) {
		t.Errorf("intercept = %v, want -1.0 (tol %v)", intercept, tol)
	}
	if !approxEq(r2, 0.9758064516129032) {
		t.Errorf("r2 = %v, want 0.9758064516129032 (tol %v)", r2, tol)
	}
}

// buildKnownRatios emits every ordered pair of distinct framerates and skips
// self-pairs: 9 framerates => 9*9 - 9 = 72 pairs, none with From == To.
func TestBuildKnownRatios_skipsSelfPairs(t *testing.T) {
	pairs := buildKnownRatios()

	if got, want := len(pairs), 72; got != want {
		t.Fatalf("len(buildKnownRatios()) = %d, want %d", got, want)
	}
	for _, p := range pairs {
		if p.From == p.To {
			t.Errorf("buildKnownRatios() contains self-pair From == To == %v, want none", p.From)
		}
	}
	if _, ok := findRatio(pairs, 24.0, 25.0); !ok {
		t.Errorf("buildKnownRatios() missing cross-pair 24->25")
	}
}

// The conversion ratio is To/From, not To*From. Anchored on pairs whose
// quotient is 2.0 but whose product is far larger.
func TestBuildKnownRatios_ratioIsToOverFrom(t *testing.T) {
	pairs := buildKnownRatios()

	p, ok := findRatio(pairs, 24.0, 48.0)
	if !ok {
		t.Fatalf("buildKnownRatios() missing pair 24->48")
	}
	// 48/24 = 2.0, not 48*24 = 1152.
	if !approxEq(p.Ratio, 2.0) {
		t.Errorf("ratio(24->48) = %v, want 2.0 (tol %v)", p.Ratio, tol)
	}

	p2, ok := findRatio(pairs, 25.0, 50.0)
	if !ok {
		t.Fatalf("buildKnownRatios() missing pair 25->50")
	}
	// 50/25 = 2.0, not 50*25 = 1250.
	if !approxEq(p2.Ratio, 2.0) {
		t.Errorf("ratio(25->50) = %v, want 2.0 (tol %v)", p2.Ratio, tol)
	}
}
