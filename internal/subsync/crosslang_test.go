package subsync

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync/crosslang"
)

// Compile-time assertion: crosslang.MinCuesForSync must equal subsync.MinCuesForSync.
// This prevents silent drift between the two packages' constants.
var _ [MinCuesForSync]struct{} = [crosslang.MinCuesForSync]struct{}{}

// The cross-language pair is SYNTHESISED, not read from testdata. This family
// used to read a 30 Rock English/French SRT pair, which testdata/.gitignore
// keeps out of the repo as copyrighted content — so the files were never
// present, loadTestCues called t.Skipf, and the whole family skipped on every
// run while reporting coverage it did not provide.
//
// The synthetic pair models the property that makes cross-language alignment
// work: the two sides share only the anchors that do NOT translate (a unique
// number and a proper noun per cue) and differ in every other word, so the
// aligner has to match on anchors rather than on text or cue index. The
// incorrect side is the reference displaced by a known constant offset, which
// turns that offset into an EXPECTATION every strategy is checked against
// instead of a number the test merely logged.
const (
	// offsetToleranceMs bounds how far a recovered offset may sit from the
	// synthesised one. Every strategy recovers it exactly today; the tolerance
	// absorbs weighted-median rounding without admitting a real regression.
	offsetToleranceMs = 250

	// maxAlignDiffMs bounds the average distance from a corrected cue to its
	// nearest reference cue. Every offset below leaves an uncorrected pair
	// further off than this, so the bound distinguishes a real correction from
	// none — which a looser one would not.
	maxAlignDiffMs = 1000

	synthCueCount = 30
)

// crosslangTestCases are constant displacements a strategy must recover.
// Each is larger than maxAlignDiffMs so an uncorrected result cannot pass.
var crosslangTestCases = []struct {
	name         string
	wantOffsetMs int64
}{
	{"late_subtitle", 7500},
	{"early_subtitle", -4200},
	{"large_offset", 21000},
}

// synthCueStart returns a deterministic irregular cue start. Irregular spacing
// matters: on an evenly spaced grid every displacement that is a multiple of
// the gap lands exactly on some reference cue, which would make the alignment
// check pass for a wrong offset.
func synthCueStart(i int) time.Duration {
	gaps := []int{13, 21, 8, 34, 17, 26, 11, 29, 19, 23}
	total := 60 * 1000 // first cue at 60s
	for k := range i {
		total += gaps[k%len(gaps)]*1000 + (k%7)*137
	}
	return time.Duration(total) * time.Millisecond
}

// synthCrossLangPair builds a reference ("English") and an incorrect
// ("French") cue list displaced by offsetMs. Adding offsetMs to every
// incorrect cue reproduces the reference timing exactly.
func synthCrossLangPair(offsetMs int64) (reference, incorrect []Cue) {
	names := []string{"Jenna", "Tracy", "Kenneth", "Jack", "Pete", "Cerie"}
	enTail := []string{
		"rehearsal in studio",
		"budget meeting on floor",
		"sketch running at minute",
		"contract signed in room",
		"promo taping at gate",
	}
	frTail := []string{
		"repetition au studio",
		"reunion budgetaire etage",
		"sketch diffuse a la minute",
		"contrat signe dans la salle",
		"tournage promo porte",
	}
	reference = make([]Cue, 0, synthCueCount)
	incorrect = make([]Cue, 0, synthCueCount)
	for i := range synthCueCount {
		start := synthCueStart(i)
		// A distinct number per cue makes the anchor discriminative: with a
		// repeated number every incorrect cue would match every reference cue
		// equally well and the alignment would have no signal to follow.
		num := 100 + i*3
		name := names[i%len(names)]
		reference = append(reference, Cue{
			Start: start,
			End:   start + 3*time.Second,
			Text:  fmt.Sprintf("Call %s about the %s %d.", name, enTail[i%len(enTail)], num),
		})
		incStart := start - time.Duration(offsetMs)*time.Millisecond
		incorrect = append(incorrect, Cue{
			Start: incStart,
			End:   incStart + 3*time.Second,
			Text:  fmt.Sprintf("Appelle %s pour la %s %d.", name, frTail[i%len(frTail)], num),
		})
	}
	return reference, incorrect
}

// TestCrossLangStrategies drives each sync strategy over one cross-language
// pair whose true offset is known, and checks that each either recovers that
// offset or declines explicitly. Every subtest asserts: a zero-confidence
// result is a stated expectation (framerate) or a failure (the rest), never a
// silent pass.
func TestCrossLangStrategies(t *testing.T) {
	for _, tc := range crosslangTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref, inc := synthCrossLangPair(tc.wantOffsetMs)

			// Strategy 1: CrossLang anchor matching. The pair is strongly
			// anchored by construction, so declining to align is a failure.
			t.Run("crosslang", func(t *testing.T) {
				result := crossLangAlign(t.Context(), ref, inc)
				if result.Confidence <= ConfidenceNone {
					t.Fatalf("crossLangAlign confidence = %v, want > 0 on a fully anchored pair",
						float64(result.Confidence))
				}
				assertOffset(t, "crossLangAlign", result.Offset, tc.wantOffsetMs)
				assertCuesAlign(t, "crossLangAlign", ref, result.Cues)
			})

			// Strategy 2: Constant offset (alass). A pure constant
			// displacement is exactly what this strategy exists to find.
			t.Run("alass_offset", func(t *testing.T) {
				shifted, offset := syncCues(t.Context(), ref, inc)
				conf := constantOffsetConfidence(ref, inc, offset)
				if conf <= ConfidenceNone {
					t.Fatalf("constantOffsetConfidence = %v, want > 0 for a constant displacement",
						float64(conf))
				}
				assertOffset(t, "syncCues", offset.Milliseconds(), tc.wantOffsetMs)
				assertCuesAlign(t, "syncCues", ref, shifted)
			})

			// Strategy 3: Framerate correction. The expectation here is a
			// DECLINE: the pair is a pure shift with no drift, so reporting a
			// rate other than identity would be an invented correction that
			// re-times every cue progressively. This is the assertion the
			// subtest previously lacked entirely.
			t.Run("framerate", func(t *testing.T) {
				result := correctFramerate(t.Context(), ref, inc, "")
				if result.Rate != 1.0 {
					t.Errorf("correctFramerate rate = %.6f, want 1.0 (no drift to correct on a pure shift)",
						result.Rate)
				}
				if result.Applied() {
					t.Errorf("correctFramerate reported an applied correction (offset=%dms rate=%.6f), want none",
						result.Offset, result.Rate)
				}
			})

			// Strategy 4: Split-aware alignment. It deliberately reports
			// Offset 0 and carries its per-segment correction in Cues, so the
			// returned CUES are the artifact to check — validating
			// result.Offset would validate something this strategy never sets.
			t.Run("splits", func(t *testing.T) {
				result := alignWithSplits(t.Context(), ref, inc, 0)
				if result.Confidence <= ConfidenceNone {
					t.Fatalf("alignWithSplits confidence = %v, want > 0", float64(result.Confidence))
				}
				assertCuesAlign(t, "alignWithSplits", ref, result.Cues)
			})

			// Strategy 5: Full voting pipeline. Which strategy wins is an
			// implementation detail; that the winner carries the true offset
			// is the contract.
			t.Run("voting", func(t *testing.T) {
				opts := DefaultSyncOptions()
				opts.EnableFramerate = true
				opts.EnableSplits = true
				result := referenceSync(t.Context(), ref, inc, &opts)
				if !result.Applied() {
					t.Fatalf("referenceSync applied nothing (offset=%dms rate=%.6f method=%s), want the %dms offset",
						result.Offset, result.Rate, result.Method, tc.wantOffsetMs)
				}
				assertOffset(t, "referenceSync", result.Offset, tc.wantOffsetMs)
				assertCuesAlign(t, "referenceSync", ref, result.Cues)
			})
		})
	}
}

// assertOffset checks a recovered offset against the synthesised one.
func assertOffset(t *testing.T, fn string, gotMs, wantMs int64) {
	t.Helper()
	if diff := abs64(gotMs - wantMs); diff > offsetToleranceMs {
		t.Errorf("%s offset = %dms, want %dms (off by %dms, tolerance %dms)",
			fn, gotMs, wantMs, diff, offsetToleranceMs)
	}
}

// assertCuesAlign checks that already-corrected cues land on the reference
// timing, by measuring the average distance from a sampled corrected cue to
// its nearest reference cue.
func assertCuesAlign(t *testing.T, fn string, ref, got []Cue) {
	t.Helper()
	if len(got) == 0 {
		t.Errorf("%s returned no cues, want %d corrected cues", fn, len(ref))
		return
	}

	// Sample 10 evenly spaced points and find the nearest ref cue.
	step := max(len(got)/10, 1)
	var totalDiff int64
	var samples int
	for i := 0; i < len(got) && samples < 10; i += step {
		startMs := got[i].Start.Milliseconds()
		bestDiff := int64(math.MaxInt64)
		for _, r := range ref {
			if diff := abs64(r.Start.Milliseconds() - startMs); diff < bestDiff {
				bestDiff = diff
			}
		}
		totalDiff += bestDiff
		samples++
	}

	if avgDiff := totalDiff / int64(samples); avgDiff > maxAlignDiffMs {
		t.Errorf("%s: average nearest-reference-cue distance = %dms over %d samples, want <= %dms",
			fn, avgDiff, samples, maxAlignDiffMs)
	}
}

// math_MaxInt64Sentinel is an unreachable cue distance used to seed the
// nearest-cue search; any real distance is smaller.
const math_MaxInt64Sentinel = int64(1) << 40

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
			result := crossLangAlign(t.Context(), tt.ref, tt.inc)
			if result.Confidence != ConfidenceNone {
				t.Errorf("crossLangAlign(%s) confidence = %v, want ConfidenceNone", tt.name, result.Confidence)
			}
			if result.Method != MethodCrosslang {
				t.Errorf("crossLangAlign(%s) method = %v, want MethodCrosslang", tt.name, result.Method)
			}
		})
	}
}
