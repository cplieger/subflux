package subsync

import (
	"context"
	"math"
	"math/cmplx"
	"sync"

	"subflux/internal/subsync/fft"
)

// frameMs is the analysis frame duration in milliseconds.
// Used by energy envelope, subtitle envelope, dialogue mask, and correlation.
const frameMs = 10

// maxCorrelationFrames caps the signal length for FFT correlation to prevent
// excessive memory usage. At 10ms/frame, 500k frames = ~1.4 hours.
const maxCorrelationFrames = 500_000

// correlationResult holds the output of cross-correlation.
type correlationResult struct {
	OffsetFrames int     // best offset in frames (positive = subtitle is early)
	OffsetMs     int64   // best offset in milliseconds
	Peak         float64 // normalized correlation peak (0.0 to 1.0)
}

// CrossCorrelateEdges correlates two float64 signals (typically GMM VAD
// speech probability or energy envelope values) using FFT-based convolution
// to find the offset that maximizes alignment. Input signals are expected
// to already be in bipolar form (centered around zero) so that mismatches
// produce negative scores.
func crossCorrelateEdges(ctx context.Context, a, b []float64) correlationResult {
	if len(a) == 0 || len(b) == 0 {
		return correlationResult{}
	}
	if ctx.Err() != nil {
		return correlationResult{}
	}
	if len(a) > maxCorrelationFrames {
		a = a[:maxCorrelationFrames]
	}
	if len(b) > maxCorrelationFrames {
		b = b[:maxCorrelationFrames]
	}
	return correlateFloat(ctx, a, b)
}

// correlateFloat performs FFT cross-correlation on two float64 signals.
// It computes the correlation in the frequency domain (multiplication of
// FFT(a) with conj(FFT(b)), then IFFT) and finds the offset that maximizes
// alignment. Parabolic interpolation on the peak provides sub-frame precision.
func correlateFloat(ctx context.Context, fa, fb []float64) correlationResult {
	n := fft.NextPow2(len(fa) + len(fb) - 1)

	ws := fft.WorkspacePool.Get().(*fft.Workspace) //nolint:errcheck // pool always returns *Workspace from New
	ws.Ensure(n)

	// Fill workspace buffers with zero-padded signal data.
	for i := range ws.A {
		ws.A[i] = 0
	}
	for i, v := range fa {
		ws.A[i] = complex(v, 0)
	}
	for i := range ws.B {
		ws.B[i] = 0
	}
	for i, v := range fb {
		ws.B[i] = complex(v, 0)
	}

	// Run the two independent FFTs in parallel since they operate on
	// separate buffers with no shared state.
	var wg sync.WaitGroup
	wg.Go(func() { fft.FFT(ws.A) })
	wg.Go(func() { fft.FFT(ws.B) })
	wg.Wait()

	// Check for cancellation after the parallel FFTs complete.
	if ctx.Err() != nil {
		fft.WorkspacePool.Put(ws)
		return correlationResult{}
	}

	for i := range ws.Product {
		ws.Product[i] = ws.A[i] * cmplx.Conj(ws.B[i])
	}

	corr := fft.IFFT(ws.Product)

	maxOffset := min(len(fa), len(fb))
	bestIdx, bestVal := findPeakOffset(corr, n, maxOffset)

	offset := bestIdx
	if offset > n/2 {
		offset -= n
	}

	normA := floatEnergy(fa)
	normB := floatEnergy(fb)
	var peak float64
	if normA > 0 && normB > 0 {
		peak = bestVal / math.Sqrt(normA*normB)
	}
	peak = max(0, min(1, peak))

	offsetMs := parabolicRefine(corr, bestIdx, offset, n, bestVal)

	fft.WorkspacePool.Put(ws)

	return correlationResult{
		OffsetFrames: offset,
		OffsetMs:     int64(math.Round(offsetMs)),
		Peak:         peak,
	}
}

// findPeakOffset searches the correlation array for the best offset,
// considering both positive offsets (b shifted right) and negative
// offsets (b shifted left, wrapped around in the FFT output).
func findPeakOffset(corr []complex128, n, maxOffset int) (bestIdx int, bestVal float64) {
	bestVal = math.Inf(-1)
	// Positive offsets (b shifted right).
	for i := range min(maxOffset, len(corr)) {
		if v := real(corr[i]); v > bestVal {
			bestVal = v
			bestIdx = i
		}
	}
	// Negative offsets (b shifted left).
	for i := max(0, n-maxOffset); i < n && i < len(corr); i++ {
		if v := real(corr[i]); v > bestVal {
			bestVal = v
			bestIdx = i
		}
	}
	return bestIdx, bestVal
}

// parabolicRefine fits a parabola through the peak and its two circular
// neighbors to find the fractional offset that maximizes correlation.
// Returns the refined offset in milliseconds.
func parabolicRefine(corr []complex128, bestIdx, offset, n int, bestVal float64) float64 {
	offsetFrac := float64(offset)
	prev := bestIdx - 1
	next := bestIdx + 1
	if prev < 0 {
		prev += n
	}
	if next >= n {
		next -= n
	}
	vPrev := real(corr[prev])
	vNext := real(corr[next])
	denom := 2 * (2*bestVal - vPrev - vNext)
	if denom > 1e-10 {
		delta := (vPrev - vNext) / denom
		if delta > -0.5 && delta < 0.5 {
			offsetFrac = float64(offset) + delta
		}
	}
	return offsetFrac * float64(frameMs)
}

// --- Signal helpers ---

// floatEnergy returns the sum of squared values (L2 norm squared) of the signal.
func floatEnergy(signal []float64) float64 {
	var sum float64
	for _, v := range signal {
		sum += v * v
	}
	return sum
}
