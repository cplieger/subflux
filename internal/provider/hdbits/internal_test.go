package hdbits

import (
	"encoding/json"
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
