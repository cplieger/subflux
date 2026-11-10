package language

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// FuzzReadFlags: texts read the same in either order and whatever separates
// their words, ASCII texts read the same all lower case as all upper case,
// and a text denying both flags clears them whatever the other says.
func FuzzReadFlags(f *testing.F) {
	f.Add("The.Foreigner.2017.srt", "SDH")
	f.Add("Movie.Not.Forced.srt", "English [HI]")
	f.Add("Show S01E01 HI removed", "closed captions")
	f.Add("foreign parts hard-coded", "Forced")
	f.Add("Hard of Hearing, non-forced", "")
	f.Add("\xff\xfeSDH", "NotForced")
	f.Add("show.hi10p.1080p", "Hi.Score.Girl")
	f.Add("Show.S01E01.SDH.srt", "Non-Hi")
	f.Fuzz(func(t *testing.T, a, b string) {
		got := ReadFlags(a, b)
		if swapped := ReadFlags(b, a); swapped != got {
			t.Fatalf("ReadFlags(%q, %q) = %+v, swapped %+v", a, b, got, swapped)
		}
		dotted := func(s string) string {
			return strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsNumber(r) {
					return r
				}
				return '.'
			}, s)
		}
		if d := ReadFlags(dotted(a), dotted(b)); d != got {
			t.Fatalf("ReadFlags(%q, %q) = %+v, with separators as dots %+v", a, b, got, d)
		}
		if isASCII(a) && isASCII(b) {
			lower := ReadFlags(strings.ToLower(a), strings.ToLower(b))
			if upper := ReadFlags(strings.ToUpper(a), strings.ToUpper(b)); upper != lower {
				t.Fatalf("ReadFlags(%q, %q) = %+v lower-cased, %+v upper-cased", a, b, lower, upper)
			}
		}
		if d := ReadFlags(a, "non-SDH, not forced", b); d != (Flags{}) {
			t.Fatalf("ReadFlags(%q, a denial, %q) = %+v, want no flags", a, b, d)
		}
	})
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
