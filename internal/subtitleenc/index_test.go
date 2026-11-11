package subtitleenc

import (
	"bytes"
	"fmt"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// pointerRule is one multibyte page as the Encoding Standard numbers its
// two-byte codes (https://encoding.spec.whatwg.org/#legacy-multi-byte-chinese-(simplified)-encodings
// and the sections after it).
type pointerRule struct {
	enc      Encoding
	dec      encoding.Encoding
	pointers int
	code     func(pointer int) []byte
	// written reports whether the standard's encoder writes a character at
	// a pointer: false for the pointers its index pointer excludes.
	written   func(pointer int) bool
	takesLast map[rune]bool
	// keepsUnwritten: a character only excluded pointers hold keeps its
	// first code, where the standard writes it nowhere (design 7).
	keepsUnwritten bool
}

func trailOffset(trail, below, low, high int) int {
	if trail < below {
		return trail + low
	}
	return trail + high
}

var pointerRules = []pointerRule{
	{
		enc: Big5, dec: traditionalchinese.Big5, pointers: 126 * 157,
		code: func(p int) []byte {
			return []byte{byte(p/157 + 0x81), byte(trailOffset(p%157, 0x3F, 0x40, 0x62))}
		},
		written:        func(p int) bool { return p >= (0xA1-0x81)*157 },
		takesLast:      map[rune]bool{0x2550: true, 0x255E: true, 0x2561: true, 0x256A: true, 0x5341: true, 0x5345: true},
		keepsUnwritten: true,
	},
	{
		enc: ShiftJIS, dec: japanese.ShiftJIS, pointers: 60 * 188,
		code: func(p int) []byte {
			return []byte{byte(trailOffset(p/188, 0x1F, 0x81, 0xC1)), byte(trailOffset(p%188, 0x3F, 0x40, 0x41))}
		},
		written: func(p int) bool { return p < 8272 || p > 8835 },
	},
	{
		enc: EUCJP, dec: japanese.EUCJP, pointers: 94 * 94,
		code:    func(p int) []byte { return []byte{byte(p/94 + 0xA1), byte(p%94 + 0xA1)} },
		written: func(int) bool { return true },
	},
	{
		enc: GB18030, dec: simplifiedchinese.GB18030, pointers: 126 * 190,
		code: func(p int) []byte {
			return []byte{byte(p/190 + 0x81), byte(trailOffset(p%190, 0x3F, 0x40, 0x41))}
		},
		written: func(int) bool { return true },
	},
	{
		enc: EUCKR, dec: korean.EUCKR, pointers: 126 * 190,
		code:    func(p int) []byte { return []byte{byte(p/190 + 0x81), byte(p%190 + 0x41)} },
		written: func(int) bool { return true },
	},
}

// standardCodes is the rule's pointer for every character a two-byte code
// decodes to as one, and every code with its character.
func (pr pointerRule) standardCodes(t *testing.T) (want map[rune][]byte, codes map[string]rune) {
	t.Helper()
	want, codes = map[rune][]byte{}, map[string]rune{}
	unwritten := map[rune][]byte{}
	for p := range pr.pointers {
		code := pr.code(p)
		out, err := pr.dec.NewDecoder().Bytes(code)
		r, size := utf8.DecodeRune(out)
		if err != nil || len(out) == 0 || size != len(out) || r == utf8.RuneError {
			continue
		}
		codes[string(code)] = r
		_, seen := want[r]
		switch {
		case !pr.written(p):
			if _, kept := unwritten[r]; !kept {
				unwritten[r] = code
			}
		case !seen || pr.takesLast[r]:
			want[r] = code
		}
	}
	for r, code := range unwritten {
		if _, ok := want[r]; !ok && pr.keepsUnwritten {
			want[r] = code
		}
	}
	return want, codes
}

// TestEncodeWritesTheStandardCode drives every two-byte code of each
// multibyte page through its decoder: the codec read checks against writes
// each character at the pointer the standard's index pointer names, and
// Decode accepts a code only when it is that one. ISO-2022-JP writes jis0208 at EUC-JP's pointer.
func TestEncodeWritesTheStandardCode(t *testing.T) {
	t.Parallel()
	for _, pr := range pointerRules {
		t.Run(pr.enc.String(), func(t *testing.T) {
			t.Parallel()
			want, codes := pr.standardCodes(t)
			if len(want) < 7000 {
				t.Fatalf("the decode table gives %d characters", len(want))
			}
			var bad []string
			for r, code := range want {
				if got, ok := codecs[pr.enc].encode([]byte(string(r))); !ok || !bytes.Equal(got, code) {
					bad = append(bad, fmt.Sprintf("encode(%U) = % X, %t; want % X", r, got, ok, code))
				}
				if pr.enc != EUCJP {
					continue
				}
				iso := append(append([]byte("\x1b$B"), code[0]-0x80, code[1]-0x80), "\x1b(B"...)
				if got, ok := codecs[ISO2022JP].encode([]byte(string(r))); !ok || !bytes.Equal(got, iso) {
					bad = append(bad, fmt.Sprintf("ISO2022JP encode(%U) = % X, %t; want % X", r, got, ok, iso))
				}
			}
			for code, r := range codes {
				_, err := pr.enc.Decode([]byte(code))
				if accepted := bytes.Equal(want[r], []byte(code)); (err == nil) != accepted {
					bad = append(bad, fmt.Sprintf("Decode(% X) error %v; the standard writes %U at % X", code, err, r, want[r]))
				}
			}
			for i, b := range bad {
				if i == 5 {
					t.Errorf("and %d more", len(bad)-5)
					break
				}
				t.Error(b)
			}
		})
	}
}

// TestEncodeStandardRows pins codes read off the standard's index files
// (index-big5.txt and index-jis0208.txt, 2024-09-18) and the departures
// design 7 records.
func TestEncodeStandardRows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		enc  Encoding
		text string
		want []byte
	}{
		{"big5_first_from_lead_a1", Big5, "包港響", []byte{0xA5, 0x5D, 0xB4, 0xE4, 0xC5, 0x54}},
		{"big5_last_for_the_named_six", Big5, "═十", []byte{0xF9, 0xF9, 0xA4, 0x51}},
		{"big5_hkscs_only_keeps_its_code", Big5, "咗", []byte{0x9D, 0xF7}},
		{"shiftjis_ibm_extensions", ShiftJIS, "髙ⅰ＂﨑", []byte{0xFB, 0xFC, 0xFA, 0x40, 0xFA, 0x57, 0xFA, 0xB1}},
		{"shiftjis_first_outside_nec_selection", ShiftJIS, "\uFFE2∵", []byte{0x81, 0xCA, 0x81, 0xE6}},
		{"shiftjis_single_bytes", ShiftJIS, "\u0080ｱ", []byte{0x80, 0xB1}},
		{"eucjp_first_pointer_nec_selection_included", EUCJP, "髙", []byte{0xFC, 0xE2}},
		{"eucjp_jis_x_0212_keeps_its_code", EUCJP, "丂", []byte{0x8F, 0xB0, 0xA1}},
		{"eucjp_halfwidth_katakana", EUCJP, "ｱ", []byte{0x8E, 0xB1}},
		{"iso2022jp_yen_in_jis_roman", ISO2022JP, "¥", []byte("\x1b(J\\\x1b(B")},
		{"iso2022jp_roman_keeps_other_ascii", ISO2022JP, "a¥b~", []byte("a\x1b(J\\b\x1b(B~")},
		{"iso2022jp_overline_then_kanji", ISO2022JP, "‾髙", []byte("\x1b(J~\x1b$B|b\x1b(B")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := codecs[tt.enc].encode([]byte(tt.text))
			if !ok || !bytes.Equal(got, tt.want) {
				t.Errorf("encode(%q) = % X, %t; want % X", tt.text, got, ok, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name string
		enc  Encoding
		text string
	}{
		{"iso2022jp_halfwidth_katakana", ISO2022JP, "ｱ"},
		{"iso2022jp_minus_sign", ISO2022JP, "−"},
		{"iso2022jp_escape_byte", ISO2022JP, "\x1b(B"},
		{"shiftjis_yen", ShiftJIS, "¥"},
		{"big5_replacement_character", Big5, "\uFFFD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := codecs[tt.enc].encode([]byte(tt.text)); ok {
				t.Errorf("encode(%q) = % X; want no code", tt.text, got)
			}
		})
	}
}
