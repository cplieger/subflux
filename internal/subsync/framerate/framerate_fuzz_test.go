package framerate

import (
	"math"
	"testing"
)

func FuzzLinearRegression(f *testing.F) {
	f.Add(0.0, 0.0, 1000.0, 10.0, 2000.0, 20.0)
	f.Add(0.0, 5.0, 1000.0, 5.0, 2000.0, 5.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0, 0.0)

	f.Fuzz(func(t *testing.T, t1, d1, t2, d2, t3, d3 float64) {
		points := []DriftPoint{
			{TimeMs: t1, DriftMs: d1},
			{TimeMs: t2, DriftMs: d2},
			{TimeMs: t3, DriftMs: d3},
		}
		slope, intercept, r2 := LinearRegression(points)

		if math.IsNaN(slope) || math.IsNaN(intercept) {
			t.Fatalf("LinearRegression returned NaN: slope=%v intercept=%v", slope, intercept)
		}
		if !math.IsNaN(r2) && (r2 < -0.01 || r2 > 1.01) {
			t.Fatalf("R² out of range: %v", r2)
		}
	})
}
