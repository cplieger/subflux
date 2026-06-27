package hdbits

import (
	"strings"
	"testing"
)

// FuzzFlexIntUnmarshalJSON checks the security invariant that a successful
// decode never yields a non-positive id: flexInt rejects null, zero, and
// negative values so a malformed/changing API response can't materialize as
// subtitle id 0 (which would build getdox.php?id=0&passkey=...). Crash-freedom
// is implied by the decode running on arbitrary bytes.
func FuzzFlexIntUnmarshalJSON(f *testing.F) {
	f.Add([]byte(`42`))
	f.Add([]byte(`"123"`))
	f.Add([]byte(`null`))
	f.Add([]byte(`0`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`"not_a_number"`))
	f.Add([]byte(`""`))
	f.Add([]byte(`99999999`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var fi flexInt
		if err := fi.UnmarshalJSON(data); err == nil && fi <= 0 {
			t.Fatalf("UnmarshalJSON(%q) accepted non-positive id %d", data, int(fi))
		}
	})
}

// FuzzKeepSubtitle checks that the keep decision is case-insensitive: both the
// extension check and the excluded-keyword check lowercase their inputs, so
// lowercasing the title/filename up front must not change the verdict.
// strings.ToLower is idempotent and context-free in Go, so this metamorphic
// relation holds for every input on the correct implementation; a dropped
// ToLower on the extension would make "FILE.SRT" diverge from "file.srt".
func FuzzKeepSubtitle(f *testing.F) {
	f.Add("English Subs", "sub.srt")
	f.Add("Commentary Track", "commentary.srt")
	f.Add("Extras", "extras.ass")
	f.Add("Lyrics", "lyrics.lrc")
	f.Add("", "")
	f.Add("Normal Title", "file.exe")
	f.Add("Title", "file.sub")
	f.Add("Title", "noext")
	f.Add("Title", "FILE.SRT")
	f.Add("COMMENTARY", "Sub.SRT")

	f.Fuzz(func(t *testing.T, title, filename string) {
		got := keepSubtitle(hdbSubtitleItem{Title: title, Filename: filename})
		lowered := keepSubtitle(hdbSubtitleItem{
			Title:    strings.ToLower(title),
			Filename: strings.ToLower(filename),
		})
		if lowered != got {
			t.Fatalf("keepSubtitle case sensitivity: (%q,%q)=>%v but lowercased=>%v",
				title, filename, got, lowered)
		}
	})
}

// FuzzHdbLangToISO checks that any mapped language code stays a short
// ISO-style token: a non-empty result longer than three characters signals a
// mapping that leaked unexpected input. Crash-freedom is implied.
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
