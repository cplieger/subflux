package timing_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

func TestOffsetMs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		t    timing.Timing
		want int64
	}{
		{"identity", timing.Identity{}, 0},
		{"shift", timing.Shift{Ms: -1_500}, -1_500},
		{"linear", timing.Linear{Scale: 25 / 23.976, OffsetMs: 700}, 700},
		{"piecewise after a leading drop", timing.Piecewise{Scale: 1, Segments: []timing.Segment{
			{FromMs: 0, ToMs: 1_000, OffsetMs: 9_000, Drop: true},
			{FromMs: 1_000, ToMs: 5_000, OffsetMs: 2_000},
			{FromMs: 5_000, OffsetMs: 4_000},
		}}, 2_000},
		{"piecewise dropping everything", timing.Piecewise{Scale: 1, Segments: []timing.Segment{{OffsetMs: 3_000, Drop: true}}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timing.OffsetMs(tt.t); got != tt.want {
				t.Errorf("OffsetMs(%+v) = %d, want %d", tt.t, got, tt.want)
			}
		})
	}
}
