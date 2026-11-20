package wirespec_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/wirespec"
)

func TestFileNameRefuses(t *testing.T) {
	refused := map[string]string{
		"a wrong extension":     "episode.txt",
		"no extension":          "episode",
		"dot":                   ".",
		"dot dot":               "..",
		"a decoded slash":       "sub/episode.srt",
		"a backslash":           `sub\episode.srt`,
		"a NUL":                 "episode\x00.srt",
		"256 bytes":             strings.Repeat("a", 252) + ".srt",
		"an empty name":         "",
		"a parent element":      "../episode.srt",
		"an absolute path":      "/episode.srt",
		"an archive name":       "episode.zip",
		"a writer-only archive": "episode.vtt",
	}
	for name, in := range refused {
		t.Run(name, func(t *testing.T) {
			var n wirespec.FileName
			if err := n.UnmarshalText([]byte(in)); err == nil {
				t.Fatalf("UnmarshalText(%q) accepted it", in)
			}
			if n != "" {
				t.Errorf("a refusal set the name to %q", n)
			}
		})
	}
	t.Run("a name ValidName refuses wraps its sentinel", func(t *testing.T) {
		var n wirespec.FileName
		if err := n.UnmarshalText([]byte("episode.txt")); !errors.Is(err, mediawrite.ErrInvalidRef) {
			t.Errorf("err = %v, want mediawrite.ErrInvalidRef", err)
		}
	})

	accepted := map[string]string{
		"a Unicode name":          "Épisode 1.fr.srt",
		"a CJK name":              "番組.ja.ass",
		"an extension alone":      ".srt",
		"an uppercase extension":  "EPISODE.SRT",
		"255 ASCII bytes":         strings.Repeat("a", 251) + ".srt",
		"255 bytes of multi-byte": strings.Repeat("é", 125) + "a.ssa",
	}
	for name, in := range accepted {
		t.Run(name, func(t *testing.T) {
			var n wirespec.FileName
			if err := n.UnmarshalText([]byte(in)); err != nil {
				t.Fatalf("UnmarshalText(%q) = %v", in, err)
			}
			if string(n) != in {
				t.Errorf("name = %q, want %q", n, in)
			}
			if b, err := n.MarshalText(); err != nil || string(b) != in {
				t.Errorf("MarshalText = %q, %v; want %q", b, err, in)
			}
		})
	}
	if n := len(accepted["255 bytes of multi-byte"]); n != 255 {
		t.Fatalf("the multi-byte fixture is %d bytes, want 255", n)
	}
}

func TestUploadNameRefuses(t *testing.T) {
	refused := map[string]string{
		"empty":         "",
		"256 bytes":     strings.Repeat("a", 256),
		"invalid UTF-8": "release\xff",
		"a line break":  "release\nname",
		"a tab":         "release\tname",
		"a DEL":         "release\x7f",
		"a C1 control":  "release\u0085",
	}
	for name, in := range refused {
		t.Run(name, func(t *testing.T) {
			var n wirespec.UploadName
			if err := n.UnmarshalText([]byte(in)); err == nil {
				t.Errorf("UnmarshalText(%q) accepted it", in)
			}
		})
	}
	for _, in := range []string{"Show.S01E02.1080p.WEB-DL-GROUP", strings.Repeat("é", 127) + "a"} {
		var n wirespec.UploadName
		if err := n.UnmarshalText([]byte(in)); err != nil || string(n) != in {
			t.Errorf("UnmarshalText(%q) = %q, %v", in, n, err)
		}
		if b, err := n.MarshalText(); err != nil || string(b) != in {
			t.Errorf("MarshalText = %q, %v; want %q", b, err, in)
		}
	}
}

func TestUploadBytes(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString
	refused := map[string]string{
		"empty":                 "",
		"not base64":            "@@@@",
		"unpadded":              "YQ",
		"URL alphabet":          "-_8=",
		"trailing bits set":     "YR==",
		"one byte over the cap": enc(make([]byte, 10_000_001)),
		"over the base64 cap":   strings.Repeat("A", wirespec.UploadBytes(nil).MaxBytes()+4),
	}
	for name, in := range refused {
		t.Run(name, func(t *testing.T) {
			var u wirespec.UploadBytes
			if err := u.UnmarshalText([]byte(in)); err == nil {
				t.Errorf("UnmarshalText accepted %d base64 bytes", len(in))
			}
			if u != nil {
				t.Errorf("a refusal set %d bytes", len(u))
			}
		})
	}
	t.Run("decodes a small file", func(t *testing.T) {
		var u wirespec.UploadBytes
		if err := u.UnmarshalText([]byte("YWJj")); err != nil || string(u) != "abc" {
			t.Errorf("UnmarshalText = %q, %v", u, err)
		}
		if b, err := u.MarshalText(); err != nil || string(b) != "YWJj" {
			t.Errorf("MarshalText = %q, %v; want YWJj", b, err)
		}
	})
	t.Run("accepts exactly the cap", func(t *testing.T) {
		in := enc(make([]byte, 10_000_000))
		if len(in) > wirespec.UploadBytes(nil).MaxBytes() {
			t.Fatalf("the cap encodes to %d bytes, over MaxBytes %d", len(in), wirespec.UploadBytes(nil).MaxBytes())
		}
		var u wirespec.UploadBytes
		if err := u.UnmarshalText([]byte(in)); err != nil || len(u) != 10_000_000 {
			t.Errorf("UnmarshalText = %d bytes, %v", len(u), err)
		}
	})
}

func TestBoundsDeclared(t *testing.T) {
	ranges := map[string]func() (int64, int64){
		"FromMs": wirespec.FromMs(0).Range,
	}
	want := map[string][2]int64{"FromMs": {0, 86_400_000}}
	for name, r := range ranges {
		if lo, hi := r(); [2]int64{lo, hi} != want[name] {
			t.Errorf("%s.Range() = %d, %d, want %v", name, lo, hi, want[name])
		}
	}
	if lo, hi := wirespec.ResyncFiles(nil).Count(); lo != 1 || hi != 500 {
		t.Errorf("ResyncFiles.Count() = %d, %d, want 1, 500", lo, hi)
	}
}
