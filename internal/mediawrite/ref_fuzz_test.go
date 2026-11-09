package mediawrite_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// FuzzValidName: ValidName and DecodeRef agree,
// and every accepted name is one local element with an on-disk subtitle
// extension that a root-relative join keeps inside its directory.
func FuzzValidName(f *testing.F) {
	for _, s := range []string{"movie.en.srt", "..", "a/b.srt", "x.vtt", "\x00.srt", `a\b.ass`, "movie.en.sub", "./x.srt", "x.srt/"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		err := mediawrite.ValidName(name)
		ref, refErr := mediawrite.DecodeRef(1, name)
		if (err == nil) != (refErr == nil) {
			t.Fatalf("%q: ValidName %v, DecodeRef %v disagree", name, err, refErr)
		}
		if err != nil {
			if !errors.Is(err, mediawrite.ErrInvalidRef) || !errors.Is(refErr, mediawrite.ErrInvalidRef) {
				t.Fatalf("%q: refusals %v, %v are not ErrInvalidRef", name, err, refErr)
			}
			return
		}
		if ref.Name() != name || filepath.Base(name) != name || !filepath.IsLocal(name) ||
			strings.ContainsAny(name, "/\\\x00") || !subtitleext.OnDisk(name) {
			t.Fatalf("%q accepted but not one local subtitle element", name)
		}
		if filepath.Dir(filepath.Join("dir", name)) != "dir" {
			t.Fatalf("%q leaves its directory when joined", name)
		}
	})
}
