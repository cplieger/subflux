package subsync

import (
	"testing"
	"time"
)

func FuzzPostProcessIdempotent(f *testing.F) {
	f.Add("Hello world", "[gunshot]", "♪ music ♪")
	f.Add("<i>styled</i>", "JOHN: Hi", "  spaces  ")
	f.Add("", "- dash", "normal text")

	f.Fuzz(func(t *testing.T, t1, t2, t3 string) {
		cues := []Cue{
			{Text: t1, Start: time.Second, End: 2 * time.Second},
			{Text: t2, Start: 3 * time.Second, End: 4 * time.Second},
			{Text: t3, Start: 5 * time.Second, End: 6 * time.Second},
		}
		opts := PostProcessOptions{
			StripHI:         true,
			StripTags:       true,
			CleanWhitespace: true,
			RemoveEmpty:     true,
		}
		once := PostProcess(cues, opts)
		twice := PostProcess(once, opts)
		if len(once) != len(twice) {
			t.Fatalf("PostProcess not idempotent: len(once)=%d len(twice)=%d", len(once), len(twice))
		}
		for i := range once {
			if once[i].Text != twice[i].Text {
				t.Errorf("PostProcess not idempotent at cue %d: once=%q twice=%q", i, once[i].Text, twice[i].Text)
			}
		}
	})
}
