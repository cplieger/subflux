package timing_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// FuzzParseCues: every accepted document stores at most twice its length
// and reloads to the same format and events, and the reloaded Cues stores to
// the same bytes.
func FuzzParseCues(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.*"))
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("[Script Info]\n[Events]\nFormat: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,a\n"))
	f.Add([]byte("WEBVTT\n\n00:01.000 --> 00:02.000\n<i.x>a</i> &amp;\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := timing.ParseCues(b)
		if err != nil {
			return
		}
		if n := storedDocLen(t, c); n > 2*len(b) {
			t.Fatalf("stored document of %d bytes for %d input bytes", n, len(b))
		}
		stored := timing.AppendCues(nil, c)
		got, err := timing.DecodeCues(stored)
		if err != nil {
			t.Fatalf("DecodeCues(AppendCues(ParseCues(%q))): %v", b, err)
		}
		if got.Format() != c.Format() || !slices.Equal(got.Events(), c.Events()) {
			t.Fatalf("reloaded %v %+v, parsed %v %+v", got.Format(), got.Events(), c.Format(), c.Events())
		}
		if again := timing.AppendCues(nil, got); !bytes.Equal(again, stored) {
			t.Fatalf("reloaded Cues stores differently")
		}
	})
}
