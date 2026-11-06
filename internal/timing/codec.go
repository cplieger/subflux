package timing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Kind bytes. 0 is never written for a Timing, so a nil one encodes as
// bytes DecodeTiming refuses.
const (
	kindInvalid byte = iota
	kindIdentity
	kindShift
	kindLinear
	kindPiecewise
)

// minSegmentBytes is the shortest encoded Segment: three one-byte varints
// and the drop byte.
const minSegmentBytes = 4

var (
	errTruncated = errors.New("truncated")
	errOverflow  = errors.New("varint overflows 64 bits")
)

// AppendTiming appends t's encoding to b: a kind byte, then zig-zag varints
// for every millisecond field, Scale as its big-endian IEEE 754 bits, and a
// Piecewise's segment count as a uvarint. A t failing Validate encodes as
// bytes DecodeTiming refuses.
func AppendTiming(b []byte, t Timing) []byte {
	switch v := t.(type) {
	case Identity:
		return append(b, kindIdentity)
	case Shift:
		return binary.AppendVarint(append(b, kindShift), v.Ms)
	case Linear:
		b = binary.BigEndian.AppendUint64(append(b, kindLinear), math.Float64bits(v.Scale))
		return binary.AppendVarint(b, v.OffsetMs)
	case Piecewise:
		b = binary.BigEndian.AppendUint64(append(b, kindPiecewise), math.Float64bits(v.Scale))
		b = binary.AppendUvarint(b, uint64(len(v.Segments)))
		for _, s := range v.Segments {
			b = binary.AppendVarint(b, s.FromMs)
			b = binary.AppendVarint(b, s.ToMs)
			b = binary.AppendVarint(b, s.OffsetMs)
			b = append(b, boolByte(s.Drop))
		}
		return b
	default:
		return append(b, kindInvalid)
	}
}

// DecodeTiming reads exactly one AppendTiming encoding. It refuses, as
// ErrTimingInvalid, an unknown kind, truncated or trailing bytes, a value
// failing Validate, and any encoding AppendTiming would not write for the
// value it holds, so every accepted Timing has one byte form.
func DecodeTiming(b []byte) (Timing, error) {
	d := decoder{b: b}
	t, err := d.timing()
	if err != nil {
		return nil, fmt.Errorf("%w: decode: %w", ErrTimingInvalid, err)
	}
	if err := Validate(t); err != nil {
		return nil, err
	}
	// Also refuses trailing bytes and overlong varints.
	if !bytes.Equal(AppendTiming(nil, t), b) {
		return nil, fmt.Errorf("%w: decode: not the encoding of %#v", ErrTimingInvalid, t)
	}
	return t, nil
}

type decoder struct{ b []byte }

func (d *decoder) timing() (Timing, error) {
	kind, err := d.byte()
	if err != nil {
		return nil, err
	}
	switch kind {
	case kindIdentity:
		return Identity{}, nil
	case kindShift:
		ms, err := d.varint()
		return Shift{Ms: ms}, err
	case kindLinear:
		scale, err := d.float()
		if err != nil {
			return nil, err
		}
		offset, err := d.varint()
		return Linear{Scale: scale, OffsetMs: offset}, err
	case kindPiecewise:
		return d.piecewise()
	default:
		return nil, fmt.Errorf("unknown kind %d", kind)
	}
}

func (d *decoder) piecewise() (Timing, error) {
	scale, err := d.float()
	if err != nil {
		return nil, err
	}
	n, k := binary.Uvarint(d.b)
	if k == 0 {
		return nil, errTruncated
	}
	if k < 0 {
		return nil, errOverflow
	}
	d.b = d.b[k:]
	if n > maxSegments || n*minSegmentBytes > uint64(len(d.b)) {
		return nil, fmt.Errorf("segment count %d exceeds the input or %d", n, maxSegments)
	}
	segs := make([]Segment, n)
	for i := range segs {
		if segs[i], err = d.segment(); err != nil {
			return nil, err
		}
	}
	return Piecewise{Scale: scale, Segments: segs}, nil
}

func (d *decoder) segment() (Segment, error) {
	var s Segment
	var err error
	for _, f := range []*int64{&s.FromMs, &s.ToMs, &s.OffsetMs} {
		if *f, err = d.varint(); err != nil {
			return s, err
		}
	}
	drop, err := d.byte()
	if err != nil {
		return s, err
	}
	if drop > 1 {
		return s, fmt.Errorf("drop byte %d", drop)
	}
	s.Drop = drop == 1
	return s, nil
}

func (d *decoder) byte() (byte, error) {
	if len(d.b) == 0 {
		return 0, errTruncated
	}
	c := d.b[0]
	d.b = d.b[1:]
	return c, nil
}

func (d *decoder) varint() (int64, error) {
	v, k := binary.Varint(d.b)
	if k == 0 {
		return 0, errTruncated
	}
	if k < 0 {
		return 0, errOverflow
	}
	d.b = d.b[k:]
	return v, nil
}

func (d *decoder) float() (float64, error) {
	if len(d.b) < 8 {
		return 0, errTruncated
	}
	v := math.Float64frombits(binary.BigEndian.Uint64(d.b))
	d.b = d.b[8:]
	return v, nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}
