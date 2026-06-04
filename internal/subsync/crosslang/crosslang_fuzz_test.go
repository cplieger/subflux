package crosslang

import "testing"

func FuzzWeightedMedianOffset(f *testing.F) {
	f.Add(0, 0, 1.0, int64(100), 1, 1, 0.5, int64(200))
	f.Add(0, 0, 0.0, int64(0), 0, 0, 0.0, int64(0))
	f.Add(3, 10, 0.9, int64(-500), 5, 20, 0.8, int64(500))

	f.Fuzz(func(t *testing.T, incIdx1, refIdx1 int, score1 float64, offset1 int64, incIdx2, refIdx2 int, score2 float64, offset2 int64) {
		pairs := []CuePair{
			{IncIdx: incIdx1, RefIdx: refIdx1, Score: score1, OffsetMs: offset1},
			{IncIdx: incIdx2, RefIdx: refIdx2, Score: score2, OffsetMs: offset2},
		}
		// Must not panic.
		_ = WeightedMedianOffset(pairs)
	})
}

func FuzzDPAlign(f *testing.F) {
	f.Add(0, 0, 1.0, int64(100), 1, 1, 0.8, int64(110), 2, 2, 0.6, int64(105))
	f.Add(0, 0, 0.0, int64(0), 0, 0, 0.0, int64(0), 0, 0, 0.0, int64(0))

	f.Fuzz(func(t *testing.T, ii1, ri1 int, s1 float64, o1 int64, ii2, ri2 int, s2 float64, o2 int64, ii3, ri3 int, s3 float64, o3 int64) {
		pairs := []CuePair{
			{IncIdx: ii1, RefIdx: ri1, Score: s1, OffsetMs: o1},
			{IncIdx: ii2, RefIdx: ri2, Score: s2, OffsetMs: o2},
			{IncIdx: ii3, RefIdx: ri3, Score: s3, OffsetMs: o3},
		}
		// Must not panic.
		_ = DPAlign(pairs)
	})
}
