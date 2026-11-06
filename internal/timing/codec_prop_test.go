package timing_test

import (
	"reflect"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
	"pgregory.net/rapid"
)

func offsetGen() *rapid.Generator[int64] { return rapid.Int64Range(-21_600_000, 21_600_000) }

func scaleGen() *rapid.Generator[float64] { return rapid.Float64Range(0.9, 1.1) }

// boundedTiming draws any Timing inside Validate's bounds, canonical or not;
// small offset ranges and scale 1 are drawn often so the collapsing forms
// appear.
func boundedTiming() *rapid.Generator[timing.Timing] {
	return rapid.Custom(func(t *rapid.T) timing.Timing {
		offset := rapid.OneOf(offsetGen(), rapid.Int64Range(-2, 2))
		scale := rapid.OneOf(scaleGen(), rapid.Just(1.0))
		switch rapid.IntRange(0, 3).Draw(t, "kind") {
		case 0:
			return timing.Identity{}
		case 1:
			return timing.Shift{Ms: offset.Draw(t, "ms")}
		case 2:
			return timing.Linear{Scale: scale.Draw(t, "scale"), OffsetMs: offset.Draw(t, "offset")}
		default:
			n := rapid.IntRange(2, 40).Draw(t, "segments")
			segs := make([]timing.Segment, n)
			var from int64
			for i := range segs {
				var to int64
				if i < n-1 {
					to = rapid.Int64Range(from+1, min(from+3_600_000, 86_400_000-int64(n-i))).Draw(t, "to")
				}
				segs[i] = timing.Segment{FromMs: from, ToMs: to, OffsetMs: offset.Draw(t, "seg offset"), Drop: rapid.Bool().Draw(t, "drop")}
				from = to
			}
			return timing.Piecewise{Scale: scale.Draw(t, "scale"), Segments: segs}
		}
	})
}

func TestTimingCodecRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		in := timing.Canonical(boundedTiming().Draw(t, "timing"))
		if err := timing.Validate(in); err != nil {
			t.Fatalf("Validate(Canonical(%#v)) = %v", in, err)
		}
		got, err := timing.DecodeTiming(timing.AppendTiming(nil, in))
		if err != nil {
			t.Fatalf("DecodeTiming(AppendTiming(%#v)) = %v", in, err)
		}
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("DecodeTiming(AppendTiming(%#v)) = %#v", in, got)
		}
	})
}

// Canonical is idempotent and Validate refuses exactly the bounded values
// Canonical would change.
func TestCanonicalProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		in := boundedTiming().Draw(t, "timing")
		c := timing.Canonical(in)
		if again := timing.Canonical(c); !reflect.DeepEqual(again, c) {
			t.Fatalf("Canonical(Canonical(%#v)) = %#v, want %#v", in, again, c)
		}
		changed := !reflect.DeepEqual(c, in)
		if err := timing.Validate(in); (err != nil) != changed {
			t.Fatalf("Validate(%#v) = %v with Canonical changing it %v", in, err, changed)
		}
	})
}
