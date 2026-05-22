package subsync

import (
	"strings"
	"testing"
)

func FuzzParseSRT(f *testing.F) {
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	f.Add("")
	f.Add("1\n99:99:99,999 --> 00:00:00,000\nBad\n\n")
	f.Add("not a subtitle file at all")
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = ParseSRT(strings.NewReader(input))
	})
}
