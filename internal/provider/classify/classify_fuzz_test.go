package classify

import (
	"strings"
	"testing"
)

func FuzzIsForced(f *testing.F) {
	f.Add("")
	f.Add("forced")
	f.Add("Foreign Parts Only")
	f.Add("English (Full)")
	f.Add("FORCED")
	f.Fuzz(func(t *testing.T, comment string) {
		got := IsForced(comment)
		// IsForced lowercases its input before matching, so the verdict must
		// be identical for the already-lowercased form (kills a dropped
		// case-fold).
		if lowered := IsForced(strings.ToLower(comment)); lowered != got {
			t.Errorf("IsForced(%q) = %v, lowercased = %v (must be case-insensitive)",
				comment, got, lowered)
		}
	})
}

func FuzzIsHearingImpaired(f *testing.F) {
	f.Add("", "")
	f.Add("SDH", "")
	f.Add("non-sdh", "")
	f.Add("", "movie_hi_eng.srt")
	f.Add("closed caption", "sub.srt")
	f.Fuzz(func(t *testing.T, commentary, filename string) {
		got := IsHearingImpaired(commentary, filename)
		// Both inputs are lowercased before matching, so the verdict must be
		// stable under lowercasing.
		if lowered := IsHearingImpaired(strings.ToLower(commentary), strings.ToLower(filename)); lowered != got {
			t.Errorf("IsHearingImpaired(%q, %q) = %v, lowercased = %v (must be case-insensitive)",
				commentary, filename, got, lowered)
		}
	})
}
