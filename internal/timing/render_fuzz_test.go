package timing_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// FuzzRenderIdentity: Identity writes the stored document byte for byte
// whenever no event ends at or before 0 (those Render removes).
func FuzzRenderIdentity(f *testing.F) {
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
	f.Add([]byte("1\n0:00:01,5 --> 0:00:02.25 X1:1\nA\n"))
	f.Add([]byte(assHead + "Dialogue: 0,12:00:00.00,12:00:01.00,Default,,0,0,0,,a\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := timing.ParseCues(b)
		if err != nil {
			return
		}
		for _, e := range c.Events() {
			if e.EndMs <= 0 {
				return
			}
		}
		out, err := timing.Render(c, timing.Identity{})
		if err != nil {
			t.Fatalf("Render(Identity): %v", err)
		}
		if doc := storedDoc(t, c); !bytes.Equal(out, doc) {
			t.Fatalf("Render(Identity) = %q, want the document %q", out, doc)
		}
	})
}
