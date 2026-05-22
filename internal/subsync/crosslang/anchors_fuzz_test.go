package crosslang

import "testing"

func FuzzExtractAnchors(f *testing.F) {
	// Seed corpus covering different input shapes.
	f.Add("Hello world")
	f.Add("123 Main Street")
	f.Add("<i>Italic text</i>")
	f.Add("{\\an8}Positioned text")
	f.Add("Line one\nLine two\nLine three")
	f.Add("Über straße café")
	f.Add("")
	f.Add("Mr. Smith went to Washington.")
	f.Add("42 is the answer to everything.")
	f.Add("♪ La la la ♪") //nolint:dupword // intentional lyric repetition in test seed
	f.Add("<font color=\"#ffffff\">Colored</font>")

	f.Fuzz(func(t *testing.T, text string) {
		a := ExtractAnchors(text)
		// Structural invariants: non-negative counts.
		if a.WordCount < 0 {
			t.Errorf("WordCount = %d, want >= 0", a.WordCount)
		}
		if a.CharLen < 0 {
			t.Errorf("CharLen = %d, want >= 0", a.CharLen)
		}
	})
}
