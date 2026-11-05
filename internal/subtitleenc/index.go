package subtitleenc

import (
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// indexed reads through x/text's decoder and writes each character at the
// code the Encoding Standard's index pointer names, built from that decode
// table, where x/text's encoder departs from it: Big5 writes a duplicated
// character at its last code, an HKSCS one for 港 or 包 that a CP950 reader
// garbles (https://github.com/golang/go/issues/43581), and Shift_JIS writes
// CP932's IBM extensions such as 髙 at NEC's codes, which the standard skips
// (https://encoding.spec.whatwg.org/#index-shift_jis-pointer).
type indexed struct {
	dec   encoding.Encoding
	codes func() map[rune]string
}

func (c indexed) decode(b []byte) ([]byte, bool) { return xtext{c.dec}.decode(b) }

func (c indexed) encode(text []byte) ([]byte, bool) {
	codes := c.codes()
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		code, ok := codes[r]
		switch {
		case r < utf8.RuneSelf:
			out = append(out, text[i])
		case !ok:
			return nil, false
		default:
			out = append(out, code...)
		}
		i += size
	}
	return out, true
}

// big5Codes is the index Big5 pointer: of a character's codes from lead
// 0xA1 on, the first, but the last for the six characters the standard
// names. A character only a code below lead 0xA1 holds, an HKSCS one the
// standard writes nowhere, keeps the first of those, so a Hong Kong file
// using that code reads back as written.
var big5Codes = sync.OnceValue(func() map[rune]string {
	takesLast := map[rune]bool{0x2550: true, 0x255E: true, 0x2561: true, 0x256A: true, 0x5341: true, 0x5345: true}
	dec := traditionalchinese.Big5.NewDecoder()
	codes, hkscs := map[rune]string{}, map[rune]string{}
	for lead := 0x81; lead <= 0xFE; lead++ {
		for trail := 0x40; trail <= 0xFE; trail++ {
			if trail > 0x7E && trail < 0xA1 {
				continue
			}
			code := string([]byte{byte(lead), byte(trail)})
			r, ok := decodeOne(dec, code)
			switch {
			case !ok:
			case lead < 0xA1:
				if _, seen := hkscs[r]; !seen {
					hkscs[r] = code
				}
			default:
				if _, seen := codes[r]; !seen || takesLast[r] {
					codes[r] = code
				}
			}
		}
	}
	for r, code := range hkscs {
		if _, seen := codes[r]; !seen {
			codes[r] = code
		}
	}
	return codes
})

// shiftJISCodes is the index Shift_JIS pointer: the first of a character's
// two-byte codes outside pointers 8272 to 8835, and the single bytes 0x80
// and 0xA1 to 0xDF.
var shiftJISCodes = sync.OnceValue(func() map[rune]string {
	dec := japanese.ShiftJIS.NewDecoder()
	codes := map[rune]string{}
	add := func(code string) {
		if r, ok := decodeOne(dec, code); ok {
			if _, seen := codes[r]; !seen {
				codes[r] = code
			}
		}
	}
	add("\x80")
	for b := 0xA1; b <= 0xDF; b++ {
		add(string([]byte{byte(b)}))
	}
	for pointer := range 60 * 188 {
		if 8272 <= pointer && pointer <= 8835 {
			continue
		}
		lead, trail := pointer/188, pointer%188
		lead += 0x81
		if lead > 0x9F {
			lead += 0x40
		}
		trail += 0x40
		if trail > 0x7E {
			trail++
		}
		add(string([]byte{byte(lead), byte(trail)}))
	}
	return codes
})

// decodeOne reads code through dec as one character; an undefined code,
// which x/text reads as U+FFFD, or one reading as two characters is none.
func decodeOne(dec transform.Transformer, code string) (rune, bool) {
	var buf [2 * utf8.UTFMax]byte
	dec.Reset()
	n, _, err := dec.Transform(buf[:], []byte(code), true)
	r, size := utf8.DecodeRune(buf[:n])
	return r, err == nil && size == n && r != utf8.RuneError
}
