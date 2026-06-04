package framerate

import (
	"math"
	"testing"
)

func FuzzLinearRegression(f *testing.F) {
	f.Add(0.0, 0.0, 1000.0, 10.0, 2000.0, 20.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0, 0.0)
	f.Add(100.0, 5.0, 200.0, 10.0, 300.0, 15.0)
	f.Add(1.0, 1.0, 0.0, 0.0, 0.0, 0.0)
	f.Add(-1000.0, -50.0, 0.0, 0.0, 1000.0, 50.0)

	f.Fuzz(func(t *testing.T, x1, y1, x2, y2, x3, y3 float64) {
		points := []DriftPoint{
			{TimeMs: x1, DriftMs: y1},
			{TimeMs: x2, DriftMs: y2},
			{TimeMs: x3, DriftMs: y3},
		}
		slope, intercept, r2 := LinearRegression(points)

		// Must not return NaN or Inf.
		if math.IsNaN(slope) || math.IsInf(slope, 0) {
			t.Fatalf("slope is %v", slope)
		}
		if math.IsNaN(intercept) || math.IsInf(intercept, 0) {
			t.Fatalf("intercept is %v", intercept)
		}
		if math.IsNaN(r2) || math.IsInf(r2, 0) {
			t.Fatalf("r2 is %v", r2)
		}
	})
}
