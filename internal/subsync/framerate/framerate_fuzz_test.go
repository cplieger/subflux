package framerate

import (
	"math"
	"testing"
)

func FuzzLinearRegression(f *testing.F) {
	f.Add(0.0, 0.0, 1000.0, 1.0, 2000.0, 2.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0, 0.0)
	f.Add(100.0, 5.0, 200.0, 10.0, 300.0, 15.0)
	f.Fuzz(func(t *testing.T, x1, y1, x2, y2, x3, y3 float64) {
		// Skip degenerate float values that could produce NaN/Inf in ways
		// unrelated to the logic under test.
		for _, v := range []float64{x1, y1, x2, y2, x3, y3} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e15 {
				return
			}
		}
		points := []DriftPoint{
			{TimeMs: x1, DriftMs: y1},
			{TimeMs: x2, DriftMs: y2},
			{TimeMs: x3, DriftMs: y3},
		}
		// Must not panic.
		slope, intercept, r2 := LinearRegression(points)
		_ = slope
		_ = intercept
		// r2 should be in [0,1] for well-defined inputs (allow small epsilon for floating point)
		if !math.IsNaN(r2) && (r2 < -0.01 || r2 > 1.01) {
			t.Errorf("r2 out of expected range: %f", r2)
		}
	})
}
