package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestFilterByVariant_properties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		subs := make([]api.Subtitle, n)
		for i := range subs {
			subs[i].Forced = rapid.Bool().Draw(t, "forced")
			subs[i].HearingImp = rapid.Bool().Draw(t, "hi")
		}
		variant := rapid.SampledFrom([]api.Variant{api.VariantForced, api.VariantHI, api.VariantStandard}).Draw(t, "variant")

		filtered, fallback := filterByVariant(subs, variant)

		// Output is always a subset of input.
		if len(filtered) > len(subs) {
			t.Fatalf("filtered (%d) > input (%d)", len(filtered), len(subs))
		}

		switch variant {
		case api.VariantForced:
			for _, s := range filtered {
				if !s.Forced {
					t.Fatal("forced variant output contains non-forced sub")
				}
			}
			if fallback {
				t.Fatal("forced variant should never fallback")
			}
		case api.VariantHI:
			for _, s := range filtered {
				if !s.HearingImp {
					t.Fatal("HI variant output contains non-HI sub")
				}
			}
			if fallback {
				t.Fatal("HI variant should never fallback")
			}
		default:
			for _, s := range filtered {
				if s.Forced {
					t.Fatal("standard variant output contains forced sub")
				}
			}
		}
	})
}
