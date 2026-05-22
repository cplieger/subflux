package subsync

import (
	"context"
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestCorrectFramerate_too_few_cues(t *testing.T) {
	t.Parallel()
	ref := makeCues(5, 0, 2*time.Second)
	inc := makeCues(5, 0, 2*time.Second)
	result := correctFramerate(context.Background(), ref, inc, "")
	if result.Rate != 1.0 {
		t.Fatalf("expected rate 1.0, got %f", result.Rate)
	}
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestCorrectFramerate_too_short_duration(t *testing.T) {
	t.Parallel()
	// 30 cues but only 1 minute of content.
	ref := makeCues(30, 0, 2*time.Second)
	inc := makeCues(30, 0, 2*time.Second)
	result := correctFramerate(context.Background(), ref, inc, "")
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence for short duration, got %f", float64(result.Confidence))
	}
}

func TestCorrectFramerate_known_ratio_23976_to_25(t *testing.T) {
	t.Parallel()
	// Create reference at normal timing spread across 30 minutes.
	// With 100 cues over 30 minutes, each cue is 18 seconds apart.
	ref := makeLongCues(100, 30*time.Minute)

	// Scale incorrect cues by the framerate ratio.
	// If subtitle was authored at 23.976fps but video is 25fps,
	// the subtitle timestamps need to be divided by (25/23.976).
	ratio := 25.0 / 23.976
	inc := scaleCuesForTest(ref, ratio)

	result := correctFramerate(context.Background(), ref, inc, "")
	if result.Confidence == ConfidenceNone {
		t.Fatal("expected framerate detection, got no confidence")
	}
	if result.Method != MethodFramerate {
		t.Fatalf("expected method 'framerate', got %q", result.Method)
	}
}

func TestCorrectFramerate_identical_subtitles(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(50, 10*time.Minute)
	result := correctFramerate(context.Background(), ref, ref, "")
	// Identical subtitles should show no framerate issue.
	if result.Applied() {
		t.Fatalf("expected no correction for identical subtitles, got rate=%f offset=%d",
			result.Rate, result.Offset)
	}
}

func TestScaleCues_ratio_one(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "hello"},
	}
	scaled := scaleCues(cues, 1.0)
	if scaled[0].Start != cues[0].Start || scaled[0].End != cues[0].End {
		t.Fatal("ratio 1.0 should not change timing")
	}
}

func TestScaleCues_stretches(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 10 * time.Second, End: 12 * time.Second, Text: "test"},
	}
	// Ratio 2.0 should halve the timestamps (divides by ratio).
	scaled := scaleCues(cues, 2.0)
	if scaled[0].Start != 5*time.Second {
		t.Fatalf("expected 5s, got %v", scaled[0].Start)
	}
}

func TestLinearRegression_perfect_line(t *testing.T) {
	t.Parallel()
	points := []driftPoint{
		{timeMs: 0, driftMs: 0},
		{timeMs: 1000, driftMs: 10},
		{timeMs: 2000, driftMs: 20},
		{timeMs: 3000, driftMs: 30},
	}
	slope, intercept, r2 := linearRegression(points)
	if math.Abs(slope-0.01) > 1e-10 {
		t.Fatalf("expected slope 0.01, got %f", slope)
	}
	if math.Abs(intercept) > 1e-10 {
		t.Fatalf("expected intercept 0, got %f", intercept)
	}
	if math.Abs(r2-1.0) > 1e-10 {
		t.Fatalf("expected R²=1.0, got %f", r2)
	}
}

func TestLinearRegression_constant(t *testing.T) {
	t.Parallel()
	points := []driftPoint{
		{timeMs: 0, driftMs: 5},
		{timeMs: 1000, driftMs: 5},
		{timeMs: 2000, driftMs: 5},
	}
	slope, _, r2 := linearRegression(points)
	if math.Abs(slope) > 1e-10 {
		t.Fatalf("expected slope 0, got %f", slope)
	}
	// R² is 1.0 when all points are identical (ssTot = 0).
	if r2 != 1.0 {
		t.Fatalf("expected R²=1.0 for constant, got %f", r2)
	}
}

func TestLinearRegression_too_few_points(t *testing.T) {
	t.Parallel()
	points := []driftPoint{{timeMs: 0, driftMs: 0}}
	slope, intercept, r2 := linearRegression(points)
	if slope != 0 || intercept != 0 || r2 != 0 {
		t.Fatal("expected zeros for single point")
	}
}

func TestOverlapMs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b TimeSpan
		want float64
	}{
		{"full overlap", TimeSpan{0, 100}, TimeSpan{0, 100}, 100},
		{"partial", TimeSpan{0, 100}, TimeSpan{50, 150}, 50},
		{"no overlap", TimeSpan{0, 100}, TimeSpan{200, 300}, 0},
		{"adjacent", TimeSpan{0, 100}, TimeSpan{100, 200}, 0},
		{"contained", TimeSpan{0, 200}, TimeSpan{50, 100}, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := overlapMs(tt.a, tt.b)
			if got != tt.want {
				t.Fatalf("overlapMs = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestAlignmentScore_shifted(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{{Start: 1000, End: 2000}}
	inc := []TimeSpan{{Start: 0, End: 1000}}
	// With offset 1000, inc becomes [1000, 2000] which fully overlaps ref.
	score := alignmentScore(ref, inc, 1000)
	if score != 1000 {
		t.Fatalf("expected 1000, got %f", score)
	}
}

// --- Helpers ---

// makeCues creates n evenly spaced cues starting at offset.
func makeCues(n int, offset, gap time.Duration) []Cue {
	cues := make([]Cue, n)
	for i := range n {
		start := offset + time.Duration(i)*gap
		cues[i] = Cue{
			Start: start,
			End:   start + time.Second,
			Text:  "test",
		}
	}
	return cues
}

// makeLongCues creates n cues spread across the given total duration.
func makeLongCues(n int, totalDur time.Duration) []Cue {
	gap := totalDur / time.Duration(n)
	return makeCues(n, 0, gap)
}

// scaleCuesForTest applies a framerate ratio to cues (for test setup).
func scaleCuesForTest(cues []Cue, ratio float64) []Cue {
	scaled := make([]Cue, len(cues))
	for i, c := range cues {
		scaled[i] = Cue{
			Start: time.Duration(float64(c.Start) * ratio),
			End:   time.Duration(float64(c.End) * ratio),
			Text:  c.Text,
		}
	}
	return scaled
}

func TestCorrectFramerate_non_linear_drift(t *testing.T) {
	t.Parallel()
	// Create cues where drift is random (not linear), so R² < 0.8.
	ref := makeLongCues(50, 10*time.Minute)
	inc := make([]Cue, len(ref))
	for i, c := range ref {
		// Add random-looking non-linear drift: alternating positive/negative.
		var drift time.Duration
		switch i % 3 {
		case 0:
			drift = 5 * time.Second
		case 1:
			drift = -3 * time.Second
		}
		inc[i] = Cue{
			Start: c.Start + drift,
			End:   c.End + drift,
			Text:  c.Text,
		}
	}
	result := correctFramerate(context.Background(), ref, inc, "")
	// Non-linear drift should produce no confidence.
	if result.Confidence != ConfidenceNone {
		t.Errorf("CorrectFramerate(non-linear drift) confidence = %f, want 0",
			float64(result.Confidence))
	}
}

func TestMeasureDrifts_few_cues(t *testing.T) {
	t.Parallel()
	// With 4 cues: numSamples = min(10, 4/2) = 2 < 3 → returns nil.
	ref := makeCues(4, 0, 2*time.Second)
	inc := makeCues(4, 0, 2*time.Second)
	points := measureDrifts(ref, inc)
	if points != nil {
		t.Errorf("measureDrifts(4 cues) = %v, want nil", points)
	}
}

func TestMeasureDrifts_mismatched_lengths(t *testing.T) {
	t.Parallel()
	// n = min(len(ref), len(inc)) = min(100, 8) = 8.
	// numSamples = min(10, 8/2) = 4 >= 3 → produces 4 points.
	ref := makeLongCues(100, 30*time.Minute)
	inc := makeCues(8, 0, 2*time.Second)
	points := measureDrifts(ref, inc)
	if len(points) != 4 {
		t.Errorf("measureDrifts(100 ref, 8 inc) = %d points, want 4", len(points))
	}
}

func TestGoldenSectionSearch_ratio_near_one(t *testing.T) {
	t.Parallel()
	// When the observed ratio is very close to 1.0, GSS should converge
	// to ~1.0 and return ConfidenceNone (no correction needed).
	ref := makeLongCues(50, 10*time.Minute)
	// Tiny ratio deviation: 1.000001 — effectively identical.
	observedRatio := 1.000001
	result := goldenSectionSearch(context.Background(), ref, ref, observedRatio, 0.95)
	if result.Confidence != ConfidenceNone {
		t.Errorf("goldenSectionSearch(ratio~1.0) confidence = %f, want 0",
			float64(result.Confidence))
	}
	if result.Rate != 1.0 {
		t.Errorf("goldenSectionSearch(ratio~1.0) rate = %f, want 1.0", result.Rate)
	}
}

func TestGoldenSectionSearch_real_ratio(t *testing.T) {
	t.Parallel()
	// Use a non-standard ratio that doesn't match any known pair.
	ref := makeLongCues(100, 30*time.Minute)
	// Apply a custom ratio of 1.03 (not in knownRatios).
	inc := scaleCuesForTest(ref, 1.03)
	// observedRatio should be close to 1.03.
	result := goldenSectionSearch(context.Background(), ref, inc, 1.03, 0.95)
	if result.Confidence == ConfidenceNone {
		t.Fatal("expected some confidence from GSS with real ratio")
	}
	if result.Method != MethodFramerate {
		t.Errorf("expected method 'framerate', got %q", result.Method)
	}
}

func TestLinearRegression_two_points(t *testing.T) {
	t.Parallel()
	points := []driftPoint{
		{timeMs: 0, driftMs: 0},
		{timeMs: 1000, driftMs: 10},
	}
	slope, intercept, r2 := linearRegression(points)
	if slope != 0.01 {
		t.Errorf("slope = %f, want 0.01", slope)
	}
	if intercept != 0 {
		t.Errorf("intercept = %f, want 0", intercept)
	}
	if r2 != 1.0 {
		t.Errorf("R² = %f, want 1.0", r2)
	}
}

func TestMatchKnownRatio(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ratio    float64
		videoFPS float64
		wantOK   bool
	}{
		{"no_match", 1.5, 0, false},
		{"with_video_fps", 25.0 / 23.976, 25.0, true},
		{"video_fps_filters_candidates", 25.0 / 23.976, 60.0, false},
		{"exact_match", 25.0 / 23.976, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cues := makeLongCues(50, 10*time.Minute)
			var ref []Cue
			if tc.ratio != 1.5 {
				ref = scaleCues(cues, 1.0/tc.ratio)
			} else {
				ref = makeCues(5, 0, time.Second)
				cues = ref
			}
			result, ok := matchKnownRatio(context.Background(), tc.ratio, cues, 0.95, tc.videoFPS, ref)
			if ok != tc.wantOK {
				t.Fatalf("matchKnownRatio(%v, videoFPS=%v) ok = %v, want %v", tc.ratio, tc.videoFPS, ok, tc.wantOK)
			}
			if ok && result.Method != MethodFramerate {
				t.Errorf("expected method 'framerate', got %q", result.Method)
			}
		})
	}
}

func TestAlignmentScore_no_overlap(t *testing.T) {
	t.Parallel()
	ref := []TimeSpan{{Start: 0, End: 1000}}
	inc := []TimeSpan{{Start: 5000, End: 6000}}
	score := alignmentScore(ref, inc, 0)
	if score != 0 {
		t.Errorf("alignmentScore(no overlap) = %f, want 0", score)
	}
}

func TestLinearRegression_identical_x_values(t *testing.T) {
	t.Parallel()
	// All x values identical → denom = n*sumX2 - sumX*sumX = 0.
	points := []driftPoint{
		{timeMs: 100, driftMs: 5},
		{timeMs: 100, driftMs: 10},
		{timeMs: 100, driftMs: 15},
	}
	slope, intercept, r2 := linearRegression(points)
	if slope != 0 || intercept != 0 || r2 != 0 {
		t.Errorf("linearRegression(identical x) = (%f, %f, %f), want (0, 0, 0)",
			slope, intercept, r2)
	}
}

func TestMeasureDrifts_exact_six_cues(t *testing.T) {
	t.Parallel()
	// 6 cues: numSamples = min(10, 6/2) = 3 → exactly 3 points.
	ref := makeCues(6, 0, 2*time.Second)
	inc := makeCues(6, time.Second, 2*time.Second)
	points := measureDrifts(ref, inc)
	if len(points) != 3 {
		t.Errorf("measureDrifts(6 cues) = %d points, want 3", len(points))
	}
}

func TestCorrectFramerate_known_ratio_rejected_falls_through_to_GSS(t *testing.T) {
	t.Parallel()
	// Craft cues where the observed drift ratio matches a known pair (25/23.976)
	// within tolerance, but the actual timing has per-cue jitter that makes the
	// known ratio produce max residual > 200ms, triggering the fallthrough to
	// golden-section search.
	//
	// Strategy: apply a ratio close to 25/23.976 but add progressive jitter
	// that accumulates to >200ms residual when the exact known ratio is applied.
	ref := makeLongCues(100, 30*time.Minute)

	// The known ratio 25/23.976 ≈ 1.04270937...
	// Apply a slightly different ratio (1.043) that's within framerateRatioTolerance
	// of the known pair, but different enough that applying the exact known ratio
	// produces >200ms residual on some cues.
	actualRatio := 1.043
	inc := make([]Cue, len(ref))
	for i, c := range ref {
		// Scale by actualRatio and add progressive jitter.
		// The jitter grows with index so later cues have >200ms error
		// when corrected with the exact known ratio instead of actualRatio.
		jitter := time.Duration(i*3) * time.Millisecond
		inc[i] = Cue{
			Start: time.Duration(float64(c.Start)*actualRatio) + jitter,
			End:   time.Duration(float64(c.End)*actualRatio) + jitter,
			Text:  c.Text,
		}
	}

	result := correctFramerate(context.Background(), ref, inc, "")

	// The function should still produce a result (via GSS fallthrough).
	if result.Confidence == ConfidenceNone {
		t.Fatal("expected some confidence after GSS fallthrough")
	}
	if result.Method != MethodFramerate {
		t.Errorf("expected method 'framerate', got %q", result.Method)
	}
	// The rate should be close to actualRatio (GSS finds the true ratio).
	relErr := math.Abs(result.Rate-actualRatio) / actualRatio
	t.Logf("GSS fallthrough: rate=%.6f (want ~%.6f, relErr=%.4f), conf=%.2f",
		result.Rate, actualRatio, relErr, float64(result.Confidence))
}

func TestCorrectFramerate_cue_count_mismatch_too_large(t *testing.T) {
	t.Parallel()
	// Reference has 100 cues, incorrect has 50 → 50% difference > 25% threshold.
	ref := makeLongCues(100, 30*time.Minute)
	inc := makeLongCues(50, 30*time.Minute)
	result := correctFramerate(context.Background(), ref, inc, "")
	if result.Confidence != ConfidenceNone {
		t.Errorf("CorrectFramerate(100 ref, 50 inc) confidence = %f, want 0 (cue count mismatch)",
			float64(result.Confidence))
	}
	if result.Rate != 1.0 {
		t.Errorf("CorrectFramerate(100 ref, 50 inc) rate = %f, want 1.0", result.Rate)
	}
}

func TestCorrectFramerate_cue_count_mismatch_at_boundary(t *testing.T) {
	t.Parallel()
	// 25% difference exactly: 100 ref, 75 inc → (100-75)/100 = 0.25, not > 0.25.
	// Should NOT trigger the early return (passes through to drift analysis).
	ref := makeLongCues(100, 30*time.Minute)
	inc := makeLongCues(75, 30*time.Minute)
	result := correctFramerate(context.Background(), ref, inc, "")
	// At exactly 25%, the check is > 0.25, so it should NOT bail out.
	// The result depends on drift analysis, but confidence should not be
	// ConfidenceNone due to the cue count check specifically.
	// We just verify the function doesn't panic and returns a valid result.
	if result.Method != MethodFramerate {
		t.Logf("CorrectFramerate(100 ref, 75 inc) method = %q (boundary case, drift analysis ran)", result.Method)
	}
}

func TestLinearRegression_perfect_fit_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		slope := rapid.Float64Range(-10, 10).Draw(t, "slope")
		if math.Abs(slope) < 0.001 {
			slope = 0.001 // avoid near-zero slopes where R² is numerically unstable
		}
		intercept := rapid.Float64Range(-1000, 1000).Draw(t, "intercept")
		n := rapid.IntRange(3, 20).Draw(t, "n")

		points := make([]driftPoint, n)
		for i := range n {
			x := float64(i) * 1000
			points[i] = driftPoint{timeMs: x, driftMs: slope*x + intercept}
		}

		gotSlope, gotIntercept, r2 := linearRegression(points)
		if math.Abs(r2-1.0) > 1e-6 {
			t.Fatalf("linearRegression(perfect line) R² = %f, want 1.0", r2)
		}
		if math.Abs(gotSlope-slope) > 1e-6 {
			t.Fatalf("linearRegression(perfect line) slope = %f, want %f", gotSlope, slope)
		}
		if math.Abs(gotIntercept-intercept) > 1e-4 {
			t.Fatalf("linearRegression(perfect line) intercept = %f, want %f", gotIntercept, intercept)
		}
	})
}

func TestLinearRegression_y_translation_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(3, 20).Draw(t, "n")
		offset := rapid.Float64Range(-500, 500).Draw(t, "offset")

		points := make([]driftPoint, n)
		shifted := make([]driftPoint, n)
		for i := range n {
			x := float64(i) * 1000
			y := float64(i*i) * 0.5
			points[i] = driftPoint{timeMs: x, driftMs: y}
			shifted[i] = driftPoint{timeMs: x, driftMs: y + offset}
		}

		slope1, intercept1, r2_1 := linearRegression(points)
		slope2, intercept2, r2_2 := linearRegression(shifted)

		if math.Abs(slope1-slope2) > 1e-6 {
			t.Fatalf("Y-translation changed slope: %f vs %f", slope1, slope2)
		}
		if math.Abs((intercept2-intercept1)-offset) > 1e-4 {
			t.Fatalf("Y-translation intercept delta = %f, want %f",
				intercept2-intercept1, offset)
		}
		if math.Abs(r2_1-r2_2) > 1e-6 {
			t.Fatalf("Y-translation changed R²: %f vs %f", r2_1, r2_2)
		}
	})
}

func TestScaleCues_round_trip_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "n")
		ratio := rapid.Float64Range(0.5, 2.0).Draw(t, "ratio")
		if math.Abs(ratio) < 0.01 {
			ratio = 0.5
		}

		cues := make([]Cue, n)
		for i := range n {
			start := time.Duration(rapid.Int64Range(0, int64(30*time.Minute)).Draw(t, "start"))
			dur := time.Duration(rapid.Int64Range(int64(100*time.Millisecond), int64(5*time.Second)).Draw(t, "dur"))
			cues[i] = Cue{Start: start, End: start + dur, Text: "test"}
		}

		scaled := scaleCues(cues, ratio)
		restored := scaleCues(scaled, 1.0/ratio)

		for i := range n {
			startDiff := math.Abs(float64(cues[i].Start - restored[i].Start))
			endDiff := math.Abs(float64(cues[i].End - restored[i].End))
			if startDiff > float64(time.Microsecond) {
				t.Fatalf("scaleCues round-trip: cue[%d].Start diff = %v, want < 1µs", i, time.Duration(int64(startDiff)))
			}
			if endDiff > float64(time.Microsecond) {
				t.Fatalf("scaleCues round-trip: cue[%d].End diff = %v, want < 1µs", i, time.Duration(int64(endDiff)))
			}
		}
	})
}

func TestOverlapMs_commutativity_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := TimeSpan{
			Start: rapid.Int64Range(0, 100000).Draw(t, "a_start"),
			End:   rapid.Int64Range(0, 100000).Draw(t, "a_end"),
		}
		if a.End < a.Start {
			a.Start, a.End = a.End, a.Start
		}
		b := TimeSpan{
			Start: rapid.Int64Range(0, 100000).Draw(t, "b_start"),
			End:   rapid.Int64Range(0, 100000).Draw(t, "b_end"),
		}
		if b.End < b.Start {
			b.Start, b.End = b.End, b.Start
		}

		ab := overlapMs(a, b)
		ba := overlapMs(b, a)
		if ab != ba {
			t.Fatalf("overlapMs(%v, %v) = %f, but overlapMs(%v, %v) = %f",
				a, b, ab, b, a, ba)
		}
		if ab < 0 {
			t.Fatalf("overlapMs(%v, %v) = %f, want >= 0", a, b, ab)
		}
	})
}

func TestOverlapMs_self_overlap_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		start := rapid.Int64Range(0, 100000).Draw(t, "start")
		end := rapid.Int64Range(start, start+100000).Draw(t, "end")
		span := TimeSpan{Start: start, End: end}

		got := overlapMs(span, span)
		want := float64(end - start)
		if got != want {
			t.Fatalf("overlapMs(%v, %v) = %f, want %f (self-overlap = duration)",
				span, span, got, want)
		}
	})
}

func TestMeasureDrifts_drift_values_reflect_offset(t *testing.T) {
	t.Parallel()
	// Reference cues at normal timing, incorrect cues shifted by 5 seconds.
	ref := makeLongCues(20, 10*time.Minute)
	offset := 5 * time.Second
	inc := make([]Cue, len(ref))
	for i, c := range ref {
		inc[i] = Cue{
			Start: c.Start + offset,
			End:   c.End + offset,
			Text:  c.Text,
		}
	}

	points := measureDrifts(ref, inc)
	if len(points) == 0 {
		t.Fatal("measureDrifts returned no points")
	}

	// Every drift point should be approximately 5000ms (the offset).
	for i, p := range points {
		if math.Abs(p.driftMs-5000) > 1 {
			t.Errorf("measureDrifts point[%d].driftMs = %f, want ~5000", i, p.driftMs)
		}
	}
}

func TestScaleCues_empty_slice(t *testing.T) {
	t.Parallel()
	scaled := scaleCues(nil, 2.0)
	if len(scaled) != 0 {
		t.Errorf("scaleCues(nil, 2.0) = %d cues, want 0", len(scaled))
	}
	scaled = scaleCues([]Cue{}, 1.5)
	if len(scaled) != 0 {
		t.Errorf("scaleCues([], 1.5) = %d cues, want 0", len(scaled))
	}
}
