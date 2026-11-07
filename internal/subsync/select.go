package subsync

import (
	"context"

	"github.com/cplieger/subflux/internal/timing"
)

// pwFit is the piecewise stage's fit and its held-out scores.
type pwFit struct {
	timing timing.Piecewise
	// heldOut is the fit's e2 on the odd cues when fitted on the even ones;
	// global is the global model's e2 on the same odd cues.
	heldOut, global float64
	ok              bool
}

// accepted reports whether the piecewise fit replaces the global one: a
// mapping Validate accepts whose held-out e2 beats the global model's by
// heldOutGain.
func (f pwFit) accepted() bool { return f.ok && f.heldOut >= f.global+heldOutGain }

// piecewiseStage fits the DP on inc (baseline, sorted by start) scaled by
// the selected global model's ratio, and scores it held out: refit on the
// even cues at half the penalty, each odd cue moved by its preceding even
// cue's offset or dropped with it.
func piecewiseStage(ctx context.Context, d *dp, inc []Span, scale float64, globalOffset int64, x refIndex) (pwFit, error) {
	penalty := splitPenaltyPer * float64(min(len(d.ref), len(inc)))
	offs, err := d.fit(ctx, scaleSpans(inc, scale), penalty)
	if err != nil {
		return pwFit{}, err
	}
	pw, ok := piecewiseOf(inc, offs, scale)
	var even, odd []Span
	for i, s := range inc {
		if i%2 == 0 {
			even = append(even, s)
		} else {
			odd = append(odd, s)
		}
	}
	if len(odd) == 0 {
		return pwFit{}, nil
	}
	evenOffs, err := d.fit(ctx, scaleSpans(even, scale), penalty/2)
	if err != nil {
		return pwFit{}, err
	}
	oddScaled := scaleSpans(odd, scale)
	var moved []Span
	for j, s := range oddScaled {
		if off := evenOffs[j]; off != droppedOffset {
			moved = append(moved, Span{s.StartMs + off, s.EndMs + off})
		}
	}
	heldOut, _ := measure(fit{kept: moved, displayMs: displayMs(oddScaled)}, x)
	global, _ := measure(fit{kept: shiftSpans(oddScaled, globalOffset), displayMs: displayMs(oddScaled)}, x)
	return pwFit{timing: pw, heldOut: heldOut, global: global, ok: ok}, nil
}

// result is the accepted fit as a Result, its Shape measured over every
// cue of inc with the dropped ones in the denominator.
func (f pwFit) result(inc []Span, x refIndex) Result {
	_, sh := measure(fit{kept: applyPiecewise(inc, f.timing), displayMs: displayMs(scaleSpans(inc, f.timing.Scale))}, x)
	sh.InsertedMs = insertedMs(inc, f.timing)
	return Result{Timing: f.timing, Method: timing.MethodReference, Confidence: f.heldOut, Shape: sh, Measured: true}
}
