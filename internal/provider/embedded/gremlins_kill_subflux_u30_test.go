package embedded

import "testing"

// Kills embedded.go:181:42 CONDITIONALS_BOUNDARY (i > 0, > -> >=).
// normalizeTrack truncates the language tag at the first '-' only when that
// index is > 0 (a real "en-US" style subtag boundary). With lang "-en" the
// dash is at index 0: the original leaves the tag intact ("-en"), while the
// mutated ">= 0" truncates lang[:0] to "" before the alpha-3 lookup.
func Test_gk_subflux_u30_NormalizeTrackDashAtIndexZero(t *testing.T) {
	st := normalizeTrack(1, "srt", "-en", "name", false, false)
	if st == nil {
		t.Fatalf("normalizeTrack(1, \"srt\", \"-en\", ...) = nil, want non-nil track")
	}
	if st.lang != "-en" {
		t.Errorf("normalizeTrack lang = %q, want %q", st.lang, "-en")
	}
}
