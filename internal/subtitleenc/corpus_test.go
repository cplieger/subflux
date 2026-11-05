package subtitleenc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

// corpusExpect is a corpus file's <name>.expect.json.
type corpusExpect struct {
	Hint     string `json:"hint"`
	Encoding string `json:"encoding"` // an Encoding's String, or "Unknown"
	SHA256   string `json:"sha256"`   // of the original UTF-8 text
}

func TestDetectCorpus(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.srt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("corpus: %d files, %v", len(files), err)
	}
	for _, path := range files {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".srt"), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := readCorpusExpect(t, strings.TrimSuffix(path, ".srt")+".expect.json")
			hint, ok := language.Parse(want.Hint)
			if !ok {
				t.Fatalf("hint %q does not parse", want.Hint)
			}
			name, text := corpusAnswer(data, hint)
			switch {
			case name == "Unknown" && want.Encoding != "Unknown":
				t.Fatalf("answers Unknown, a missed conversion of a %s file", want.Encoding)
			case name != "" && name != want.Encoding:
				t.Fatalf("encoding %s, want %s", name, want.Encoding)
			}
			if want.Encoding == "Unknown" {
				if !bytes.Equal(text, data) {
					t.Errorf("an Unknown answer changed the bytes")
				}
				return
			}
			sum := sha256.Sum256(text)
			if got := hex.EncodeToString(sum[:]); got != want.SHA256 {
				t.Errorf("decoded text sha256 %s, want the original's %s", got, want.SHA256)
			}
		})
	}
}

func readCorpusExpect(t *testing.T, path string) corpusExpect {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var e corpusExpect
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if e.Encoding == "" || len(e.SHA256) != sha256.Size*2 {
		t.Fatalf("%s: encoding %q, sha256 %q", path, e.Encoding, e.SHA256)
	}
	return e
}
