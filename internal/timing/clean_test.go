package timing_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleenc"
	"github.com/cplieger/subflux/internal/timing"
)

var allText = timing.CleanOptions{StripHI: true, StripTags: true, CleanWhitespace: true, RemoveEmpty: true}

func mustClean(t *testing.T, c timing.Cues, opts timing.CleanOptions, tag language.Tag) timing.Cues {
	t.Helper()
	got, err := timing.Clean(c, opts, tag)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	return got
}

func doc(t *testing.T, c timing.Cues) string {
	t.Helper()
	return string(mustRender(t, c, timing.Identity{}))
}

func TestStripHI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"brackets", "[gunshot] Down!", " Down!"},
		{"parens", "Fine (sighs) then.", "Fine  then."},
		{"music span", "♪ la la ♪ Next", " Next"},
		{"music-only line", "Hi\n ♪\nThere", "Hi\nThere"},
		{"a note span leaves an empty line", "Hi\n♪ ♫\nThere", "Hi\n\nThere"},
		{"speaker label and its blanks", "DR. SMITH: Sit.", "Sit."},
		{"stacked labels", "JOHN: MARY: Hi", "Hi"},
		{"label with a parenthetical", "NARRATOR (V.O.): Once.", "Once."},
		{"one letter is no label", "A: yes", "A: yes"},
		{"lower case is no label", "John: hi", "John: hi"},
		{"label only at the line start", "Hi JOHN: there", "Hi JOHN: there"},
		{"a removed span exposes a label", "[x]JOHN: hi", "hi"},
		{"an unclosed opener stays", "(still talking", "(still talking"},
		{"tags are not HI", "<i>x</i>", "<i>x</i>"},
		{"invalid UTF-8 reads as U+FFFD", "a\xffb", "a\uFFFDb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := timing.StripHI(tc.in); got != tc.want {
				t.Errorf("StripHI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanSRT(t *testing.T) {
	t.Parallel()
	in := "1\r\n00:00:01,000 --> 00:00:02,000\r\n<i>JOHN: [door] Hello.</i>\r\n\r\n" +
		"2\r\n00:00:03,000 --> 00:00:04,000\r\n(laughs)\r\n\r\n" +
		"3\r\n00:00:05,000 --> 00:00:06,000\r\n  - Bye.  \r\n- (sighs)\r\n"
	c := mustParse(t, []byte(in))
	for _, tc := range []struct {
		name string
		opts timing.CleanOptions
		want string
	}{
		{"nothing", timing.CleanOptions{}, in},
		{
			"every text option", allText,
			"1\r\n00:00:01,000 --> 00:00:02,000\r\nHello.\r\n\r\n" +
				"2\r\n00:00:05,000 --> 00:00:06,000\r\n- Bye.\r\n",
		},
		{
			"no remove_empty",
			timing.CleanOptions{StripHI: true, StripTags: true, CleanWhitespace: true},
			"1\r\n00:00:01,000 --> 00:00:02,000\r\nHello.\r\n\r\n" +
				"2\r\n00:00:03,000 --> 00:00:04,000\r\n\r\n\r\n" +
				"3\r\n00:00:05,000 --> 00:00:06,000\r\n- Bye.\r\n",
		},
		{
			"tags only",
			timing.CleanOptions{StripTags: true},
			strings.Replace(strings.Replace(in, "<i>", "", 1), "</i>", "", 1),
		},
		{"remove_empty only keeps text that is there", timing.CleanOptions{RemoveEmpty: true}, in},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mustClean(t, c, tc.opts, language.Tag{})
			if s := doc(t, got); s != tc.want {
				t.Errorf("Clean = %q, want %q", s, tc.want)
			}
		})
	}
}

func TestStripHIKeepsOverrideBlocks(t *testing.T) {
	t.Parallel()
	text := `{\pos(320,50)}[sign] (note){\fad(200,200)}JOHN: Hi\N{\clip(0,0,10,10)}♪ la ♪\N(gone)`
	c := mustParse(t, []byte(assHead+"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,"+text+"\n"))
	got := doc(t, mustClean(t, c, allText, language.Tag{}))
	want := assHead + `Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\pos(320,50)}{\fad(200,200)}Hi{\clip(0,0,10,10)}` + "\n"
	if got != want {
		t.Errorf("Clean = %q,\nwant %q", got, want)
	}
}

func TestCleanASSEscapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, text, want string }{
		{"a removal never forms a line break", `a \(x)N b`, `a \(x)N b`},
		{"a line break splits lines", `a (x)\N b`, `a\Nb`},
		{"a hard space is text", `JOHN:\hHi`, `\hHi`},
		{"comment block is a block", `{note to self}(sighs) Hi`, `{note to self}Hi`},
		{"an unclosed brace is text", `{(sighs) Hi`, `{ Hi`},
		{"a line of only a block is no text", `(sighs){\an8}`, `{\an8}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line := func(text string) string {
				return "Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,," + text + "\n"
			}
			c := mustParse(t, []byte(assHead+line(tc.text)+line("kept")))
			opts := timing.CleanOptions{StripHI: true, CleanWhitespace: true}
			if got, want := doc(t, mustClean(t, c, opts, language.Tag{})), assHead+line(tc.want)+line("kept"); got != want {
				t.Errorf("Clean = %q, want %q", got, want)
			}
		})
	}
	c := mustParse(t, []byte(assHead+"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,(sighs){\\an8}\nDialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,kept\n"))
	got := doc(t, mustClean(t, c, allText, language.Tag{}))
	if want := assHead + "Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,kept\n"; got != want {
		t.Errorf("remove_empty on a line of only a block = %q, want %q", got, want)
	}
}

func TestCleanRenumbers(t *testing.T) {
	t.Parallel()
	in := "7\n00:00:01,000 --> 00:00:02,000\nkept\n\n8\n00:00:03,000 --> 00:00:04,000\n[music]\n\n9\n00:00:05,000 --> 00:00:06,000\nalso\n"
	got := doc(t, mustClean(t, mustParse(t, []byte(in)), allText, language.Tag{}))
	want := "1\n00:00:01,000 --> 00:00:02,000\nkept\n\n2\n00:00:05,000 --> 00:00:06,000\nalso\n"
	if got != want {
		t.Errorf("Clean = %q, want %q", got, want)
	}
}

func TestNormalizeUTF8FalseKeepsBytes(t *testing.T) {
	t.Parallel()
	in := fixture(t, "srt-windows-1252.srt")
	c := mustParse(t, in)
	got := []byte(doc(t, mustClean(t, c, timing.CleanOptions{CleanWhitespace: true, NormalizeLineEndings: false}, tag(t, "fr"))))
	if !bytes.Equal(got, in) {
		t.Errorf("Clean changed bytes it had no rule for:\n got %q\nwant %q", got, in)
	}
	utf8Doc := doc(t, mustClean(t, c, timing.CleanOptions{NormalizeUTF8: true}, tag(t, "fr")))
	want := strings.Join(eventTexts(mustParse(t, decodeAs(t, subtitleenc.Windows1252, in))), "|")
	if got := strings.Join(eventTexts(mustParse(t, []byte(utf8Doc))), "|"); got != want {
		t.Errorf("normalize_utf8 texts = %q, want %q", got, want)
	}
}

func decodeAs(t testing.TB, enc subtitleenc.Encoding, b []byte) []byte {
	t.Helper()
	d, err := enc.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func eventTexts(c timing.Cues) []string {
	var out []string
	for _, e := range c.Events() {
		out = append(out, e.Text)
	}
	return out
}

func TestCleanNormalize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   []byte
		opts timing.CleanOptions
		want string
	}{
		{
			"UTF-8 byte order mark dropped", []byte("\uFEFF1\n00:00:01,000 --> 00:00:02,000\nHi\n"),
			timing.CleanOptions{NormalizeUTF8: true},
			"1\n00:00:01,000 --> 00:00:02,000\nHi\n",
		},
		{
			"UTF-16 re-encoded", utf16LE("\uFEFF1\r\n00:00:01,000 --> 00:00:02,000\r\nH\u00e9\r\n"),
			timing.CleanOptions{NormalizeUTF8: true},
			"1\r\n00:00:01,000 --> 00:00:02,000\r\nH\u00e9\r\n",
		},
		{
			"line endings CRLF", []byte("1\n00:00:01,000 --> 00:00:02,000\rHi\r\n\n2\n00:00:03,000 --> 00:00:04,000\nBye"),
			timing.CleanOptions{NormalizeLineEndings: true},
			"1\r\n00:00:01,000 --> 00:00:02,000\r\nHi\r\n\r\n2\r\n00:00:03,000 --> 00:00:04,000\r\nBye\r\n",
		},
		{
			"UTF-16 line endings in its own units", utf16LE("\uFEFF1\n00:00:01,000 --> 00:00:02,000\nHi"),
			timing.CleanOptions{NormalizeLineEndings: true},
			string(utf16LE("\uFEFF1\r\n00:00:01,000 --> 00:00:02,000\r\nHi\r\n")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := doc(t, mustClean(t, mustParse(t, tc.in), tc.opts, language.Tag{})); got != tc.want {
				t.Errorf("Clean = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCleanTextInUTF16(t *testing.T) {
	t.Parallel()
	c := mustParse(t, utf16LE("\uFEFF1\r\n00:00:01,000 --> 00:00:02,000\r\n[gasps] \U0001F600 h\u00e9\r\n"))
	got := doc(t, mustClean(t, c, allText, language.Tag{}))
	if want := string(utf16LE("\uFEFF1\r\n00:00:01,000 --> 00:00:02,000\r\n\U0001F600 h\u00e9\r\n")); got != want {
		t.Errorf("Clean = %q, want %q", got, want)
	}
}

func TestCleanMultibyteTrailBytes(t *testing.T) {
	t.Parallel()
	// In Shift-JIS ー is 0x81 0x5B, its second byte the code of '['; the
	// kana after it are what Detect needs to name Shift-JIS.
	const kana = "\x82\xb1\x82\xf1\x82\xc9\x82\xbf\x82\xcd\x81\x41\x90\xa2\x8a\x45\x81\x42\x82\xa0\x82\xe8\x82\xaa\x82\xc6\x82\xa4"
	in := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\n[x]\x81\x5ba]" + kana + "\r\n")
	if enc := subtitleenc.Detect(in, tag(t, "ja")); enc != subtitleenc.ShiftJIS {
		t.Fatalf("Detect = %v, want ShiftJIS", enc)
	}
	got := doc(t, mustClean(t, mustParse(t, in), timing.CleanOptions{StripHI: true}, tag(t, "ja")))
	if want := "1\r\n00:00:01,000 --> 00:00:02,000\r\n\x81\x5ba]" + kana + "\r\n"; got != want {
		t.Errorf("Clean = %q, want %q", got, want)
	}
}

func TestCleanReDetectsWithTag(t *testing.T) {
	t.Parallel()
	in := fixture(t, "srt-windows-1251.srt")
	c := mustParse(t, in)
	for _, tc := range []struct {
		lang    string
		enc     subtitleenc.Encoding
		wantDoc []byte
	}{
		{"ru", subtitleenc.Windows1251, decodeAs(t, subtitleenc.Windows1251, in)},
		{"fr", subtitleenc.Unknown, in},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			t.Parallel()
			if enc := subtitleenc.Detect(in, tag(t, tc.lang)); enc != tc.enc {
				t.Fatalf("Detect under %s = %v, want %v", tc.lang, enc, tc.enc)
			}
			want := strings.Join(eventTexts(mustParse(t, tc.wantDoc)), "|")
			got := strings.Join(eventTexts(mustClean(t, c, timing.CleanOptions{NormalizeUTF8: true}, tag(t, tc.lang))), "|")
			if got != want {
				t.Errorf("Clean under %s reads %q, want %q", tc.lang, got, want)
			}
		})
	}
}

func TestCleanRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range cleanFailures(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := timing.Clean(tc.c, tc.opts, tc.tag); !errors.Is(err, timing.ErrFormatUnsupported) {
				t.Errorf("Clean = %v, want ErrFormatUnsupported", err)
			}
		})
	}
}

// cleanFailures is every Clean refusal; codes_test.go drives each too.
func cleanFailures(t testing.TB) []struct {
	name string
	c    timing.Cues
	opts timing.CleanOptions
	tag  language.Tag
} {
	t.Helper()
	hiOnly, err := timing.ParseCues([]byte("1\n00:00:01,000 --> 00:00:02,000\n[music]\n"))
	if err != nil {
		t.Fatal(err)
	}
	assHIOnly, err := timing.ParseCues([]byte(assHead + "Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,[music]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// 15.4 MiB of windows-1252, 18.0 MiB in UTF-8.
	bigDoc := []byte("1\n00:00:01,000 --> 00:00:02,000\n" + strings.Repeat("caf\xe9 \xe0 la cr\xe8me\n", 900_000) + "\n")
	if enc := subtitleenc.Detect(bigDoc, tag(t, "fr")); enc != subtitleenc.Windows1252 {
		t.Fatalf("Detect = %v, want Windows1252", enc)
	}
	big, err := timing.ParseCues(bigDoc)
	if err != nil {
		t.Fatal(err)
	}
	return []struct {
		name string
		c    timing.Cues
		opts timing.CleanOptions
		tag  language.Tag
	}{
		{"zero Cues", timing.Cues{}, allText, language.Tag{}},
		{"every event removed", hiOnly, allText, language.Tag{}},
		{"every event removed, header left", assHIOnly, allText, language.Tag{}},
		{"re-encoded past 16 MiB", big, timing.CleanOptions{NormalizeUTF8: true}, tag(t, "fr")},
	}
}

func tag(t testing.TB, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("language.Parse(%q) failed", s)
	}
	return tg
}

func utf16LE(s string) []byte {
	var b []byte
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			hi, lo := 0xD800+(r>>10), 0xDC00+(r&0x3FF)
			b = append(b, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
			continue
		}
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}
