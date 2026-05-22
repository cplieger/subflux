package classify

import "testing"

func FuzzIsForced(f *testing.F) {
	f.Add("")
	f.Add("forced")
	f.Add("Foreign Parts Only")
	f.Add("English (Full)")
	f.Add("FORCED")
	f.Fuzz(func(t *testing.T, comment string) {
		// Must not panic.
		IsForced(comment)
	})
}

func FuzzIsHearingImpaired(f *testing.F) {
	f.Add("", "")
	f.Add("SDH", "")
	f.Add("non-sdh", "")
	f.Add("", "movie_hi_eng.srt")
	f.Add("closed caption", "sub.srt")
	f.Fuzz(func(t *testing.T, commentary, filename string) {
		// Must not panic.
		IsHearingImpaired(commentary, filename)
	})
}
