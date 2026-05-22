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
