package wirespec

import (
	"fmt"
	"slices"

	"github.com/cplieger/subflux/internal/timing"
)

// TimingWire is timing.Timing on the wire, a union tagged by kind whose
// cases are TimingCases. A field another kind uses, or one its kind lists
// and the body omits, is a *FieldError; ToTiming then validates
// timing.Canonical's result, so a non-canonical form such as a zero shift
// binds as its canonical value.
type TimingWire struct {
	Ms       *int64       `json:"ms,omitempty"`
	Scale    *float64     `json:"scale,omitempty"`
	OffsetMs *int64       `json:"offset_ms,omitempty"`
	Segments []SegmentDoc `json:"segments,omitempty"`
	Kind     TimingKind   `json:"kind"`
}

// TimingKind is TimingWire's tag: Identity | Shift | Linear | Piecewise.
type TimingKind uint8

// The timing kinds, one per timing.Timing variant.
const (
	TimingIdentity TimingKind = iota + 1
	TimingShift
	TimingLinear
	TimingPiecewise
)

var timingKindNames = []string{
	TimingIdentity: "identity", TimingShift: "shift", TimingLinear: "linear", TimingPiecewise: "piecewise",
}

// MarshalText answers identity, shift, linear or piecewise and refuses any
// other value.
func (k TimingKind) MarshalText() ([]byte, error) {
	return marshalName(timingKindNames, "timing kind", uint8(k))
}

// UnmarshalText reads MarshalText's spellings and refuses every other text.
func (k *TimingKind) UnmarshalText(b []byte) error {
	return unmarshalName(k, timingKindNames, "timing kind", b)
}

// SegmentDoc is timing.Segment on the wire.
type SegmentDoc struct {
	FromMs   int64 `json:"from_ms"`
	ToMs     int64 `json:"to_ms"`
	OffsetMs int64 `json:"offset_ms"`
	Drop     bool  `json:"drop,omitempty"`
}

// TimingCases is TimingWire's one union table: ToTiming refuses a field its
// kind does not list and requires each one it lists.
var TimingCases = map[string][]string{
	"identity":  {},
	"shift":     {fieldMs},
	"linear":    {fieldScale, fieldOffsetMs},
	"piecewise": {fieldScale, fieldSegments},
}

// TimingWire's case fields by json name.
const (
	fieldMs       = "ms"
	fieldScale    = "scale"
	fieldOffsetMs = "offset_ms"
	fieldSegments = "segments"
)

// ToTiming binds w: a *FieldError, with a path relative to w, for a field
// outside its kind's case or one missing from it, else timing.Validate's
// verdict on timing.Canonical's result, wrapping timing.ErrTimingInvalid.
func ToTiming(w TimingWire) (timing.Timing, error) {
	name, err := w.Kind.MarshalText()
	if err != nil {
		return nil, &FieldError{Path: "kind", Message: "must be identity, shift, linear or piecewise"}
	}
	listed := TimingCases[string(name)]
	for _, f := range []struct {
		name    string
		present bool
	}{{fieldMs, w.Ms != nil}, {fieldScale, w.Scale != nil}, {fieldOffsetMs, w.OffsetMs != nil}, {fieldSegments, w.Segments != nil}} {
		switch in := slices.Contains(listed, f.name); {
		case f.present && !in:
			return nil, &FieldError{Path: f.name, Message: fmt.Sprintf("is not a field of a %s timing", name)}
		case !f.present && in:
			return nil, &FieldError{Path: f.name, Message: fmt.Sprintf("is required by a %s timing", name)}
		}
	}
	var t timing.Timing
	switch w.Kind {
	case TimingIdentity:
		t = timing.Identity{}
	case TimingShift:
		t = timing.Shift{Ms: *w.Ms}
	case TimingLinear:
		t = timing.Linear{Scale: *w.Scale, OffsetMs: *w.OffsetMs}
	case TimingPiecewise:
		segs := make([]timing.Segment, len(w.Segments))
		for i, s := range w.Segments {
			segs[i] = timing.Segment{FromMs: s.FromMs, ToMs: s.ToMs, OffsetMs: s.OffsetMs, Drop: s.Drop}
		}
		t = timing.Piecewise{Segments: segs, Scale: *w.Scale}
	}
	t = timing.Canonical(t)
	if err := timing.Validate(t); err != nil {
		return nil, err
	}
	return t, nil
}

// FromTiming is t on the wire; timing.Identity and a nil t are the identity
// kind.
func FromTiming(t timing.Timing) TimingWire {
	switch v := t.(type) {
	case timing.Shift:
		return TimingWire{Kind: TimingShift, Ms: &v.Ms}
	case timing.Linear:
		return TimingWire{Kind: TimingLinear, Scale: &v.Scale, OffsetMs: &v.OffsetMs}
	case timing.Piecewise:
		segs := make([]SegmentDoc, len(v.Segments))
		for i, s := range v.Segments {
			segs[i] = SegmentDoc{FromMs: s.FromMs, ToMs: s.ToMs, OffsetMs: s.OffsetMs, Drop: s.Drop}
		}
		return TimingWire{Kind: TimingPiecewise, Scale: &v.Scale, Segments: segs}
	}
	return TimingWire{Kind: TimingIdentity}
}
