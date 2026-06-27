package subsync

import "testing"

func FuzzParseASSDialogue(f *testing.F) {
	seeds := []string{
		"",
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n",
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,OP,,0,0,0,,Lyrics\n",
		"[V4+ Styles]\nStyle: Default,Arial,20\n[Events]\n",
		"Dialogue: 0,99:99:99.99,99:99:99.99,Default,,0,0,0,,Overflow\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		ParseASSDialogue([]byte(input))
	})
}

// FuzzStripASSOverrides exercises ASS override-tag removal ({\...} sequences)
// with arbitrary input. The pass must be idempotent: a second removal over
// already-stripped text changes nothing.
func FuzzStripASSOverrides(f *testing.F) {
	f.Add("{\\an8}Hello")
	f.Add("{\\pos(320,50)}Text")
	f.Add("")
	f.Add("{}")
	f.Add("no overrides")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripASSOverrides(text)
		if second := stripASSOverrides(result); second != result {
			t.Fatalf("stripASSOverrides not idempotent: %q -> %q -> %q", text, result, second)
		}
	})
}

// FuzzIsASSContent exercises the ASS format-detection predicate with arbitrary
// byte input. It only inspects a bounded prefix, so the invariant checked here
// is that it stays a total, panic-free predicate on any input (including
// empty and short slices).
func FuzzIsASSContent(f *testing.F) {
	f.Add([]byte("[Script Info]\nTitle: Test"))
	f.Add([]byte("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hi"))
	f.Add([]byte(""))
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nSRT content\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = IsASSContent(data) // must not panic
	})
}
