// Package timing is subflux's subtitle timing model: a Timing maps each cue's
// baseline time to the time a media server shows it, and every managed file
// is its baseline rendered under one Timing.
package timing

// Timing is Identity, Shift, Linear or Piecewise. A valid Timing is in its
// canonical form (see Canonical), so a Timing is applied exactly when it is
// not Identity{}.
type Timing interface{ isTiming() }

// Identity leaves every cue where the baseline has it.
type Identity struct{}

// Shift maps baseline time t to t + Ms.
type Shift struct{ Ms int64 }

// Linear maps baseline time t to t*Scale + OffsetMs.
type Linear struct {
	Scale    float64
	OffsetMs int64
}

// Segment covers baseline time [FromMs, ToMs); a cue belongs to the segment
// holding its baseline start. Drop removes the cues starting in it.
type Segment struct {
	FromMs, ToMs, OffsetMs int64
	Drop                   bool
}

// Piecewise maps a cue at baseline time t in segment s to t*Scale + s.OffsetMs.
// The last segment's ToMs is 0, meaning open-ended.
type Piecewise struct {
	Segments []Segment
	Scale    float64
}

func (Identity) isTiming()  {}
func (Shift) isTiming()     {}
func (Linear) isTiming()    {}
func (Piecewise) isTiming() {}

// OffsetMs is the offset t applies at the start of the baseline: a Shift's,
// a Linear's, a Piecewise's first kept segment's; 0 for Identity.
func OffsetMs(t Timing) int64 {
	switch v := t.(type) {
	case Shift:
		return v.Ms
	case Linear:
		return v.OffsetMs
	case Piecewise:
		for _, s := range v.Segments {
			if !s.Drop {
				return s.OffsetMs
			}
		}
	}
	return 0
}

// Canonical returns t in its canonical form: a Piecewise with no Drop and
// one OffsetMs is Linear, a Linear with Scale 1 is Shift, Shift{0} is
// Identity. Every producer canonicalizes before Validate, which refuses any
// other form. A t outside Validate's bounds is returned unchanged, so a
// malformed value never collapses into a valid one.
func Canonical(t Timing) Timing {
	if checkBounds(t) != nil {
		return t
	}
	if p, ok := t.(Piecewise); ok {
		if offset, ok := singleOffset(p.Segments); ok {
			t = Linear{Scale: p.Scale, OffsetMs: offset}
		}
	}
	if l, ok := t.(Linear); ok && l.Scale == 1 {
		t = Shift{Ms: l.OffsetMs}
	}
	if s, ok := t.(Shift); ok && s.Ms == 0 {
		t = Identity{}
	}
	return t
}

func singleOffset(segs []Segment) (int64, bool) {
	if len(segs) == 0 {
		return 0, false
	}
	for _, s := range segs {
		if s.Drop || s.OffsetMs != segs[0].OffsetMs {
			return 0, false
		}
	}
	return segs[0].OffsetMs, true
}
