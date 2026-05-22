package subsync

import "testing"

func FuzzCleanTextLen(f *testing.F) {
	// Seed corpus: known ASS override patterns, HTML tags, music notes, HI annotations, drawing commands.
	f.Add("{\\an8}Hello world")
	f.Add("{\\pos(320,50)}Subtitle text")
	f.Add("<i>italic text</i>")
	f.Add("<b><font color=\"#FFFFFF\">styled</font></b>")
	f.Add("♪ Music playing ♪")
	f.Add("♫ La la ♫")
	f.Add("[gunshot]")
	f.Add("(door creaking)")
	f.Add("{\\p1}m 0 0 l 100 0 100 100 0 100{\\p0}")
	f.Add("Comment: 0,0:00:00.00,0:00:00.00,Default,,0,0,0,,")
	f.Add("{\\fad(500,500)}Fading text")
	f.Add("- Who are you?\n- I'm nobody.")
	f.Add("Synced by XYZ@Subscene")
	f.Add("")
	f.Add("   ")
	f.Add("{\\an8\\pos(320,50)\\fad(0,500)}Complex ASS")

	f.Fuzz(func(t *testing.T, text string) {
		result := cleanTextLen(text)
		if result < 0 {
			t.Fatalf("cleanTextLen returned negative: %d", result)
		}
	})
}
