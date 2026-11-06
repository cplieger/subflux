package timing_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// scaleBits is 1.0625 (1 + 1/16) as big-endian IEEE 754 binary64.
var scaleBits = []byte{0x3f, 0xf1, 0, 0, 0, 0, 0, 0}

func cat(parts ...[]byte) []byte { return slices.Concat(parts...) }

// The stored byte form; a change here is a CodecVersion bump.
func TestTimingEncoding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		t    timing.Timing
		want []byte
	}{
		{"identity", timing.Identity{}, []byte{1}},
		{"shift -1", timing.Shift{Ms: -1}, []byte{2, 0x01}},
		{"shift 150", timing.Shift{Ms: 150}, []byte{2, 0xac, 0x02}},
		{"linear", timing.Linear{Scale: 1.0625, OffsetMs: -2}, cat([]byte{3}, scaleBits, []byte{0x03})},
		{"piecewise", timing.Piecewise{Scale: 1.0625, Segments: []timing.Segment{
			{FromMs: 0, ToMs: 1000, OffsetMs: 5},
			{FromMs: 1000, ToMs: 0, OffsetMs: 0, Drop: true},
		}}, cat([]byte{4}, scaleBits, []byte{
			0x02,
			0x00, 0xd0, 0x0f, 0x0a, 0x00,
			0xd0, 0x0f, 0x00, 0x00, 0x01,
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := timing.AppendTiming([]byte{0xee}, tc.t)
			if want := cat([]byte{0xee}, tc.want); !bytes.Equal(got, want) {
				t.Errorf("AppendTiming(prefix, %#v) = % x, want % x", tc.t, got, want)
			}
			back, err := timing.DecodeTiming(tc.want)
			if err != nil {
				t.Fatalf("DecodeTiming(% x) = %v", tc.want, err)
			}
			if !reflect.DeepEqual(back, tc.t) {
				t.Errorf("DecodeTiming(% x) = %#v, want %#v", tc.want, back, tc.t)
			}
		})
	}
}

// undecodable holds byte strings DecodeTiming must refuse.
var undecodable = []struct {
	name string
	b    []byte
}{
	{"empty", nil},
	{"kind 0", []byte{0}},
	{"kind 5", []byte{5}},
	{"kind 255", []byte{255}},
	{"nil timing encoding", timing.AppendTiming(nil, nil)},
	{"identity trailing byte", []byte{1, 0}},
	{"shift truncated", []byte{2}},
	{"shift varint truncated", []byte{2, 0x80}},
	{"shift varint overflow", []byte{2, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}},
	{"shift non-minimal varint", []byte{2, 0x82, 0x00}},
	{"shift past max", timing.AppendTiming(nil, timing.Shift{Ms: 21_600_001})},
	{"shift 0 non-canonical", []byte{2, 0x00}},
	{"linear scale truncated", cat([]byte{3}, scaleBits[:7])},
	{"linear offset missing", cat([]byte{3}, scaleBits)},
	{"linear scale NaN", cat([]byte{3, 0x7f, 0xf8, 0, 0, 0, 0, 0, 0}, []byte{0x02})},
	{"linear scale 1 non-canonical", timing.AppendTiming(nil, timing.Linear{Scale: 1, OffsetMs: 3})},
	{"piecewise count missing", cat([]byte{4}, scaleBits)},
	{"piecewise count overflow", cat([]byte{4}, scaleBits, bytes.Repeat([]byte{0xff}, 10), []byte{0x7f})},
	{"piecewise count past 2048", cat([]byte{4}, scaleBits, []byte{0x81, 0x10})},
	{"piecewise count past input", cat([]byte{4}, scaleBits, []byte{0x03, 0, 0x02, 0x00, 0x00})},
	{"piecewise segment truncated", cat([]byte{4}, scaleBits, []byte{0x02, 0x00, 0xd0, 0x0f, 0x0a, 0x00, 0xd0, 0x0f, 0x00, 0x00})},
	{"piecewise drop byte 2", cat([]byte{4}, scaleBits, []byte{0x02, 0x00, 0xd0, 0x0f, 0x0a, 0x00, 0xd0, 0x0f, 0x00, 0x00, 0x02})},
	{"piecewise unchained", timing.AppendTiming(nil, withSegment(twoSegments, 1, func(s *timing.Segment) { s.FromMs = 1001 }))},
	{"piecewise one offset non-canonical", timing.AppendTiming(nil, pw(1.04, []int64{1000}, []int64{7, 7}))},
}

func TestDecodeTimingRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range undecodable {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := timing.DecodeTiming(tc.b)
			if !errors.Is(err, timing.ErrTimingInvalid) {
				t.Errorf("DecodeTiming(% x) = %#v, %v; want ErrTimingInvalid", tc.b, got, err)
			}
			if got != nil {
				t.Errorf("DecodeTiming(% x) returned %#v beside its error", tc.b, got)
			}
		})
	}
}

func TestTimingCodecRoundTripValid(t *testing.T) {
	t.Parallel()
	for _, tc := range validTimings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := timing.AppendTiming(nil, tc.t)
			got, err := timing.DecodeTiming(b)
			if err != nil {
				t.Fatalf("DecodeTiming(AppendTiming(%#v)) = %v", tc.t, err)
			}
			if !reflect.DeepEqual(got, tc.t) {
				t.Errorf("DecodeTiming(AppendTiming(%#v)) = %#v", tc.t, got)
			}
		})
	}
}
