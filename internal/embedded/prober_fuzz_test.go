package embedded

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

// FuzzParseProbeTitle: a stored title is a prefix of the stream's, at most
// maxTitleBytes and never cut inside a UTF-8 sequence; the title refines the
// tag but never changes its language; the flags are the dispositions' plus
// what language.ReadFlags reads in the stored title; and a title opening with
// denials leaves only the dispositions'.
func FuzzParseProbeTitle(f *testing.F) {
	f.Add("eng", "English SDH", false, false)
	f.Add("por", "Brasil", true, false)
	f.Add("chi", "Traditional Chinese", false, true)
	f.Add("srp", "Latinica", false, false)
	f.Add("und", "Forced", false, false)
	f.Add("eng", "English Unforced", false, false)
	f.Add("eng", "Hard of Hearing, non-forced", false, false)
	f.Add("", strings.Repeat("é", 200), false, false)
	f.Add("xx-bad", "\xff\xfe", true, true)
	f.Fuzz(func(t *testing.T, lang, title string, forced, hi bool) {
		got := trackOf(&ffmpeg.Stream{Type: "subtitle", Language: lang, Title: title, Forced: forced, HearingImpaired: hi})
		if !strings.HasPrefix(title, got.Title) || len(got.Title) > maxTitleBytes {
			t.Fatalf("title %q stored as %q", title, got.Title)
		}
		if len(title) <= maxTitleBytes && got.Title != title {
			t.Fatalf("title %q within the cap stored as %q", title, got.Title)
		}
		if utf8.ValidString(title) && !utf8.ValidString(got.Title) {
			t.Fatalf("valid title %q stored cut inside a sequence: %q", title, got.Title)
		}
		base, _ := language.Parse(strings.TrimSpace(lang))
		if primary(got.Tag) != primary(base) {
			t.Fatalf("language %q, title %q: tag %v, want the language of %v", lang, title, got.Tag, base)
		}
		read := language.ReadFlags(got.Title)
		if want := (language.Flags{HI: hi || read.HI, Forced: forced || read.Forced}); got.Flags != want {
			t.Fatalf("title %q, dispositions forced %v, hi %v: flags %+v, want %+v", got.Title, forced, hi, got.Flags, want)
		}
		denied := trackOf(&ffmpeg.Stream{Type: "subtitle", Language: lang, Title: "Non-SDH Unforced not forced " + title, Forced: forced, HearingImpaired: hi})
		if want := (language.Flags{HI: hi, Forced: forced}); denied.Flags != want {
			t.Fatalf("title %q opening with denials: flags %+v, want the dispositions' %+v", denied.Title, denied.Flags, want)
		}
	})
}

// primary is t's language subtag, "" for the zero Tag.
func primary(t language.Tag) string {
	p, _, _ := strings.Cut(t.String(), "-")
	return p
}
