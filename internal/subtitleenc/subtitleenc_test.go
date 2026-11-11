package subtitleenc

import (
	"bytes"
	"errors"
	"testing"
)

// mustEncode writes text in e through the codec read checks against,
// failing the test when e cannot carry it.
func mustEncode(t *testing.T, e Encoding, text string) []byte {
	t.Helper()
	b, ok := codecs[e].encode([]byte(text))
	if !ok {
		t.Fatalf("%s has no code for a character of %q", e, text)
	}
	return b
}

// TestEncodingValuesAppendOnly pins every value's number: timing's baseline
// codec stores them.
func TestEncodingValuesAppendOnly(t *testing.T) {
	t.Parallel()
	golden := []struct {
		e    Encoding
		name string
		n    uint8
	}{
		{Unknown, "Unknown", 0},
		{UTF8, "UTF8", 1},
		{UTF16LE, "UTF16LE", 2},
		{UTF16BE, "UTF16BE", 3},
		{ISO2022JP, "ISO2022JP", 4},
		{Windows1250, "Windows1250", 5},
		{Windows1251, "Windows1251", 6},
		{Windows1252, "Windows1252", 7},
		{Windows1253, "Windows1253", 8},
		{Windows1254, "Windows1254", 9},
		{Windows1255, "Windows1255", 10},
		{Windows1256, "Windows1256", 11},
		{ISO8859_2, "ISO8859_2", 12},
		{ISO8859_5, "ISO8859_5", 13},
		{ISO8859_6, "ISO8859_6", 14},
		{ISO8859_7, "ISO8859_7", 15},
		{ISO8859_8, "ISO8859_8", 16},
		{KOI8R, "KOI8R", 17},
		{KOI8U, "KOI8U", 18},
		{ShiftJIS, "ShiftJIS", 19},
		{EUCJP, "EUCJP", 20},
		{GB18030, "GB18030", 21},
		{Big5, "Big5", 22},
		{EUCKR, "EUCKR", 23},
	}
	if len(golden) != len(encodingNames) {
		t.Errorf("%d golden values for %d names", len(golden), len(encodingNames))
	}
	for _, g := range golden {
		if uint8(g.e) != g.n {
			t.Errorf("%s is %d, stored as %d", g.name, g.e, g.n)
		}
		if g.e.String() != g.name {
			t.Errorf("Encoding(%d).String() = %q, want %q", g.n, g.e.String(), g.name)
		}
		if _, ok := g.e.codec(); ok == (g.e == Unknown) {
			t.Errorf("%s: codec present %v", g.name, ok)
		}
	}
	if got := Encoding(24).String(); got != "Encoding(24)" {
		t.Errorf("Encoding(24).String() = %q", got)
	}
}

func TestValid(t *testing.T) {
	t.Parallel()
	for e := Unknown; e <= EUCKR; e++ {
		if !e.Valid() {
			t.Errorf("%s.Valid() = false", e)
		}
	}
	for _, e := range []Encoding{EUCKR + 1, Encoding(255)} {
		if e.Valid() {
			t.Errorf("%s.Valid() = true", e)
		}
	}
}

func TestUnknownNeverDecoded(t *testing.T) {
	t.Parallel()
	for _, e := range []Encoding{Unknown, Encoding(24), Encoding(255)} {
		if out, err := e.Decode([]byte("plain text")); !errors.Is(err, errUndecodable) || out != nil {
			t.Errorf("%s.Decode = %q, %v; want nil, errUndecodable", e, out, err)
		}
	}
}

func TestASCIITransparent(t *testing.T) {
	t.Parallel()
	for e := Unknown; int(e) < len(encodingNames); e++ {
		want := e != UTF16LE && e != UTF16BE && e != ISO2022JP
		if got := e.ASCIITransparent(); got != want {
			t.Errorf("%s.ASCIITransparent() = %v, want %v", e, got, want)
		}
		if !want || e == Unknown {
			continue
		}
		// The claim itself: an ASCII line reads as ASCII.
		line := []byte("00:00:01,000 --> 00:00:02,500\n")
		if out, err := e.Decode(line); err != nil || !bytes.Equal(out, line) {
			t.Errorf("%s.Decode(ASCII line) = %q, %v", e, out, err)
		}
	}
}

func TestDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		e    Encoding
		in   []byte
		want string // "" with fail set: errUndecodable
		fail bool
	}{
		{name: "utf8_bom_stripped", e: UTF8, in: []byte("\xef\xbb\xbfHi"), want: "Hi"},
		{name: "utf8_invalid", e: UTF8, in: []byte("Hi\xff"), fail: true},
		{name: "utf16le_bom_stripped", e: UTF16LE, in: []byte{0xFF, 0xFE, 'H', 0, 'i', 0}, want: "Hi"},
		{name: "utf16le_no_bom", e: UTF16LE, in: []byte{'H', 0, 'i', 0}, want: "Hi"},
		{name: "utf16be_bom_stripped", e: UTF16BE, in: []byte{0xFE, 0xFF, 0, 'H', 0, 'i'}, want: "Hi"},
		{name: "utf8_leading_bom_character_behind_a_bom", e: UTF8, in: []byte{0xEF, 0xBB, 0xBF, 0xEF, 0xBB, 0xBF, 'a'}, want: "\uFEFFa"},
		{name: "utf16le_leading_bom_character_behind_a_bom", e: UTF16LE, in: []byte{0xFF, 0xFE, 0xFF, 0xFE, 'a', 0}, want: "\uFEFFa"},
		{name: "utf16le_surrogate_pair", e: UTF16LE, in: []byte{0x3D, 0xD8, 0x00, 0xDE}, want: "😀"},
		{name: "utf16le_literal_replacement_char", e: UTF16LE, in: []byte{0xFD, 0xFF, 'a', 0}, want: "\uFFFDa"},
		{name: "utf16le_odd_length", e: UTF16LE, in: []byte{'H', 0, 'i'}, fail: true},
		{name: "utf16le_lone_surrogate", e: UTF16LE, in: []byte{'a', 0, 0x00, 0xD8, 'b', 0}, fail: true},
		{name: "windows1252_smart_quotes", e: Windows1252, in: []byte{0x93, 'a', 0x94, 0x85}, want: "“a”…"},
		{name: "windows1253_undefined_byte", e: Windows1253, in: []byte{'a', 0xAA}, fail: true},
		{name: "iso8859_8_undefined_byte", e: ISO8859_8, in: []byte{'a', 0xD0}, fail: true},
		{name: "gb18030_literal_replacement_char", e: GB18030, in: []byte{0x84, 0x31, 0xA4, 0x37}, want: "\uFFFD"},
		{name: "gb18030_broken_pair", e: GB18030, in: []byte{0x81, 0x20}, fail: true},
		{name: "shiftjis_broken_pair", e: ShiftJIS, in: []byte{0x82, 0x20, 'a'}, fail: true},
		{name: "big5_hkscs_composed_pair", e: Big5, in: []byte{0xA4, 0x40, 0x88, 0x62}, fail: true},
		{name: "big5_standard_code", e: Big5, in: []byte{0xA5, 0x5D, 0xBC, 0xB5}, want: "包撐"},
		{name: "big5_hkscs_duplicate_code", e: Big5, in: []byte{0xFA, 0xBD}, fail: true},
		{name: "gbk_euro_byte", e: GB18030, in: []byte{0x80}, fail: true},
		{name: "gb18030_a3a0_duplicate", e: GB18030, in: []byte{0xA3, 0xA0}, fail: true},
		{name: "big5_hkscs_only_code", e: Big5, in: []byte{0x9D, 0xF7}, want: "咗"},
		{name: "big5_hkscs_lead_duplicate", e: Big5, in: []byte{0x90, 0xF1}, fail: true},
		{name: "cp932_ibm_extensions", e: ShiftJIS, in: []byte{0xFB, 0xFC, 0xFA, 0x40, 0xFA, 0x57}, want: "髙ⅰ＂"},
		{name: "shiftjis_nec_selected_duplicate", e: ShiftJIS, in: []byte{0xEE, 0xE0}, fail: true},
		{name: "shiftjis_byte_0x80", e: ShiftJIS, in: []byte{'a', 0x80}, want: "a\u0080"},
		{name: "eucjp_jis_x_0212", e: EUCJP, in: []byte{0x8F, 0xB0, 0xA1}, want: "丂"},
		{name: "eucjp_nec_row_13_duplicate", e: EUCJP, in: []byte{0xAD, 0xFA}, fail: true},
		{name: "iso2022jp", e: ISO2022JP, in: []byte("\x1b$B$\"\x1b(B ok"), want: "あ ok"},
		{name: "iso2022jp_esc_dollar_at", e: ISO2022JP, in: []byte("\x1b$@$\"$N\x1b(B"), want: "あの"},
		{name: "iso2022jp_jis_roman", e: ISO2022JP, in: []byte("\x1b$B$\"$N\x1b(J\\~a\x1b(B"), want: "あの¥‾a"},
		{name: "iso2022jp_no_closing_escape", e: ISO2022JP, in: []byte("1\n\x1b$B$\"$N"), want: "1\nあの"},
		{name: "iso2022jp_redundant_escape", e: ISO2022JP, in: []byte("\x1b$B$\"\x1b$B$N\x1b(B"), want: "あの"},
		{name: "iso2022jp_leading_ascii_escape", e: ISO2022JP, in: []byte("\x1b(B1\n\x1b$B$\"$N\x1b(B\n"), want: "1\nあの\n"},
		{name: "iso2022jp_unknown_escape", e: ISO2022JP, in: []byte("ok \x1b$Z"), fail: true},
		{name: "iso2022jp_escape_after_escape", e: ISO2022JP, in: []byte("\x1b$B\x1b(Bok"), fail: true},
		{name: "iso2022jp_line_feed_inside_jis", e: ISO2022JP, in: []byte("\x1b$B$\"\n\x1b(B"), fail: true},
		{name: "iso2022jp_trail_outside_jis", e: ISO2022JP, in: []byte("\x1b$B$\n\x1b(B"), fail: true},
		{name: "iso2022jp_half_a_character", e: ISO2022JP, in: []byte("\x1b$B$\"$"), fail: true},
		{name: "iso2022jp_jis_x_0212", e: ISO2022JP, in: []byte("\x1b$(D0!\x1b(B"), fail: true},
		{name: "iso2022jp_halfwidth_katakana", e: ISO2022JP, in: []byte("\x1b(I1\x1b(B"), fail: true},
		{name: "iso2022jp_8bit_byte", e: ISO2022JP, in: []byte("caf\xe9"), fail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := bytes.Clone(tt.in)
			out, err := tt.e.Decode(in)
			if tt.fail {
				if !errors.Is(err, errUndecodable) || out != nil {
					t.Errorf("Decode = %q, %v; want nil, errUndecodable", out, err)
				}
				return
			}
			if err != nil || string(out) != tt.want {
				t.Errorf("Decode = %q, %v; want %q", out, err, tt.want)
			}
			if len(out) > 0 {
				out[0] ^= 0xFF
				if !bytes.Equal(in, tt.in) {
					t.Errorf("Decode's result shares the input's memory")
				}
			}
		})
	}
}
