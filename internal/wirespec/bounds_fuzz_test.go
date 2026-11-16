package wirespec_test

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subtitleext"
	"github.com/cplieger/subflux/internal/wirespec"
)

// FuzzFileName_neverAcceptsSeparator: an accepted name is one path element
// of at most 255 bytes with an on-disk subtitle extension, spelled as given.
func FuzzFileName_neverAcceptsSeparator(f *testing.F) {
	for _, s := range []string{"a.srt", "../a.srt", "a/b.srt", `a\b.srt`, ".", "..", ".srt", "a\x00.srt", "é.ass", "a.SRT", "a.txt", "a%2Fb.srt"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var n wirespec.FileName
		if err := n.UnmarshalText([]byte(in)); err != nil {
			return
		}
		switch {
		case string(n) != in:
			t.Fatalf("accepted %q as %q", in, n)
		case strings.ContainsAny(in, "/\\\x00"), in == ".", in == "..":
			t.Fatalf("accepted %q, which is not one path element", in)
		case len(in) > 255:
			t.Fatalf("accepted %d bytes", len(in))
		case !subtitleext.OnDisk(in):
			t.Fatalf("accepted %q without an on-disk subtitle extension", in)
		}
	})
}
