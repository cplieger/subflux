package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzFilterByVariantSubset verifies that filterByVariant output is always
// a subset of the input: no elements are fabricated.
func FuzzFilterByVariantSubset(f *testing.F) {
	f.Add(true, false, true, false, "standard")
	f.Add(false, true, false, true, "hi")
	f.Add(true, true, false, false, "forced")
	f.Add(false, false, false, false, "")

	f.Fuzz(func(t *testing.T, hi1, forced1, hi2, forced2 bool, variant string) {
		input := []api.Subtitle{
			{ID: "1", HearingImp: hi1, Forced: forced1},
			{ID: "2", HearingImp: hi2, Forced: forced2},
		}

		v := api.Variant(variant)
		if v != api.VariantForced && v != api.VariantHI && v != api.VariantStandard {
			v = api.VariantStandard
		}

		filtered, _ := filterByVariant(input, v)

		ids := map[string]bool{"1": true, "2": true}
		for _, s := range filtered {
			if !ids[s.ID] {
				t.Fatalf("filterByVariant produced unknown ID %q", s.ID)
			}
		}
		if len(filtered) > len(input) {
			t.Fatalf("filterByVariant output (%d) larger than input (%d)", len(filtered), len(input))
		}
	})
}

func FuzzFilterByVariant(f *testing.F) {
	f.Add("standard", true, false, true, true, false, false)
	f.Add("forced", false, true, false, false, true, false)
	f.Add("hi", true, false, false, true, false, true)
	f.Add("", false, false, false, false, false, false)
	f.Add("unknown", true, true, true, false, false, false)

	f.Fuzz(func(t *testing.T, variant string, hi1, forced1, hi2, forced2, hi3, forced3 bool) {
		results := []api.Subtitle{
			{HearingImp: hi1, Forced: forced1, ReleaseName: "sub1"},
			{HearingImp: hi2, Forced: forced2, ReleaseName: "sub2"},
			{HearingImp: hi3, Forced: forced3, ReleaseName: "sub3"},
		}

		v := api.Variant(variant)
		filtered, fallback := filterByVariant(results, v)

		// Invariant: filtered results should respect variant constraints.
		switch v {
		case api.VariantForced:
			for _, s := range filtered {
				if !s.Forced {
					t.Fatal("VariantForced filter returned non-forced subtitle")
				}
			}
			if fallback {
				t.Fatal("VariantForced should never use fallback")
			}
		case api.VariantHI:
			for _, s := range filtered {
				if !s.HearingImp {
					t.Fatal("VariantHI filter returned non-HI subtitle")
				}
				if s.Forced {
					t.Fatal("VariantHI filter returned forced subtitle")
				}
			}
			if fallback {
				t.Fatal("VariantHI should never use fallback")
			}
		default: // standard or unknown
			for _, s := range filtered {
				if s.Forced {
					t.Fatal("Standard variant filter returned forced subtitle")
				}
			}
		}
	})
}

// FuzzFilterByScore verifies that filterByScore keeps only subtitles at or
// above minScore and never fabricates results (output is a subset of input).
func FuzzFilterByScore(f *testing.F) {
	f.Add(10, 20, 30, 15)
	f.Add(0, 0, 0, 0)
	f.Add(100, 50, 75, 60)
	f.Add(-1, 5, 10, 0)

	f.Fuzz(func(t *testing.T, s1, s2, s3, minScore int) {
		if s1 < -1000 || s1 > 1000 || s2 < -1000 || s2 > 1000 || s3 < -1000 || s3 > 1000 {
			return
		}
		scored := []scoredSub{
			{sub: api.Subtitle{ID: "1"}, score: s1},
			{sub: api.Subtitle{ID: "2"}, score: s2},
			{sub: api.Subtitle{ID: "3"}, score: s3},
		}
		result := filterByScore(scored, minScore)
		// Invariant: all results must be >= minScore.
		for _, r := range result {
			if r.score < minScore {
				t.Fatalf("filterByScore returned score %d below min %d", r.score, minScore)
			}
		}
		// Invariant: result is a subset.
		if len(result) > len(scored) {
			t.Fatalf("filterByScore output (%d) larger than input (%d)", len(result), len(scored))
		}
	})
}
