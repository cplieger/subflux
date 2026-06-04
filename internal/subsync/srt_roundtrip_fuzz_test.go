package subsync

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzSRTRoundtrip verifies that ParseSRT → WriteSRT → ParseSRT produces
// identical cues (roundtrip property).
func FuzzSRTRoundtrip(f *testing.F) {
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	f.Add("1\n00:01:00,500 --> 00:01:03,200\nLine one\nLine two\n\n2\n00:02:00,000 --> 00:02:01,000\nSecond\n\n")
	f.Fuzz(func(t *testing.T, data string) {
		cues, err := ParseSRT(strings.NewReader(data))
		if err != nil || len(cues) == 0 {
			return
		}
		var buf bytes.Buffer
		if err := WriteSRT(&buf, cues); err != nil {
			t.Fatal(err)
		}
		cues2, err := ParseSRT(strings.NewReader(buf.String()))
		if err != nil {
			t.Fatal(err)
		}
		if len(cues2) != len(cues) {
			t.Fatalf("cue count mismatch: %d vs %d", len(cues), len(cues2))
		}
		for i := range cues {
			if cues[i].Start != cues2[i].Start || cues[i].End != cues2[i].End {
				t.Fatalf("cue %d timing mismatch", i)
			}
			if cues[i].Text != cues2[i].Text {
				t.Fatalf("cue %d text mismatch: %q vs %q", i, cues[i].Text, cues2[i].Text)
			}
		}
	})
}
