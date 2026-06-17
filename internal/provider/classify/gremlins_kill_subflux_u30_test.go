package classify

import "testing"

// Kills lang.go:104:15 CONDITIONALS_NEGATION (overrides != nil, != -> ==).
// When overrides is non-nil and contains the code, the original consults the
// override map and returns its value. The mutated "== nil" skips the override
// block (a non-nil map is not == nil) and falls through to LangRegistry,
// returning the canonical name instead of the override.
func Test_gk_subflux_u30_LookupLangNameOverride(t *testing.T) {
	got := LookupLangName("en", map[string]string{"en": "ZZZ"})
	if got != "ZZZ" {
		t.Errorf("LookupLangName(%q, override) = %q, want %q", "en", got, "ZZZ")
	}
}

// Kills lang.go:119:15 CONDITIONALS_NEGATION (overrides != nil, != -> ==).
// Same structure as LookupLangName: a non-nil override map must be consulted;
// the "== nil" mutant skips it and falls through to the reverse registry.
func Test_gk_subflux_u30_LookupLangCodeOverride(t *testing.T) {
	got := LookupLangCode("English", map[string]string{"English": "zz"})
	if got != "zz" {
		t.Errorf("LookupLangCode(%q, override) = %q, want %q", "English", got, "zz")
	}
}
