package search

import (
	"testing"

	"subflux/internal/api"
)

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
