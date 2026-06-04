package search

import (
	"testing"

	"subflux/internal/api"
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
