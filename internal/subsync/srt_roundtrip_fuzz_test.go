package subsync

import (
	"bytes"
	"strings"
	"testing"
)

func FuzzSRTRoundtrip(f *testing.F) {
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nLine 1\n\n2\n00:00:03,000 --> 00:00:04,000\nLine 2\n\n")
	f.Add("1\n00:01:30,500 --> 00:01:33,200\nMulti\nline\n\n")

	f.Fuzz(func(t *testing.T, input string) {
		cues1, err := ParseSRT(strings.NewReader(input))
		if err != nil || len(cues1) == 0 {
			return
		}
		var buf bytes.Buffer
		if err := WriteSRT(&buf, cues1); err != nil {
			t.Fatalf("WriteSRT failed: %v", err)
		}
		cues2, err := ParseSRT(strings.NewReader(buf.String()))
		if err != nil {
			t.Fatalf("re-parse failed: %v", err)
		}
		if len(cues1) != len(cues2) {
			t.Fatalf("roundtrip length mismatch: %d vs %d", len(cues1), len(cues2))
		}
		for i := range cues1 {
			if cues1[i].Start != cues2[i].Start || cues1[i].End != cues2[i].End || cues1[i].Text != cues2[i].Text {
				t.Errorf("roundtrip mismatch at cue %d: %+v vs %+v", i, cues1[i], cues2[i])
			}
		}
	})
}
