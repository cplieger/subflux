package subsync

import (
	"context"
	"fmt"

	"github.com/cplieger/subflux/internal/subsync/vad"
	"github.com/cplieger/subflux/internal/timing"
)

// Speech is the audio path's input: one vad.Detector LLR per 10 ms frame of
// the video's first audio stream, at most MaxAudioFrames.
type Speech struct{ LLR []float64 }

// The audio fit's bounds: an offset past 10% of the duration, clamped to
// 30 s to 5 minutes, is no fit; the correlation peak is worth at most 0.9.
const (
	audioOffsetPct   = 10
	minAudioOffsetMs = 30_000
	maxAudioOffsetMs = 300_000
	audioPeakWeight  = 0.9
)

// AlignAudio returns a Shift with Confidence peak × 0.9 from correlating the
// safe and precise speech decisions with inc's dialogue, the precise one
// when the two offsets agree within audioAgreementMs. Fewer than minSpans
// spans, no frames, or an offset past the duration's limit is ModelNone.
// With a non-empty ref (one that passed AlignReference's guard) it measures
// the shift's Shape against it, so Settle guards the audio fit as it guards
// a reference one. durationMs is the video's duration.
func AlignAudio(ctx context.Context, speech Speech, ref, inc []Span, durationMs int64) (Result, error) {
	if durationMs <= 0 {
		return Result{}, fmt.Errorf("subsync: AlignAudio over a %d ms video", durationMs)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	none := Result{Timing: timing.Identity{}, Method: timing.Audio}
	llr := speech.LLR[:min(len(speech.LLR), MaxAudioFrames)]
	if len(inc) < minSpans || len(llr) == 0 {
		return none, nil
	}
	safe, precise := make([]float64, len(llr)), make([]float64, len(llr))
	ds, dp := vad.NewDecider(safeThreshold, safeOverhang), vad.NewDecider(preciseThreshold, preciseOverhang)
	for i, v := range llr {
		safe[i], precise[i] = bipolar(ds.Speech(v)), bipolar(dp.Speech(v))
	}
	sub := dialogueSignal(inc, len(llr))
	corr := crossCorrelateEdges(ctx, safe, sub)
	prec := crossCorrelateEdges(ctx, precise, sub)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if d := corr.OffsetMs - prec.OffsetMs; d >= -audioAgreementMs && d <= audioAgreementMs {
		corr = prec
	}
	limit := min(max(durationMs*audioOffsetPct/100, minAudioOffsetMs), maxAudioOffsetMs)
	if corr.OffsetMs > limit || corr.OffsetMs < -limit {
		return none, nil
	}
	r := Result{
		Timing:     timing.Canonical(timing.Shift{Ms: corr.OffsetMs}),
		Method:     timing.Audio,
		Confidence: corr.Peak * audioPeakWeight,
	}
	if len(ref) > 0 {
		_, r.Shape = measure(fit{kept: shiftSpans(inc, corr.OffsetMs), displayMs: displayMs(inc)}, newRefIndex(ref))
		r.Measured = true
	}
	return r, nil
}

func bipolar(speech bool) float64 {
	if speech {
		return 1
	}
	return -1
}

// dialogueSignal is +1 in each of n frames some span covers, -1 elsewhere:
// a difference array over the span ends, so its cost is n plus the spans.
func dialogueSignal(spans []Span, n int) []float64 {
	edges := make([]int, n+1)
	for _, s := range spans {
		from, to := max(s.StartMs/frameMs, 0), min(s.EndMs/frameMs, int64(n))
		if from < to {
			edges[from]++
			edges[to]--
		}
	}
	sig := make([]float64, n)
	covered := 0
	for i := range sig {
		covered += edges[i]
		sig[i] = bipolar(covered > 0)
	}
	return sig
}
