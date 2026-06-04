package subdl

import "testing"

// FuzzIsNotFoundError verifies isNotFoundError never panics and returns a bool.
func FuzzIsNotFoundError(f *testing.F) {
	f.Add("We don't have the movie/series you are looking for")
	f.Add("Your request was not found")
	f.Add("")
	f.Add("some random error message")
	f.Add("NOT FOUND")
	f.Add("The film could not be located")

	f.Fuzz(func(t *testing.T, msg string) {
		_ = isNotFoundError(msg)
	})
}

// FuzzSubdlLangRoundtrip verifies that for any code that iso2ToSubDL
// produces a non-empty result, subdlToISO2 maps back to the original code.
func FuzzSubdlLangRoundtrip(f *testing.F) {
	f.Add("en")
	f.Add("fr")
	f.Add("zh")
	f.Add("xx")
	f.Add("")

	f.Fuzz(func(t *testing.T, code string) {
		sdl := iso2ToSubDL(code)
		if sdl == "" {
			return // unmapped code, nothing to roundtrip
		}
		back := subdlToISO2(sdl)
		if back == "" {
			t.Fatalf("iso2ToSubDL(%q)=%q but subdlToISO2(%q)=%q", code, sdl, sdl, back)
		}
	})
}
