package wirespec_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
	"github.com/cplieger/subflux/internal/wirespec"
)

// segs builds n chained segments over [0, n*1000), the last open-ended,
// alternating offsets so they never collapse into one.
func segs(n int) []wirespec.SegmentDoc {
	out := make([]wirespec.SegmentDoc, n)
	for i := range out {
		out[i] = wirespec.SegmentDoc{FromMs: int64(i) * 1000, ToMs: int64(i+1) * 1000, OffsetMs: int64(i % 2)}
	}
	out[n-1].ToMs = 0
	return out
}

func TestToTimingEveryKind(t *testing.T) {
	const maxOffset = 21_600_000 // ±6 h, architecture contract C7
	bound := map[string]struct {
		in   wirespec.TimingWire
		want timing.Timing
	}{
		"identity":                      {wirespec.TimingWire{Kind: wirespec.TimingIdentity}, timing.Identity{}},
		"a shift":                       {wirespec.TimingWire{Kind: wirespec.TimingShift, Ms: new(int64(1500))}, timing.Shift{Ms: 1500}},
		"a shift at +6 h":               {wirespec.TimingWire{Kind: wirespec.TimingShift, Ms: new(int64(maxOffset))}, timing.Shift{Ms: maxOffset}},
		"a shift at -6 h":               {wirespec.TimingWire{Kind: wirespec.TimingShift, Ms: new(int64(-maxOffset))}, timing.Shift{Ms: -maxOffset}},
		"a zero shift is the identity":  {wirespec.TimingWire{Kind: wirespec.TimingShift, Ms: new(int64(0))}, timing.Identity{}},
		"a linear":                      {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.05), OffsetMs: new(int64(-20))}, timing.Linear{Scale: 1.05, OffsetMs: -20}},
		"a linear at scale 0.9":         {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(0.9), OffsetMs: new(int64(0))}, timing.Linear{Scale: 0.9}},
		"a linear at scale 1.1":         {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.1), OffsetMs: new(int64(0))}, timing.Linear{Scale: 1.1}},
		"a scale-1 linear is a shift":   {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.0), OffsetMs: new(int64(40))}, timing.Shift{Ms: 40}},
		"a scale-1 zero linear is none": {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.0), OffsetMs: new(int64(0))}, timing.Identity{}},
		"a piecewise": {
			wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: segs(2)},
			timing.Piecewise{Scale: 1, Segments: []timing.Segment{{FromMs: 0, ToMs: 1000, OffsetMs: 0}, {FromMs: 1000, OffsetMs: 1}}},
		},
		"a piecewise with a drop": {
			wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{ToMs: 500, OffsetMs: 7, Drop: true}, {FromMs: 500, OffsetMs: 7}}},
			timing.Piecewise{Scale: 1, Segments: []timing.Segment{{ToMs: 500, OffsetMs: 7, Drop: true}, {FromMs: 500, OffsetMs: 7}}},
		},
		"a single-offset piecewise is a linear": {
			wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(0.95), Segments: []wirespec.SegmentDoc{{ToMs: 500, OffsetMs: 3}, {FromMs: 500, OffsetMs: 3}}},
			timing.Linear{Scale: 0.95, OffsetMs: 3},
		},
	}
	for name, c := range bound {
		t.Run(name, func(t *testing.T) {
			got, err := wirespec.ToTiming(c.in)
			if err != nil {
				t.Fatalf("ToTiming = %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ToTiming = %#v, want %#v", got, c.want)
			}
		})
	}
	t.Run("2,048 segments", func(t *testing.T) {
		if _, err := wirespec.ToTiming(wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: segs(2048)}); err != nil {
			t.Errorf("ToTiming = %v", err)
		}
	})

	invalid := map[string]wirespec.TimingWire{
		"a shift past +6 h":           {Kind: wirespec.TimingShift, Ms: new(int64(maxOffset + 1))},
		"a shift past -6 h":           {Kind: wirespec.TimingShift, Ms: new(int64(-maxOffset - 1))},
		"a linear below scale 0.9":    {Kind: wirespec.TimingLinear, Scale: new(0.8999), OffsetMs: new(int64(0))},
		"a linear above scale 1.1":    {Kind: wirespec.TimingLinear, Scale: new(1.1001), OffsetMs: new(int64(0))},
		"a linear offset past 6 h":    {Kind: wirespec.TimingLinear, Scale: new(1.05), OffsetMs: new(int64(maxOffset + 1))},
		"one segment":                 {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: segs(1)},
		"2,049 segments":              {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: segs(2049)},
		"no segments":                 {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{}},
		"a first segment not at zero": {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{FromMs: 1, ToMs: 500}, {FromMs: 500, OffsetMs: 1}}},
		"a closed last segment":       {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{ToMs: 500}, {FromMs: 500, ToMs: 900, OffsetMs: 1}}},
		"a gap between segments":      {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{ToMs: 500}, {FromMs: 600, OffsetMs: 1}}},
		"a segment past 24 h":         {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{ToMs: 86_400_001}, {FromMs: 86_400_001, OffsetMs: 1}}},
		"a segment offset past 6 h":   {Kind: wirespec.TimingPiecewise, Scale: new(1.0), Segments: []wirespec.SegmentDoc{{ToMs: 500}, {FromMs: 500, OffsetMs: maxOffset + 1}}},
		"a piecewise scale past 1.1":  {Kind: wirespec.TimingPiecewise, Scale: new(1.2), Segments: segs(2)},
	}
	for name, in := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := wirespec.ToTiming(in); !errors.Is(err, timing.ErrTimingInvalid) {
				t.Errorf("ToTiming = %v, want timing.ErrTimingInvalid", err)
			}
		})
	}

	shape := map[string]struct {
		in   wirespec.TimingWire
		path string
	}{
		"no kind":                         {wirespec.TimingWire{}, "kind"},
		"an identity with ms":             {wirespec.TimingWire{Kind: wirespec.TimingIdentity, Ms: new(int64(0))}, "ms"},
		"a shift without ms":              {wirespec.TimingWire{Kind: wirespec.TimingShift}, "ms"},
		"a shift with a scale":            {wirespec.TimingWire{Kind: wirespec.TimingShift, Ms: new(int64(1)), Scale: new(1.0)}, "scale"},
		"a linear without offset_ms":      {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.05)}, "offset_ms"},
		"a linear with segments":          {wirespec.TimingWire{Kind: wirespec.TimingLinear, Scale: new(1.05), OffsetMs: new(int64(0)), Segments: segs(2)}, "segments"},
		"a piecewise without a scale":     {wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Segments: segs(2)}, "scale"},
		"a piecewise with offset_ms":      {wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(1.0), OffsetMs: new(int64(0)), Segments: segs(2)}, "offset_ms"},
		"a piecewise without segments":    {wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Scale: new(1.0)}, "segments"},
		"an out-of-range kind":            {wirespec.TimingWire{Kind: wirespec.TimingPiecewise + 1}, "kind"},
		"a piecewise with ms and nothing": {wirespec.TimingWire{Kind: wirespec.TimingPiecewise, Ms: new(int64(0))}, "ms"},
	}
	for name, c := range shape {
		t.Run(name, func(t *testing.T) {
			_, err := wirespec.ToTiming(c.in)
			var fe *wirespec.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("ToTiming = %v, want a *FieldError", err)
			}
			if fe.Path != c.path {
				t.Errorf("path = %q, want %q", fe.Path, c.path)
			}
		})
	}
}

func TestTimingCasesMatchWireFields(t *testing.T) {
	fields := map[string]bool{}
	ty := reflect.TypeFor[wirespec.TimingWire]()
	for f := range ty.Fields() {
		if name := jsonName(f); name != "kind" {
			fields[name] = true
		}
	}
	listed := map[string]bool{}
	for kind, fs := range wirespec.TimingCases {
		var k wirespec.TimingKind
		if err := k.UnmarshalText([]byte(kind)); err != nil {
			t.Errorf("case %q is not a TimingKind: %v", kind, err)
		}
		for _, f := range fs {
			if !fields[f] {
				t.Errorf("case %q lists %q, which TimingWire has no field for", kind, f)
			}
			listed[f] = true
		}
	}
	for f := range fields {
		if !listed[f] {
			t.Errorf("TimingWire field %q is in no case", f)
		}
	}
	if len(wirespec.TimingCases) != 4 {
		t.Errorf("%d cases, want the four kinds", len(wirespec.TimingCases))
	}
}

func TestTimingDecodesFromJSON(t *testing.T) {
	var w wirespec.TimingWire
	if err := json.Unmarshal([]byte(`{"kind":"shift","ms":0}`), &w); err != nil {
		t.Fatalf("Unmarshal = %v", err)
	}
	got, err := wirespec.ToTiming(w)
	if err != nil || got != (timing.Identity{}) {
		t.Errorf("a zero shift binds as %#v, %v; want the identity", got, err)
	}
	if err := json.Unmarshal([]byte(`{"kind":"warp"}`), &w); err == nil {
		t.Error("an unknown kind decoded")
	}
}

func TestFromTimingRoundTrip(t *testing.T) {
	for name, in := range map[string]timing.Timing{
		"identity":  timing.Identity{},
		"shift":     timing.Shift{Ms: -250},
		"linear":    timing.Linear{Scale: 1.001, OffsetMs: 90},
		"piecewise": timing.Piecewise{Scale: 0.97, Segments: []timing.Segment{{ToMs: 400, OffsetMs: 5, Drop: true}, {FromMs: 400, OffsetMs: 9}}},
	} {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(wirespec.FromTiming(in))
			if err != nil {
				t.Fatalf("Marshal = %v", err)
			}
			var w wirespec.TimingWire
			if err := json.Unmarshal(b, &w); err != nil {
				t.Fatalf("Unmarshal(%s) = %v", b, err)
			}
			got, err := wirespec.ToTiming(w)
			if err != nil {
				t.Fatalf("ToTiming(%s) = %v", b, err)
			}
			if !reflect.DeepEqual(got, in) {
				t.Errorf("round trip of %s = %#v, want %#v", b, got, in)
			}
		})
	}
	if got := wirespec.FromTiming(nil); got.Kind != wirespec.TimingIdentity {
		t.Errorf("FromTiming(nil).Kind = %v, want identity", got.Kind)
	}
}
