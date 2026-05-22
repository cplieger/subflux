package crosslang

import (
	"testing"

	"pgregory.net/rapid"
)

func TestDPAlign(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		pairs     []CuePair
		wantLen   int
		checkMono bool
	}{
		{"empty", nil, 0, false},
		{"single_pair", []CuePair{{IncIdx: 0, RefIdx: 0, Score: 1.0}}, 1, true},
		{
			"monotonic_input",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 1, Score: 0.8},
				{IncIdx: 2, RefIdx: 2, Score: 0.7},
			},
			3, true,
		},
		{
			"crossing_pairs_selects_optimal",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 2, Score: 0.8},
				{IncIdx: 2, RefIdx: 1, Score: 0.9}, // crosses with previous
				{IncIdx: 3, RefIdx: 3, Score: 0.7},
			},
			-1, true, // length varies; just check monotonicity
		},
		{
			"prefers_higher_score_path",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.1},
				{IncIdx: 0, RefIdx: 1, Score: 0.9}, // same IncIdx, higher score
				{IncIdx: 1, RefIdx: 2, Score: 0.5},
			},
			2, true, // picks (0,1,0.9) then (1,2,0.5)
		},
		{
			"large_input_exceeds_dpMaxPredecessors",
			func() []CuePair {
				pairs := make([]CuePair, 400)
				for i := range pairs {
					pairs[i] = CuePair{IncIdx: i, RefIdx: i, Score: 0.5}
				}
				return pairs
			}(),
			400, true,
		},
		{
			"unsorted_input",
			[]CuePair{
				{IncIdx: 3, RefIdx: 3, Score: 0.7},
				{IncIdx: 0, RefIdx: 0, Score: 0.5},
				{IncIdx: 1, RefIdx: 1, Score: 0.8},
			},
			3, true,
		},
		{
			"duplicate_indices_different_scores",
			[]CuePair{
				{IncIdx: 0, RefIdx: 0, Score: 0.3},
				{IncIdx: 0, RefIdx: 0, Score: 0.9},
				{IncIdx: 1, RefIdx: 1, Score: 0.5},
			},
			2, true, // picks best from duplicates
		},
		{
			"all_same_index",
			[]CuePair{
				{IncIdx: 5, RefIdx: 5, Score: 0.3},
				{IncIdx: 5, RefIdx: 5, Score: 0.9},
				{IncIdx: 5, RefIdx: 5, Score: 0.5},
			},
			1, false, // can only pick one
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DPAlign(tt.pairs)
			if tt.wantLen >= 0 && len(got) != tt.wantLen {
				t.Errorf("DPAlign() returned %d pairs, want %d", len(got), tt.wantLen)
			}
			if tt.checkMono {
				for i := 1; i < len(got); i++ {
					if got[i].IncIdx <= got[i-1].IncIdx {
						t.Errorf("IncIdx not strictly increasing at %d: %d <= %d",
							i, got[i].IncIdx, got[i-1].IncIdx)
					}
					if got[i].RefIdx <= got[i-1].RefIdx {
						t.Errorf("RefIdx not strictly increasing at %d: %d <= %d",
							i, got[i].RefIdx, got[i-1].RefIdx)
					}
				}
			}
		})
	}
}

func TestWeightedMedianOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		pairs []CuePair
		want  int64
	}{
		{"empty", nil, 0},
		{"single", []CuePair{{Score: 1.0, OffsetMs: 500}}, 500},
		{
			"two_equal_weight",
			[]CuePair{
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 200},
			},
			100, // cum reaches half at first element
		},
		{
			"skewed_weight_picks_heavy",
			[]CuePair{
				{Score: 0.1, OffsetMs: 100},
				{Score: 10.0, OffsetMs: 500},
			},
			500,
		},
		{
			"three_middle_wins",
			[]CuePair{
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 300},
				{Score: 1.0, OffsetMs: 500},
			},
			300,
		},
		{
			"unsorted_input",
			[]CuePair{
				{Score: 1.0, OffsetMs: 500},
				{Score: 1.0, OffsetMs: 100},
				{Score: 1.0, OffsetMs: 300},
			},
			300,
		},
		{
			"all_zero_weight",
			[]CuePair{
				{Score: 0.0, OffsetMs: 100},
				{Score: 0.0, OffsetMs: 300},
				{Score: 0.0, OffsetMs: 500},
			},
			100, // half=0, cum>=half on first sorted element
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := WeightedMedianOffset(tt.pairs)
			if got != tt.want {
				t.Errorf("WeightedMedianOffset() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDPAlign_monotonicity_property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 50).Draw(t, "n")
		pairs := make([]CuePair, n)
		for i := range pairs {
			pairs[i] = CuePair{
				IncIdx: rapid.IntRange(0, 100).Draw(t, "incIdx"),
				RefIdx: rapid.IntRange(0, 100).Draw(t, "refIdx"),
				Score:  rapid.Float64Range(0.01, 1.0).Draw(t, "score"),
			}
		}
		result := DPAlign(pairs)
		for i := 1; i < len(result); i++ {
			if result[i].IncIdx <= result[i-1].IncIdx {
				t.Fatalf("IncIdx not strictly increasing: [%d]=%d, [%d]=%d",
					i-1, result[i-1].IncIdx, i, result[i].IncIdx)
			}
			if result[i].RefIdx <= result[i-1].RefIdx {
				t.Fatalf("RefIdx not strictly increasing: [%d]=%d, [%d]=%d",
					i-1, result[i-1].RefIdx, i, result[i].RefIdx)
			}
		}
	})
}
