package manualops

import (
	"strings"
	"testing"
)

// TestGk_subflux_u29_IsValidLangCode_lengthBoundary kills manualops.go:100:29
// CONDITIONALS_BOUNDARY on "len(lang) > MaxLangCodeLen" (">" vs ">="). A code
// of exactly MaxLangCodeLen characters is valid; the mutated ">=" rejects it
// at the boundary.
func TestGk_subflux_u29_IsValidLangCode_lengthBoundary(t *testing.T) {
	exactly := strings.Repeat("a", MaxLangCodeLen)
	if len(exactly) != MaxLangCodeLen {
		t.Fatalf("test setup: len = %d, want %d", len(exactly), MaxLangCodeLen)
	}
	if !IsValidLangCode(exactly) {
		t.Errorf("IsValidLangCode(len==%d) = false, want true (boundary is valid)", MaxLangCodeLen)
	}
	// One over the limit stays invalid for both original and mutant.
	if IsValidLangCode(exactly + "a") {
		t.Errorf("IsValidLangCode(len==%d) = true, want false", MaxLangCodeLen+1)
	}
}

// TestGk_subflux_u29_IsValidLangCode_controlCharBoundary kills
// manualops.go:106:66 CONDITIONALS_BOUNDARY on "r < 0x20" ("<" vs "<=") in the
// control-character check. A space (0x20) is NOT a control char, so a code
// containing a space is valid; the mutated "r <= 0x20" treats the space as a
// control char and rejects it.
func TestGk_subflux_u29_IsValidLangCode_controlCharBoundary(t *testing.T) {
	if !IsValidLangCode("en US") {
		t.Errorf("IsValidLangCode(%q) = false, want true (0x20 space is not a control char)", "en US")
	}
	// A real control char (tab, 0x09 < 0x20) is rejected by both original and mutant.
	if IsValidLangCode("en\tUS") {
		t.Errorf("IsValidLangCode(%q) = true, want false (tab is a control char)", "en\tUS")
	}
}
