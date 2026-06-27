package subdl

import (
	"strings"
	"testing"
)

// FuzzIsNotFoundError checks that not-found detection is case-insensitive.
// isNotFoundError lowercases its input before matching, so the verdict must
// be identical for a message and its lowercased form; a dropped ToLower would
// make "NOT FOUND" diverge from "not found". ToLower is idempotent, so this
// metamorphic relation holds for every input on the correct implementation.
func FuzzIsNotFoundError(f *testing.F) {
	f.Add("can't find the movie")
	f.Add("Cannot Find that title")
	f.Add("NOT FOUND")
	f.Add("Sorry, we can't find that")
	f.Add("")
	f.Add("some random error message")
	f.Add("internal server error")

	f.Fuzz(func(t *testing.T, msg string) {
		got := isNotFoundError(msg)
		if lowered := isNotFoundError(strings.ToLower(msg)); lowered != got {
			t.Fatalf("isNotFoundError case sensitivity: %q=>%v but lowercased=>%v", msg, got, lowered)
		}
	})
}

// FuzzSubdlLangRoundtrip checks that the SubDL<->ISO maps are exact inverses:
// whenever iso2ToSubDL yields a SubDL code, decoding it back to ISO and
// re-encoding must return the same SubDL code. This holds even for alpha-3
// input (which iso2ToSubDL first normalizes to alpha-2), so the SubDL code is
// the stable fixed point of the round-trip.
func FuzzSubdlLangRoundtrip(f *testing.F) {
	f.Add("en")
	f.Add("fr")
	f.Add("zh")
	f.Add("eng") // alpha-3, normalized to alpha-2 before lookup
	f.Add("xx")
	f.Add("")

	f.Fuzz(func(t *testing.T, code string) {
		sdl := iso2ToSubDL(code)
		if sdl == "" {
			return // unmapped code, nothing to round-trip
		}
		iso := subdlToISO2(sdl)
		if iso == "" {
			t.Fatalf("iso2ToSubDL(%q)=%q but subdlToISO2(%q)=empty", code, sdl, sdl)
		}
		if again := iso2ToSubDL(iso); again != sdl {
			t.Fatalf("round-trip not stable: iso2ToSubDL(subdlToISO2(%q))=%q, want %q", sdl, again, sdl)
		}
	})
}
