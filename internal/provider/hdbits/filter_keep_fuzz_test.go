package hdbits

import "testing"

func FuzzKeepSubtitle(f *testing.F) {
	f.Add("English Subs", "sub.srt")
	f.Add("Commentary Track", "commentary.srt")
	f.Add("Extras", "extras.ass")
	f.Add("Lyrics", "lyrics.lrc")
	f.Add("", "")
	f.Add("Normal Title", "file.exe")
	f.Add("Title", "file.sub")
	f.Add("Title", "noext")

	f.Fuzz(func(t *testing.T, title, filename string) {
		s := hdbSubtitleItem{Title: title, Filename: filename}
		// Must not panic.
		_ = keepSubtitle(s)
	})
}

func FuzzHdbLangToISO(f *testing.F) {
	f.Add("en")
	f.Add("br")
	f.Add("gr")
	f.Add("cz")
	f.Add("")
	f.Add("xx")
	f.Add("se")
	f.Add("kr")
	f.Add("日本")

	f.Fuzz(func(t *testing.T, code string) {
		result := hdbLangToISO(code)
		// If non-empty, it should be a valid 2-3 char language code.
		if result != "" && len(result) > 3 {
			t.Fatalf("hdbLangToISO(%q) = %q, unexpectedly long", code, result)
		}
	})
}
