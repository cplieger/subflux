package gestdown

import (
	"testing"
)

// FuzzConvertSubtitle exercises subtitle conversion with arbitrary inputs
// to ensure it never panics and maintains field invariants.
func FuzzConvertSubtitle(f *testing.F) {
	f.Add("/download/abc", "sub-id", "group1, group2", true, false, 1, 2, "en")
	f.Add("", "", "", false, false, 0, 0, "")
	f.Add("relative/path", "id", "release", true, true, -1, -1, "fr")
	f.Add("/valid", "x", "a,b,c,d,e,f", true, false, 99, 99, "ja")

	f.Fuzz(func(t *testing.T, dlURI, subID, version string, completed, hearingImp bool, episode, season int, isoLang string) {
		s := subtitleResult{
			DownloadURI: dlURI,
			SubtitleID:  subID,
			Version:     version,
			Completed:   completed,
			HearingImp:  hearingImp,
		}

		sub, ok := convertSubtitle(s, episode, season, isoLang)

		// Invariant 1: never panics (implicit).

		// Invariant 2: if not completed, must return false.
		if !completed && ok {
			t.Fatal("convertSubtitle returned ok=true for incomplete subtitle")
		}

		// Invariant 3: if URI doesn't start with '/', must return false.
		if len(dlURI) > 0 && dlURI[0] != '/' && ok {
			t.Fatalf("convertSubtitle accepted non-slash URI: %q", dlURI)
		}

		// Invariant 4: on success, Provider and Language are set.
		if ok {
			if sub.Provider != providerName {
				t.Fatalf("Provider = %q, want %q", sub.Provider, providerName)
			}
			if sub.Language != isoLang {
				t.Fatalf("Language = %q, want %q", sub.Language, isoLang)
			}
		}
	})
}

// FuzzIso2ToGestdown exercises language code mapping.
func FuzzIso2ToGestdown(f *testing.F) {
	f.Add("en")
	f.Add("fr")
	f.Add("")
	f.Add("xx")
	f.Add("pt")

	f.Fuzz(func(t *testing.T, code string) {
		// Must not panic regardless of input.
		_ = iso2ToGestdown(code)
	})
}
