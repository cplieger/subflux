package subsync

import (
	"testing"
	"time"
)

func FuzzStripHI(f *testing.F) {
	f.Add("[laughing] Hello")
	f.Add("(thunder rumbles)")
	f.Add("♪ Music ♪")
	f.Add("NARRATOR: Welcome")
	f.Add("Normal dialogue")
	f.Add("")
	f.Add("[MAN] What do you mean?")
	f.Add("Line one\n[sound] Line two")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripHI(text)
		if len(result) > len(text) {
			t.Fatalf("stripHI grew output: input len=%d, output len=%d", len(text), len(result))
		}
	})
}

func FuzzPostProcess(f *testing.F) {
	f.Add("[laughing] Hello <i>world</i>", true, true)
	f.Add("Normal text", false, false)
	f.Add("", true, true)
	f.Add("♪ La la ♪\nDialogue here", true, false)

	f.Fuzz(func(t *testing.T, text string, hi, tags bool) {
		cues := []Cue{
			{Start: 0, End: time.Second, Text: text},
			{Start: 2 * time.Second, End: 3 * time.Second, Text: text},
		}
		opts := PostProcessOptions{StripHI: hi, StripTags: tags, RemoveEmpty: true}
		result := PostProcess(cues, opts)

		if len(result) > len(cues) {
			t.Fatalf("PostProcess increased cue count: input=%d output=%d", len(cues), len(result))
		}
		for _, c := range result {
			if c.Text == "" {
				t.Fatal("PostProcess returned cue with empty text after RemoveEmpty")
			}
		}
	})
}
