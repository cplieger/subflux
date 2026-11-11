package subtitleenc

import (
	"bytes"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

// writeBack writes text in e as a file announcing it would: UTF-16 behind its
// BOM, UTF-8 behind one only before a U+FEFF, which Decode strips.
func writeBack(t *testing.T, e Encoding, text []byte) []byte {
	t.Helper()
	out, ok := codecs[e].encode(text)
	if !ok {
		t.Fatalf("%s decodes to %q and cannot write it back", e, text)
	}
	switch {
	case e == UTF16LE:
		return append(slices.Clip(bomUTF16LE), out...)
	case e == UTF16BE:
		return append(slices.Clip(bomUTF16BE), out...)
	case e == UTF8 && bytes.HasPrefix(out, bomUTF8):
		return append(slices.Clip(bomUTF8), out...)
	}
	return out
}

// FuzzDetect_idempotent: a named answer decodes the bytes, and its text,
// written back in it, reads as the same text again under the same hint,
// through the same answer or one decoding it alike (ISO-2022-JP text with no
// escape left is ASCII, so UTF8); Unknown stays Unknown. writeBack writes a
// UTF-8 BOM only before a U+FEFF and no ISO-2022-JP escape before ASCII, so
// the text written from one of those may announce a form Detect tries
// earlier, whose answer stands.
func FuzzDetect_idempotent(f *testing.F) {
	order := map[Encoding]int{UTF16LE: 0, UTF16BE: 0, ISO2022JP: 1, UTF8: 2}
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHi ~{there}~\n"), "en")
	f.Add([]byte("\xef\xbb\xbfCaf\xc3\xa9"), "fr")
	f.Add([]byte("\xef\xbb\xbf0\x000\x00"), "")
	f.Add([]byte("\xef\xbb\xbf\x1b$B$\"\x1b(B ok"), "")
	f.Add([]byte("\x1b(B0\x000\x00"), "")
	f.Add([]byte{0xFF, 0xFE, 'H', 0, 'i', 0}, "")
	f.Add([]byte{0xFE, 0xFF, 0, 'H', 0, 'i'}, "")
	f.Add([]byte("\xff\xfe"), "")
	f.Add([]byte("\xff\xfe\xff\xfe\xff\xfe"), "")
	f.Add([]byte("\xfe\xff\x00\x00\x000\x00\x0000"), "")
	f.Add([]byte("\xef\xbb\xbf\x92"), "en")
	f.Add([]byte{'H', 0, 'i', 0, 0xE9, 0}, "fr")
	f.Add([]byte("M\xb9\xbf przyjedzie jutro. \xb1\xb6"), "pl")
	f.Add([]byte("\xc1\xea\xef\xf5, \xb6\xea\xef\xf5"), "el")
	f.Add([]byte("\xd0\xf0\xe8\xe2\xe5\xf2 \xa4\xa6"), "ru")
	f.Add([]byte("\x1b$B$\"\x1b(B ok"), "ja")
	f.Add([]byte{0x82, 0xa0, 0x82, 0xa2, 0x82, 0xa4, 0x20, 0x82}, "ja")
	f.Add([]byte{0xa4, 0x40, 0xa4, 0x48, 0xb0, 0xea}, "zh-Hant")
	f.Add([]byte("\x88A\x88A\x88A\x88A\x88A\x88A\x88A\x88A\x88A\xa6a\x88b"), "Zh")
	f.Add([]byte{0xFF, 0xFE, 0, 0, 'A', 0, 0, 0}, "")
	f.Add([]byte("\xb2\xbb\x80\xc4@\xc4A\xc4A\xc4A\xc4A\xc4A\xc4A\xc4A\xc4A\xc4A"), "Zh")
	f.Add([]byte{0xb0, 0xa1, 0xb0, 0xa2, 0xff}, "ko")
	f.Add([]byte("1\n\x1b$@$\"$N\x1b(J\\~\n"), "ja")
	f.Add([]byte("1\n\x1b$B$\"\x1b$B$N"), "ja")
	f.Add([]byte("1\n\x1b$B$\n\x1b(B"), "ja")
	f.Add([]byte("\x1b(B\x1b$B$\"\x1b(B"), "ja")
	f.Add([]byte{0xFF, 0xFE, 'H', 0, 'i'}, "")
	f.Add([]byte{'a', 0, 'b', 0, 0x00, 0xD8, 'c', 0}, "")
	f.Add([]byte{0x82, 0xa0, 0xfb, 0xfc, 0xfa, 0x40, 0xee, 0xe0}, "ja")
	f.Add([]byte{0xa4, 0x40, 0x9d, 0xf7, 0x90, 0xf1}, "zh-Hant")
	f.Add([]byte{0xa4, 0xa2, 0x8f, 0xb0, 0xa1}, "ja")
	f.Add([]byte("\ufeff\x1b$A\x1b$A0"), "")
	f.Add([]byte("\ufeff\ufeffa"), "")
	f.Fuzz(func(t *testing.T, data []byte, hint string) {
		tag, ok := language.Parse(hint)
		if !ok {
			return
		}
		// Detect reads a page's first SampleBytes only.
		data = data[:min(len(data), SampleBytes)]
		enc := Detect(data, tag)
		if enc == Unknown {
			if again := Detect(data, tag); again != Unknown {
				t.Fatalf("Unknown, then %s on the same bytes", again)
			}
			return
		}
		text, err := enc.Decode(data)
		if err != nil {
			t.Fatalf("Detect names %s for %q, which its Decode refuses: %v", enc, data, err)
		}
		written := writeBack(t, enc, text)
		again := Detect(written, tag)
		if again == enc {
			return
		}
		announced, _ := announcedForm(written)
		ra, earlier := order[announced]
		re, encIsForm := order[enc]
		if earlier && encIsForm && ra < re {
			return
		}
		reread, err := again.Decode(written)
		if err != nil || !bytes.Equal(reread, text) {
			t.Fatalf("%s, then %s on its own output %q: %q, %v", enc, again, written, reread, err)
		}
	})
}
