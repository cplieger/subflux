package timing

import (
	"fmt"
	"math"
	"slices"
	"strconv"
)

// The latest time each format's stamp writes: SRT up to maxSRTHour, ASS
// with its one hour digit.
const (
	maxSRTMs = ((maxSRTHour*60+59)*60+59)*1000 + 999
	maxASSMs = ((9*60+59)*60+59)*1000 + 990
)

// Render writes c's document under t, rewriting only timestamp spans. A cue
// mapped to end at or before 0, or starting in a Drop segment, is removed
// with its SRT block or ASS line, and the remaining SRT indexes renumber from
// 1; one mapped to start before 0 starts at 0. Times round to 1 ms for SRT
// and 10 ms for ASS and SSA; a stamp the mapping leaves unchanged keeps its
// bytes. It refuses, as ErrTimingInvalid, a t failing Validate and a time
// past the format's last stamp (ASS 9:59:59.99), and, as
// ErrFormatUnsupported, the zero Cues.
func Render(c Cues, t Timing) ([]byte, error) {
	mapped, err := mapEvents(c, t)
	if err != nil {
		return nil, err
	}
	renumber := slices.ContainsFunc(mapped, func(m mappedCue) bool { return !m.keep })
	out := make([]byte, 0, len(c.doc))
	pos, n := 0, 0
	for i, e := range c.events {
		m := mapped[i]
		out = append(out, c.doc[pos:e.block.from]...)
		pos = e.block.from
		if !m.keep {
			pos = e.block.to
			continue
		}
		n++
		if renumber && e.index.to > e.index.from {
			out = append(out, c.doc[pos:e.index.from]...)
			out = appendASCII(out, strconv.Itoa(n), c.enc)
			pos = e.index.to
		}
		out, pos = c.appendStamp(out, pos, e.start, m.startMs, m.startMs != e.startMs)
		out, pos = c.appendStamp(out, pos, e.end, m.endMs, m.endMs != e.endMs)
	}
	return append(out, c.doc[pos:]...), nil
}

// appendStamp copies the document up to stamp, then the stamp's own bytes,
// or ms in the format's stamp when changed, and returns the new position.
func (c Cues) appendStamp(out []byte, pos int, stamp span, ms int64, changed bool) (_ []byte, next int) {
	out = append(out, c.doc[pos:stamp.from]...)
	if !changed {
		return append(out, c.doc[stamp.from:stamp.to]...), stamp.to
	}
	h, m, s, f := ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000
	if c.format == SRT {
		return appendASCII(out, fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, f), c.enc), stamp.to
	}
	return appendASCII(out, fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, f/10), c.enc), stamp.to
}

// mappedCue is one cue's times under a Timing, rounded and clamped; keep is
// false for a removed cue.
type mappedCue struct {
	startMs, endMs int64
	keep           bool
}

// mapEvents maps every cue of c under t: Render and RenderVTT read the same
// answer, so the preview never differs from the file.
func mapEvents(c Cues, t Timing) ([]mappedCue, error) {
	if c.format == 0 {
		return nil, fmt.Errorf("%w: no document", ErrFormatUnsupported)
	}
	if err := Validate(t); err != nil {
		return nil, err
	}
	unit, maxMs := int64(1), int64(maxSRTMs)
	if c.format != SRT {
		unit, maxMs = 10, maxASSMs
	}
	out := make([]mappedCue, len(c.events))
	for i, e := range c.events {
		scale, offset, keep := segmentOf(t, e.startMs)
		if !keep {
			continue
		}
		m := mappedCue{
			startMs: roundTo(float64(e.startMs)*scale+float64(offset), unit),
			endMs:   roundTo(float64(e.endMs)*scale+float64(offset), unit),
			keep:    true,
		}
		if m.endMs <= 0 {
			continue
		}
		m.startMs = max(m.startMs, 0)
		for _, ms := range [...]struct{ mapped, baseline int64 }{{m.startMs, e.startMs}, {m.endMs, e.endMs}} {
			if ms.mapped != ms.baseline && ms.mapped > maxMs {
				return nil, fmt.Errorf("%w: a cue maps to %d ms, past the format's last time %d ms", ErrTimingInvalid, ms.mapped, maxMs)
			}
		}
		out[i] = m
	}
	return out, nil
}

// segmentOf answers t's scale and offset for a cue starting at baseline
// startMs, and false when a Drop segment removes it. Membership is by the
// cue's start, so the cue's end maps with the same segment.
func segmentOf(t Timing, startMs int64) (scale float64, offsetMs int64, keep bool) {
	switch v := t.(type) {
	case Shift:
		return 1, v.Ms, true
	case Linear:
		return v.Scale, v.OffsetMs, true
	case Piecewise:
		// Validate guarantees chained segments from 0, the last open-ended.
		k, _ := slices.BinarySearchFunc(v.Segments[:len(v.Segments)-1], startMs, func(s Segment, t int64) int {
			if s.ToMs <= t {
				return -1
			}
			return 1
		})
		s := v.Segments[k]
		return v.Scale, s.OffsetMs, !s.Drop
	default:
		return 1, 0, true
	}
}

// roundTo rounds x ms to the nearest multiple of unit, halves away from 0.
// Rounding once from the exact value keeps every time within unit/2 of it.
func roundTo(x float64, unit int64) int64 {
	return int64(math.Round(x/float64(unit))) * unit
}
