package embedded

import (
	"testing"
)

// FuzzNormalizeTrack exercises the embedded subtitle track normalization
// with arbitrary codec/lang/name strings and flag combinations.
//
// Bug class: panic on empty lang causing nil dereference downstream;
// BCP 47 subtag extraction off-by-one; HI/forced detection false positives
// on adversarial track names.
func FuzzNormalizeTrack(f *testing.F) {
	f.Add(0, "subrip", "eng", "English", false, false)
	f.Add(1, "ass", "en-US", "SDH", true, false)
	f.Add(2, "hdmv_pgs_subtitle", "", "", false, false)
	f.Add(3, "subrip", "und", "Forced", false, true)
	f.Add(4, "dvd_subtitle", "fra-CA", "Commentary (Hearing Impaired)", false, false)

	f.Fuzz(func(t *testing.T, index int, codec, lang, name string, forced, hi bool) {
		track := normalizeTrack(index, codec, lang, name, forced, hi)
		if track == nil {
			// nil is valid for empty/undefined lang
			return
		}
		if track.lang == "" {
			t.Fatal("non-nil track must have non-empty lang")
		}
	})
}
