package framerate

import (
	"math"
	"testing"
)

// gremlins_kill_subflux_u18_test.go — tests that pin surviving mutation-testing
// mutants in framerate.go (unit subflux-u18). Tests only; no production edits.

const gk_subflux_u18_tol = 1e-9

// gk_subflux_u18_approx fails when got is not within tol of want.
func gk_subflux_u18_approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (tol %v)", name, got, want, tol)
	}
}

// gk_subflux_u18_findRatio returns the RatioPair matching (from,to), if present.
func gk_subflux_u18_findRatio(pairs []RatioPair, from, to float64) (RatioPair, bool) {
	for _, p := range pairs {
		if p.From == from && p.To == to {
			return p, true
		}
	}
	return RatioPair{}, false
}

// Two points on the perfect line y = 0.01*x + 5.
//
// Exactly 2 points is the n<2 guard boundary: the original (`n < 2` -> false)
// computes a real fit, while CONDITIONALS_BOUNDARY (`n <= 2`) and
// CONDITIONALS_NEGATION (`n >= 2`) both early-return (0,0,0) at line 18.
// The non-zero slope/intercept also pin the denominator subtraction (line 30,
// `n*sumX2 - sumX*sumX`) and the intercept division (line 36, `/ n`).
func TestLinearRegression_gk_subflux_u18_twoPointPerfectLine(t *testing.T) {
	points := []DriftPoint{
		{TimeMs: 0, DriftMs: 5},
		{TimeMs: 1000, DriftMs: 15},
	}

	slope, intercept, r2 := LinearRegression(points)

	// denom = 2*1e6 - 1e6 = 1e6 ; slope = (2*15000 - 1000*20)/1e6 = 10000/1e6 = 0.01
	// (line 18 mutants -> 0 ; line 30 `-`->`+` -> denom 3e6 -> slope 0.003333)
	gk_subflux_u18_approx(t, "LinearRegression(2pt).slope", slope, 0.01, gk_subflux_u18_tol)
	// intercept = (sumY - slope*sumX)/n = (20 - 10)/2 = 5
	// (line 36 `/ n` -> `* n` -> (20-10)*2 = 20 ; line 18 mutants -> 0)
	gk_subflux_u18_approx(t, "LinearRegression(2pt).intercept", intercept, 5.0, gk_subflux_u18_tol)
	// perfect fit -> r2 = 1
	gk_subflux_u18_approx(t, "LinearRegression(2pt).r2", r2, 1.0, gk_subflux_u18_tol)
}

// Fewer than 2 points must return the zero result (documents the line 18 guard).
func TestLinearRegression_gk_subflux_u18_tooFewPoints(t *testing.T) {
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

// Three points that do NOT lie on a line, so the fit is imperfect (ssRes != 0)
// and R² genuinely depends on meanY, the residual computation, and the
// ssRes/ssTot ratio.
//
//	slope     = (3*52000 - 3000*30)/(3*5e6 - 9e6) = 66000/6e6 = 0.011
//	intercept = (30 - 0.011*3000)/3 = (30 - 33)/3 = -1
//	meanY     = 30/3 = 10
//	predicted = -1, 10, 21 ; residuals = 1, -2, 1 ; ssRes = 1+4+1 = 6
//	ssTot     = (0-10)^2 + (8-10)^2 + (22-10)^2 = 100+4+144 = 248
//	r2        = 1 - 6/248 = 0.9758064516129032
//
// Pins line 38 (meanY `/ n` -> r2 0.99969), line 41 (predicted `+ intercept`
// -> r2 0.92742), line 42:36 (`*`->`/` collapses ssRes terms to 1 -> r2
// 0.98790), line 43:32 (`*`->`/` collapses ssTot terms to 1 -> r2 -1.0),
// line 45 (`==`->`!=` takes the if-branch -> r2 1.0 because ssTot != 0),
// and line 48 (`-`->`+` -> r2 1.02419 ; `/`->`*` -> r2 1 - 6*248 = -1487).
func TestLinearRegression_gk_subflux_u18_threePointImperfectFit(t *testing.T) {
	points := []DriftPoint{
		{TimeMs: 0, DriftMs: 0},
		{TimeMs: 1000, DriftMs: 8},
		{TimeMs: 2000, DriftMs: 22},
	}

	slope, intercept, r2 := LinearRegression(points)

	gk_subflux_u18_approx(t, "LinearRegression(3pt).slope", slope, 0.011, gk_subflux_u18_tol)
	gk_subflux_u18_approx(t, "LinearRegression(3pt).intercept", intercept, -1.0, gk_subflux_u18_tol)
	gk_subflux_u18_approx(t, "LinearRegression(3pt).r2", r2, 0.9758064516129032, gk_subflux_u18_tol)
}

// buildKnownRatios must emit every ORDERED pair of DISTINCT framerates and skip
// self-pairs (line 79, `from == to` -> continue). 9 framerates => 9*9 - 9 = 72
// pairs, none with From == To. The NEGATION mutant (`from != to`) keeps only the
// 9 self-pairs instead, so the count and the no-self-pair invariant both break.
func TestBuildKnownRatios_gk_subflux_u18_skipsSelfPairs(t *testing.T) {
	pairs := buildKnownRatios()

	if got, want := len(pairs), 72; got != want {
		t.Fatalf("len(buildKnownRatios()) = %d, want %d", got, want)
	}
	for _, p := range pairs {
		if p.From == p.To {
			t.Errorf("buildKnownRatios() contains self-pair From == To == %v, want none", p.From)
		}
	}
	if _, ok := gk_subflux_u18_findRatio(pairs, 24.0, 25.0); !ok {
		t.Errorf("buildKnownRatios() missing cross-pair 24->25")
	}
}

// The conversion ratio is To/From (division at line 85), not To*From. Anchored
// on two pairs whose quotient is 2.0 but whose product is far larger.
func TestBuildKnownRatios_gk_subflux_u18_ratioIsToOverFrom(t *testing.T) {
	pairs := buildKnownRatios()

	p, ok := gk_subflux_u18_findRatio(pairs, 24.0, 48.0)
	if !ok {
		t.Fatalf("buildKnownRatios() missing pair 24->48")
	}
	// 48/24 = 2.0 ; mutated 48*24 = 1152
	gk_subflux_u18_approx(t, "ratio(24->48)", p.Ratio, 2.0, gk_subflux_u18_tol)

	p2, ok := gk_subflux_u18_findRatio(pairs, 25.0, 50.0)
	if !ok {
		t.Fatalf("buildKnownRatios() missing pair 25->50")
	}
	// 50/25 = 2.0 ; mutated 50*25 = 1250
	gk_subflux_u18_approx(t, "ratio(25->50)", p2.Ratio, 2.0, gk_subflux_u18_tol)
}
