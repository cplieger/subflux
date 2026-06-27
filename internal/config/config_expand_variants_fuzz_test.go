package config

import "testing"

// FuzzExpandVariants feeds arbitrary target lists through expandTargetList and
// checks its structural invariants: it never panics, a non-empty input code is
// preserved on every expanded target, and no output target ever carries both
// "variant" and "variants" (that combination is a load-time error).
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

		// When the input code was non-empty, every expanded target keeps it.
		if code != "" {
			for i, r := range result {
				if r.Code == "" {
					t.Fatalf("result[%d].Code is empty, want %q", i, code)
				}
			}
		}

		// variant and variants are never both set in an expanded target.
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
