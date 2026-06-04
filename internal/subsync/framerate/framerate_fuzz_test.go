package framerate

import (
	"math"
	"testing"
)

func FuzzLinearRegression(f *testing.F) {
	f.Add(0.0, 0.0, 1000.0, 50.0, 2000.0, 100.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0, 0.0)
	f.Add(1.0, 1.0, 1.0, 1.0, 1.0, 1.0)
	f.Add(-1e9, 1e9, 0.0, 0.0, 1e9, -1e9)

	f.Fuzz(func(t *testing.T, t1, d1, t2, d2, t3, d3 float64) {
		points := []DriftPoint{
			{TimeMs: t1, DriftMs: d1},
			{TimeMs: t2, DriftMs: d2},
			{TimeMs: t3, DriftMs: d3},
		}
		slope, intercept, r2 := LinearRegression(points)
		if math.IsNaN(slope) || math.IsNaN(intercept) {
			// NaN is acceptable for degenerate inputs
			return
		}
		if !math.IsNaN(r2) && !math.IsInf(r2, 0) && (r2 < -1e-9 || r2 > 1.0+1e-9) {
			t.Errorf("r2 out of [0,1] range: %v", r2)
		}
	})
}
