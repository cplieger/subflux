package subsync

import (
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/subflux/internal/timing"
)

// Model is the hypothesis the pipeline selected, and AlignReference's ceiling.
type Model uint8

// The models, in the order the pipeline tries them.
const (
	ModelNone Model = iota // the guard refused an input
	ModelShift
	ModelLinear
	ModelPiecewise
)

// Result is one fit. Shape is measured against the reference whenever one
// passed the guard, for an audio fit too; Measured is false when none did.
type Result struct {
	Timing     timing.Timing // canonical form
	Shape      Shape
	Confidence float64 // e2, held out for piecewise; peak × 0.9 for audio
	Method     timing.Method
	Measured   bool
}

// noReference is the result when the guard refuses: the reference counts
// as absent.
var noReference = Result{Timing: timing.Identity{}, Method: timing.MethodNone}

// AlignReference is shift, then linear, then (upTo ModelPiecewise only)
// piecewise with a drop state on the selected global model's scale; every
// non-Drop segment it emits holds a kept cue's start. More than maxSpans
// spans on either side, fewer than minSpans, or a reference speech coverage
// outside (0.05, 0.95) is ModelNone. A context ended before or during a
// stage returns its error and no result.
func AlignReference(ctx context.Context, ref, inc []Span, upTo Model) (Result, error) {
	if upTo < ModelShift || upTo > ModelPiecewise {
		return Result{}, fmt.Errorf("subsync: AlignReference up to model %d", upTo)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	x := newRefIndex(ref)
	if !guard(len(ref)) || !guard(len(inc)) {
		return noReference, nil
	}
	if c := x.coverage(); c <= 0.05 || c >= 0.95 {
		return noReference, nil
	}
	incS := sortSpans(inc)
	global, pw, err := stages(ctx, sortSpans(ref), incS, x, upTo)
	if err != nil {
		return Result{}, err
	}
	if pw.accepted() {
		return pw.result(incS, x), nil
	}
	return global, nil
}

// stages runs the global stages and, up to ModelPiecewise, the piecewise
// stage on the selected global model's scale, over ref and inc sorted by
// start.
func stages(ctx context.Context, ref, inc []Span, x refIndex, upTo Model) (Result, pwFit, error) {
	t, scale, err := globalFit(ctx, ref, inc, upTo)
	if err != nil {
		return Result{}, pwFit{}, err
	}
	scaled := scaleSpans(inc, scale)
	conf, sh := measure(fit{kept: shiftSpans(scaled, offsetOf(t)), displayMs: displayMs(scaled)}, x)
	global := Result{Timing: timing.Canonical(t), Method: timing.MethodReference, Confidence: conf, Shape: sh, Measured: true}
	if upTo < ModelPiecewise {
		return global, pwFit{}, nil
	}
	pw, err := piecewiseStage(ctx, newDP(productionGrid, ref), inc, scale, offsetOf(t), x)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return Result{}, pwFit{}, err
	}
	return global, pw, nil
}

// globalFit is the shift stage and, up to ModelLinear or past it, the
// linear stage: the best ratio's fit wins only at linearMargin times the
// shift's rating.
func globalFit(ctx context.Context, ref, inc []Span, upTo Model) (timing.Timing, float64, error) {
	w := newSweeper(windowMs)
	offset, shiftRating, err := w.best(ctx, ref, inc)
	if err != nil {
		return nil, 0, err
	}
	var t timing.Timing = timing.Shift{Ms: offset}
	scale := 1.0
	if upTo < ModelLinear {
		return t, scale, nil
	}
	best := 0.0
	for _, r := range ratios {
		o, rating, err := w.best(ctx, ref, scaleSpans(inc, r))
		if err != nil {
			return nil, 0, err
		}
		if rating > best && rating >= linearMargin*shiftRating {
			best, t, scale = rating, timing.Linear{Scale: r, OffsetMs: o}, r
		}
	}
	return t, scale, nil
}

func guard(n int) bool { return n >= minSpans && n <= maxSpans }

func sortSpans(spans []Span) []Span {
	out := slices.Clone(spans)
	slices.SortStableFunc(out, func(a, b Span) int { return byStart(a, b.StartMs) })
	return out
}

func offsetOf(t timing.Timing) int64 {
	switch t := t.(type) {
	case timing.Shift:
		return t.Ms
	case timing.Linear:
		return t.OffsetMs
	}
	return 0
}

func displayMs(spans []Span) int64 {
	var d int64
	for _, s := range spans {
		d += max(0, s.EndMs-s.StartMs)
	}
	return d
}
