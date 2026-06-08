package subsync

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync/crosslang"
)

// Compile-time assertion: crosslang.MinCuesForSync must equal subsync.MinCuesForSync.
// This prevents silent drift between the two packages' constants.
var _ [MinCuesForSync]struct{} = [crosslang.MinCuesForSync]struct{}{}

// Test files: embedded English SRT (reference) + original French SRT (incorrect).
// The French subs are from a different source (720p WEB-DL) than the video
// (1080p WEBRip), so there's a small constant offset (~0-10s).
// The goal: each strategy should find an offset close to the true value.

var crosslangTestCases = []struct {
	name   string
	enFile string
	frFile string
}{
	{"S01E01", "30rock_s01e01_en.srt", "30rock_s01e01_fr.srt"},
	{"S01E02", "30rock_s01e02_en.srt", "30rock_s01e02_fr.srt"},
	{"S02E06", "30rock_s02e06_en.srt", "30rock_s02e06_fr.srt"},
}

func loadTestCues(t *testing.T, filename string) []Cue {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test file not found: %s", path)
	}
	data = NormalizeEncoding(data)
	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	if len(cues) == 0 {
		t.Fatalf("no cues in %s", filename)
	}
	return cues
}

// TestCrossLangStrategies runs each sync strategy independently on the
// 30 Rock test files and reports the offset and confidence for tuning.
func TestCrossLangStrategies(t *testing.T) {
	for _, tc := range crosslangTestCases {
		t.Run(tc.name, func(t *testing.T) {
			ref := loadTestCues(t, tc.enFile)
			inc := loadTestCues(t, tc.frFile)

			t.Logf("ref cues: %d, inc cues: %d", len(ref), len(inc))
			t.Logf("ref range: %v - %v", ref[0].Start, ref[len(ref)-1].End)
			t.Logf("inc range: %v - %v", inc[0].Start, inc[len(inc)-1].End)

			// Strategy 1: CrossLang anchor matching.
			t.Run("crosslang", func(t *testing.T) {
				result := crossLangAlign(context.Background(), ref, inc)
				t.Logf("offset=%dms confidence=%.3f method=%s",
					result.Offset, float64(result.Confidence), result.Method)
				if result.Confidence > 0 {
					validateOffset(t, ref, inc, result.Offset)
				}
			})

			// Strategy 2: Constant offset (alass).
			t.Run("alass_offset", func(t *testing.T) {
				_, offset := syncCues(context.Background(), ref, inc)
				conf := constantOffsetConfidence(ref, inc, offset)
				t.Logf("offset=%dms confidence=%.3f",
					offset.Milliseconds(), float64(conf))
				validateOffset(t, ref, inc, offset.Milliseconds())
			})

			// Strategy 3: Framerate correction.
			t.Run("framerate", func(t *testing.T) {
				result := correctFramerate(context.Background(), ref, inc, "")
				t.Logf("offset=%dms rate=%.6f confidence=%.3f",
					result.Offset, result.Rate, float64(result.Confidence))
			})

			// Strategy 4: Split-aware alignment.
			t.Run("splits", func(t *testing.T) {
				result := alignWithSplits(context.Background(), ref, inc, 0)
				t.Logf("offset=%dms confidence=%.3f method=%s",
					result.Offset, float64(result.Confidence), result.Method)
				if result.Confidence > 0 {
					validateOffset(t, ref, inc, result.Offset)
				}
			})

			// Strategy 5: Full voting pipeline.
			t.Run("voting", func(t *testing.T) {
				opts := DefaultSyncOptions()
				opts.EnableFramerate = true
				opts.EnableSplits = true
				result := referenceSync(context.Background(), ref, inc, &opts)
				t.Logf("WINNER: offset=%dms confidence=%.3f method=%s",
					result.Offset, float64(result.Confidence), result.Method)
				if result.Applied() {
					validateOffset(t, ref, inc, result.Offset)
				}
			})
		})
	}
}

// validateOffset checks if the offset produces reasonable alignment by
// sampling cue pairs and measuring how well they overlap after shifting.
func validateOffset(t *testing.T, ref, inc []Cue, offsetMs int64) {
	t.Helper()
	offset := time.Duration(offsetMs) * time.Millisecond
	shifted := ShiftCues(inc, offset)

	// Sample 10 evenly spaced points and find the nearest ref cue.
	step := max(len(shifted)/10, 1)

	var totalDiff int64
	var samples int
	for i := 0; i < len(shifted) && samples < 10; i += step {
		shiftedStartMs := shifted[i].Start.Milliseconds()
		// Find nearest ref cue by start time.
		bestDiff := int64(999999)
		for _, r := range ref {
			diff := abs64(r.Start.Milliseconds() - shiftedStartMs)
			if diff < bestDiff {
				bestDiff = diff
			}
		}
		totalDiff += bestDiff
		samples++
	}

	avgDiff := totalDiff / int64(samples)
	t.Logf("  avg nearest-cue diff after shift: %dms (samples=%d)", avgDiff, samples)

	// Generous threshold: a good sync should place most cues within 5s
	// of a reference cue. Anything worse indicates a broken offset.
	if avgDiff > 5000 {
		t.Errorf("avg nearest-cue diff %dms exceeds 5000ms threshold", avgDiff)
	}

	// Also check first and last cue alignment.
	firstDiff := abs64(shifted[0].Start.Milliseconds() - ref[0].Start.Milliseconds())
	// Use 90th percentile to avoid credits.
	incLast := shifted[len(shifted)*9/10].Start.Milliseconds()
	refLast := ref[len(ref)*9/10].Start.Milliseconds()
	lastDiff := abs64(incLast - refLast)
	t.Logf("  first cue diff: %dms, 90th%% cue diff: %dms", firstDiff, lastDiff)
}

func TestCrossLangAlign_early_returns(t *testing.T) {
	t.Parallel()
	makeCuesXL := func(n int, start, gap time.Duration, text string) []Cue {
		cues := make([]Cue, n)
		for i := range cues {
			s := start + time.Duration(i)*gap
			cues[i] = Cue{Start: s, End: s + time.Second, Text: text}
		}
		return cues
	}
	tests := []struct {
		name string
		ref  []Cue
		inc  []Cue
	}{
		{"too_few_reference_cues", makeCuesXL(4, 0, 5*time.Second, "Hello 42"), makeCuesXL(10, 0, 5*time.Second, "Bonjour 42")},
		{"too_few_incorrect_cues", makeCuesXL(10, 0, 5*time.Second, "Hello 42"), makeCuesXL(4, 0, 5*time.Second, "Bonjour 42")},
		{"too_few_anchored_cues", makeCuesXL(10, 0, 5*time.Second, "oh no"), makeCuesXL(10, 0, 5*time.Second, "ah bon")},
		{"zero_duration_reference", makeCuesXL(10, -10*time.Second, 0, "Hello 42"), makeCuesXL(10, 0, 5*time.Second, "Bonjour 42")},
		{"zero_duration_incorrect", makeCuesXL(10, 0, 5*time.Second, "Hello 42"), makeCuesXL(10, -10*time.Second, 0, "Bonjour 42")},
		{
			"too_few_pass1_candidates",
			// Reference cues clustered at the start (0-5s), incorrect at the end (50-55s).
			// Normalized positions are ~0.0 vs ~1.0, far outside the ±10% window.
			makeCuesXL(10, 0, 500*time.Millisecond, "Hello 42"),
			makeCuesXL(10, 50*time.Second, 500*time.Millisecond, "Bonjour 42"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := crossLangAlign(context.Background(), tt.ref, tt.inc)
			if result.Confidence != ConfidenceNone {
				t.Errorf("crossLangAlign(%s) confidence = %v, want ConfidenceNone", tt.name, result.Confidence)
			}
			if result.Method != MethodCrosslang {
				t.Errorf("crossLangAlign(%s) method = %v, want MethodCrosslang", tt.name, result.Method)
			}
		})
	}
}
