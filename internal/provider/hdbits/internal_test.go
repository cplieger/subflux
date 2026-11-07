package hdbits

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKeep(t *testing.T) {
	for _, tc := range []struct {
		title, file string
		want        bool
	}{
		{"Inception 2010 720p", "Inception.srt", true},
		{"Inception 2010 720p", "Inception.SRT", true},
		{"Inception 2010 720p", "Inception.ass", true},
		{"Inception 2010 720p", "Inception.zip", true},
		{"Inception 2010 720p", "Inception.rar", true},
		{"Inception 2010 720p", "no extension", true},
		{"Inception 2010 720p", "Inception.exe", false},
		{"Inception 2010 720p", "Inception.nfo", false},
		{"Inception Commentary", "Inception.srt", false},
		{"Inception", "Inception.Extras.720p.rar", false},
		{"Inception lyrics", "Inception.srt", false},
		{"Inception forced", "Inception.forced.srt", true},
	} {
		if got := keep(subtitle{Title: tc.title, Filename: tc.file}); got != tc.want {
			t.Errorf("keep(%q, %q) = %v, want %v", tc.title, tc.file, got, tc.want)
		}
	}
}

func TestToken(t *testing.T) {
	for file, want := range map[string]string{
		"x.srt": "7.srt", "x.ASS": "7.ass", "x.tar.gz": "7.gz", "x": "7", "x.": "7",
		"x.sub-title": "7", "x.verylongext": "7", "x.smi": "7.smi",
	} {
		if got := token(subtitle{ID: 7, Filename: file}); got != want {
			t.Errorf("token(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestSubtitleID(t *testing.T) {
	for in, want := range map[string]subtitleID{`551609`: 551609, `"551609"`: 551609} {
		var got subtitleID
		if err := json.Unmarshal([]byte(in), &got); err != nil || got != want {
			t.Errorf("%s: %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{`null`, `""`, `0`, `-3`, `"abc"`, `1.5`, `2147483648`, `{}`} {
		got := subtitleID(9)
		if err := json.Unmarshal([]byte(in), &got); err == nil || got != 0 {
			t.Errorf("%s: %d, %v; want an error and 0", in, got, err)
		}
	}
}

// FuzzToken: whatever the listed file name, the token is the id or the id
// and a plain extension, the two forms Fetch reads.
func FuzzToken(f *testing.F) {
	f.Add("Inception.srt")
	f.Add("x.tar.gz")
	f.Add("../../etc/passwd")
	f.Fuzz(func(t *testing.T, file string) {
		id, ext, dotted := strings.Cut(token(subtitle{ID: 1, Filename: file}), ".")
		if id != "1" || dotted && !plainExt(ext) {
			t.Fatalf("token for %q is %q.%q", file, id, ext)
		}
	})
}
