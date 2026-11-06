package timing_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// pw builds a Piecewise from segment end points: ends[i] is segment i's
// ToMs, offsets[i] its OffsetMs; the last segment is open-ended.
func pw(scale float64, ends, offsets []int64) timing.Piecewise {
	segs := make([]timing.Segment, len(offsets))
	var from int64
	for i := range segs {
		var to int64
		if i < len(ends) {
			to = ends[i]
		}
		segs[i] = timing.Segment{FromMs: from, ToMs: to, OffsetMs: offsets[i]}
		from = to
	}
	return timing.Piecewise{Scale: scale, Segments: segs}
}

func manySegments(n int) timing.Piecewise {
	ends := make([]int64, n-1)
	offsets := make([]int64, n)
	for i := range ends {
		ends[i] = int64(i+1) * 1000
	}
	for i := range offsets {
		offsets[i] = int64(i)
	}
	return pw(1, ends, offsets)
}

func withSegment(p timing.Piecewise, i int, edit func(*timing.Segment)) timing.Piecewise {
	p.Segments = append([]timing.Segment(nil), p.Segments...)
	edit(&p.Segments[i])
	return p
}

var twoSegments = pw(1, []int64{1000}, []int64{0, 500})

var validTimings = []struct {
	name string
	t    timing.Timing
}{
	{"identity", timing.Identity{}},
	{"shift at max", timing.Shift{Ms: 21_600_000}},
	{"shift at min", timing.Shift{Ms: -21_600_000}},
	{"linear at min scale", timing.Linear{Scale: 0.9, OffsetMs: -21_600_000}},
	{"linear at max scale", timing.Linear{Scale: 1.1, OffsetMs: 21_600_000}},
	{"linear with zero offset", timing.Linear{Scale: 1.04, OffsetMs: 0}},
	{"piecewise of two", twoSegments},
	{"piecewise of 2048", manySegments(2048)},
	{"piecewise at min scale", pw(0.9, []int64{1000}, []int64{0, 500})},
	{"piecewise at max scale", pw(1.1, []int64{1000}, []int64{0, 500})},
	{"piecewise closed end at 24 h", pw(1, []int64{86_400_000}, []int64{0, 500})},
	{"piecewise offsets at bounds", pw(1, []int64{1000}, []int64{-21_600_000, 21_600_000})},
	{"piecewise one offset with a drop", withSegment(pw(1, []int64{1000}, []int64{7, 7}), 1, func(s *timing.Segment) { s.Drop = true })},
}

var invalidTimings = []struct {
	name string
	t    timing.Timing
}{
	{"nil", nil},
	{"pointer variant", &timing.Shift{Ms: 5}},
	{"shift past max", timing.Shift{Ms: 21_600_001}},
	{"shift past min", timing.Shift{Ms: -21_600_001}},
	{"linear offset past max", timing.Linear{Scale: 1.04, OffsetMs: 21_600_001}},
	{"linear offset past min", timing.Linear{Scale: 1.04, OffsetMs: -21_600_001}},
	{"linear scale NaN", timing.Linear{Scale: math.NaN(), OffsetMs: 5}},
	{"linear scale +Inf", timing.Linear{Scale: math.Inf(1), OffsetMs: 5}},
	{"linear scale -Inf", timing.Linear{Scale: math.Inf(-1), OffsetMs: 5}},
	{"linear scale 0.8999", timing.Linear{Scale: 0.8999, OffsetMs: 5}},
	{"linear scale 1.1001", timing.Linear{Scale: 1.1001, OffsetMs: 5}},
	{"piecewise scale NaN", pw(math.NaN(), []int64{1000}, []int64{0, 500})},
	{"piecewise scale +Inf", pw(math.Inf(1), []int64{1000}, []int64{0, 500})},
	{"piecewise scale -Inf", pw(math.Inf(-1), []int64{1000}, []int64{0, 500})},
	{"piecewise scale 0.8999", pw(0.8999, []int64{1000}, []int64{0, 500})},
	{"piecewise scale 1.1001", pw(1.1001, []int64{1000}, []int64{0, 500})},
	{"piecewise of none", timing.Piecewise{Scale: 1}},
	{"piecewise of one", timing.Piecewise{Scale: 1, Segments: []timing.Segment{{OffsetMs: 5}}}},
	{"piecewise of 2049", manySegments(2049)},
	{"first FromMs not 0", withSegment(twoSegments, 0, func(s *timing.Segment) { s.FromMs = 1 })},
	{"segments with a gap", withSegment(twoSegments, 1, func(s *timing.Segment) { s.FromMs = 1001 })},
	{"segments overlapping", withSegment(twoSegments, 1, func(s *timing.Segment) { s.FromMs = 999 })},
	{"last ToMs not 0", withSegment(twoSegments, 1, func(s *timing.Segment) { s.ToMs = 5000 })},
	{"empty segment", pw(1, []int64{1000, 1000}, []int64{0, 5, 10})},
	{"closed end past 24 h", pw(1, []int64{86_400_001}, []int64{0, 500})},
	{"segment offset past max", pw(1, []int64{1000}, []int64{0, 21_600_001})},
	{"segment offset past min", pw(1, []int64{1000}, []int64{-21_600_001, 0})},
	{"non-canonical shift 0", timing.Shift{Ms: 0}},
	{"non-canonical linear scale 1", timing.Linear{Scale: 1, OffsetMs: 300}},
	{"non-canonical piecewise one offset", pw(1.04, []int64{1000}, []int64{7, 7})},
}

func TestValidateBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range validTimings {
		t.Run("valid "+tc.name, func(t *testing.T) {
			t.Parallel()
			if err := timing.Validate(tc.t); err != nil {
				t.Errorf("Validate(%#v) = %v, want nil", tc.t, err)
			}
		})
	}
	for _, tc := range invalidTimings {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			t.Parallel()
			if err := timing.Validate(tc.t); !errors.Is(err, timing.ErrTimingInvalid) {
				t.Errorf("Validate(%#v) = %v, want ErrTimingInvalid", tc.t, err)
			}
		})
	}
}

func TestCanonicalForms(t *testing.T) {
	t.Parallel()
	dropped := withSegment(pw(1, []int64{1000}, []int64{0, 0}), 0, func(s *timing.Segment) { s.Drop = true })
	twoOffsets := pw(1.04, []int64{1000}, []int64{0, 500})
	badChain := withSegment(pw(1, []int64{1000}, []int64{7, 7}), 1, func(s *timing.Segment) { s.FromMs = 1001 })
	for _, tc := range []struct {
		name    string
		in      timing.Timing
		want    timing.Timing
		inValid bool
	}{
		{"identity stays", timing.Identity{}, timing.Identity{}, true},
		{"shift 0 is identity", timing.Shift{}, timing.Identity{}, false},
		{"shift stays", timing.Shift{Ms: -40}, timing.Shift{Ms: -40}, true},
		{"linear scale 1 offset 0 is identity", timing.Linear{Scale: 1}, timing.Identity{}, false},
		{"linear scale 1 is shift", timing.Linear{Scale: 1, OffsetMs: 300}, timing.Shift{Ms: 300}, false},
		{"linear stays", timing.Linear{Scale: 1.05}, timing.Linear{Scale: 1.05}, true},
		{"piecewise one offset is linear", pw(1.04, []int64{1000, 2000}, []int64{7, 7, 7}), timing.Linear{Scale: 1.04, OffsetMs: 7}, false},
		{"piecewise scale 1 one offset is shift", pw(1, []int64{1000}, []int64{7, 7}), timing.Shift{Ms: 7}, false},
		{"piecewise scale 1 offset 0 is identity", pw(1, []int64{1000}, []int64{0, 0}), timing.Identity{}, false},
		{"piecewise with a drop stays", dropped, dropped, true},
		{"piecewise with two offsets stays", twoOffsets, twoOffsets, true},
		{"malformed piecewise stays", badChain, badChain, false},
		{"out-of-bounds linear stays", timing.Linear{Scale: 1, OffsetMs: 21_600_001}, timing.Linear{Scale: 1, OffsetMs: 21_600_001}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := timing.Canonical(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Canonical(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
			if err := timing.Validate(tc.in); (err == nil) != tc.inValid {
				t.Errorf("Validate(%#v) = %v, want valid %v", tc.in, err, tc.inValid)
			}
		})
	}
}
