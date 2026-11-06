package timing

import (
	"fmt"
	"math"
	"reflect"
)

// The bounds of architecture contract C7.
const (
	maxOffsetMs     = 21_600_000
	minScale        = 0.9
	maxScale        = 1.1
	minSegments     = 2
	maxSegments     = 2_048
	maxSegmentEndMs = 86_400_000
)

// Validate is the one Timing check: it refuses, as ErrTimingInvalid, a nil
// or unknown Timing, an offset past ±6 h, a Scale outside [0.9, 1.1], a
// Piecewise of fewer than 2 or more than 2,048 segments, whose first FromMs
// is not 0, whose segments do not chain or end past 24 h, or whose last ToMs
// is not 0, and any form Canonical would change.
func Validate(t Timing) error {
	if err := checkBounds(t); err != nil {
		return err
	}
	// Canonical changes a value exactly when it changes its variant.
	if c := Canonical(t); reflect.TypeOf(c) != reflect.TypeOf(t) {
		return fmt.Errorf("%w: %T is not canonical, use %T", ErrTimingInvalid, t, c)
	}
	return nil
}

func checkBounds(t Timing) error {
	switch v := t.(type) {
	case Identity:
		return nil
	case Shift:
		return checkOffset("Ms", v.Ms)
	case Linear:
		if err := checkScale(v.Scale); err != nil {
			return err
		}
		return checkOffset("OffsetMs", v.OffsetMs)
	case Piecewise:
		if err := checkScale(v.Scale); err != nil {
			return err
		}
		return checkSegments(v.Segments)
	default:
		return fmt.Errorf("%w: unknown variant %T", ErrTimingInvalid, t)
	}
}

func checkOffset(field string, ms int64) error {
	if ms < -maxOffsetMs || ms > maxOffsetMs {
		return fmt.Errorf("%w: %s %d outside ±%d", ErrTimingInvalid, field, ms, maxOffsetMs)
	}
	return nil
}

func checkScale(s float64) error {
	if math.IsNaN(s) || s < minScale || s > maxScale {
		return fmt.Errorf("%w: Scale %v outside [%v, %v]", ErrTimingInvalid, s, minScale, maxScale)
	}
	return nil
}

func checkSegments(segs []Segment) error {
	n := len(segs)
	if n < minSegments || n > maxSegments {
		return fmt.Errorf("%w: %d segments outside [%d, %d]", ErrTimingInvalid, n, minSegments, maxSegments)
	}
	var from int64
	for i, s := range segs {
		if s.FromMs != from {
			return fmt.Errorf("%w: segment %d starts at %d, want %d", ErrTimingInvalid, i, s.FromMs, from)
		}
		if s.OffsetMs < -maxOffsetMs || s.OffsetMs > maxOffsetMs {
			return fmt.Errorf("%w: segment %d offset %d outside ±%d", ErrTimingInvalid, i, s.OffsetMs, maxOffsetMs)
		}
		if i < n-1 && (s.ToMs <= s.FromMs || s.ToMs > maxSegmentEndMs) {
			return fmt.Errorf("%w: segment %d ends at %d, outside (%d, %d]", ErrTimingInvalid, i, s.ToMs, s.FromMs, maxSegmentEndMs)
		}
		from = s.ToMs
	}
	if last := segs[n-1].ToMs; last != 0 {
		return fmt.Errorf("%w: last segment ends at %d, want 0 (open-ended)", ErrTimingInvalid, last)
	}
	return nil
}
