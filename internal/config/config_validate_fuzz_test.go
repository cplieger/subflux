package config

import (
	"testing"
)

func FuzzExpandVariants(f *testing.F) {
	// Seed corpus from existing test configs.
	f.Add("en", "", "standard,forced", 3)
	f.Add("fr", "standard", "", 1)
	f.Add("de", "", "standard,forced,sdh", 2)
	f.Add("en", "forced", "standard", 1) // conflict case
	f.Add("", "", "", 0)
	f.Add("en", "", "", 5)
	f.Add("ja", "sdh", "", 2)
	f.Add("pt-BR", "", "standard,forced,sdh,cc", 1)

	f.Fuzz(func(t *testing.T, code, variant, variantsCSV string, numTargets int) {
		if numTargets < 0 || numTargets > 20 {
			return
		}

		var variants []string
		if variantsCSV != "" {
			for _, v := range splitCSV(variantsCSV) {
				if v != "" {
					variants = append(variants, v)
				}
			}
		}

		targets := make([]yamlSubtitleTarget, numTargets)
		for i := range targets {
			targets[i] = yamlSubtitleTarget{
				Code:     code,
				Variant:  variant,
				Variants: variants,
			}
		}

		result, err := expandTargetList(targets, "fuzz")
		if err != nil {
			// Error is expected when both variant and variants are set.
			return
		}

		// Invariant 1: no panic.
		// Invariant 2: when error is nil, all returned targets have non-empty Code
		// (only if input code was non-empty).
		if code != "" {
			for i, r := range result {
				if r.Code == "" {
					t.Fatalf("result[%d].Code is empty, want %q", i, code)
				}
			}
		}

		// Invariant 3: variant and variants are never both set in output.
		for i, r := range result {
			if r.Variant != "" && len(r.Variants) > 0 {
				t.Fatalf("result[%d] has both variant=%q and variants=%v", i, r.Variant, r.Variants)
			}
		}
	})
}

func splitCSV(s string) []string {
	var parts []string
	start := 0
	for i := range s {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
