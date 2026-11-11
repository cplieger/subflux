package subtitleenc

import (
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
)

// iso2022jp is the Encoding Standard's ISO-2022-JP, its decoder in fatal
// mode (https://encoding.spec.whatwg.org/#iso-2022-jp). x/text's decoder
// also reads RFC 2237's JIS X 0212, takes ESC ( J as ASCII, and reads a
// byte outside 0x21 to 0x7E as half of a character ("ESC $ B $ LF" is ξ).
type iso2022jp struct{}

type jisState uint8

const (
	jisASCII jisState = iota
	jisRoman
	jisKatakana
	jisDouble
)

var jisEscapes = map[string]jisState{
	"\x1b(B": jisASCII, "\x1b(J": jisRoman, "\x1b(I": jisKatakana, "\x1b$@": jisDouble, "\x1b$B": jisDouble,
}

// jis0208 is index jis0208 below pointer 94 × 94, the part ISO-2022-JP
// addresses, read off x/text's EUC-JP decode table: each pointer's
// character, zero where it has none, and the two bytes of each
// character's first pointer.
type jis0208 struct {
	codes map[rune][2]byte
	chars [94 * 94]rune
}

var jisIndex = sync.OnceValue(func() *jis0208 {
	dec := japanese.EUCJP.NewDecoder()
	t := &jis0208{codes: map[rune][2]byte{}}
	for lead := byte(0x21); lead <= 0x7E; lead++ {
		for trail := byte(0x21); trail <= 0x7E; trail++ {
			r, ok := decodeOne(dec, string([]byte{lead | 0x80, trail | 0x80}))
			if !ok {
				continue
			}
			t.chars[int(lead-0x21)*94+int(trail-0x21)] = r
			if _, seen := t.codes[r]; !seen {
				t.codes[r] = [2]byte{lead, trail}
			}
		}
	}
	return t
})

// decode refuses an escape straight after another, as the standard's
// decoder does with its output flag.
func (iso2022jp) decode(b []byte) ([]byte, bool) {
	out := make([]byte, 0, len(b))
	state, escaped := jisASCII, false
	for i := 0; i < len(b); {
		if b[i] == 0x1B {
			next, ok := jisEscapes[string(b[i:min(i+3, len(b))])]
			if !ok || escaped {
				return nil, false
			}
			state, escaped, i = next, true, i+3
			continue
		}
		r, size := state.read(b[i:])
		if size == 0 {
			return nil, false
		}
		out = utf8.AppendRune(out, r)
		escaped, i = false, i+size
	}
	return out, true
}

// read is the character b starts with in state s and its length, 0 for
// bytes the state does not read.
func (s jisState) read(b []byte) (r rune, size int) {
	c := b[0]
	switch {
	case s == jisDouble:
		return readJIS0208(b)
	case s == jisKatakana:
		if c < 0x21 || c > 0x5F {
			return 0, 0
		}
		return 0xFF61 - 0x21 + rune(c), 1
	case c > 0x7F || c == 0x0E || c == 0x0F:
		return 0, 0
	case s == jisRoman && c == 0x5C:
		return 0xA5, 1
	case s == jisRoman && c == 0x7E:
		return 0x203E, 1
	default:
		return rune(c), 1
	}
}

func readJIS0208(b []byte) (r rune, size int) {
	if len(b) < 2 || !jisByte(b[0]) || !jisByte(b[1]) {
		return 0, 0
	}
	r = jisIndex().chars[int(b[0]-0x21)*94+int(b[1]-0x21)]
	if r == 0 {
		return 0, 0
	}
	return r, 2
}

func jisByte(c byte) bool { return 0x21 <= c && c <= 0x7E }

// encode is the standard's encoder, except that a character it writes as
// another one (U+2212 as U+FF0D, halfwidth katakana as fullwidth) has no
// code here, so read refuses bytes decoding to one.
func (iso2022jp) encode(text []byte) ([]byte, bool) {
	codes := jisIndex().codes
	out := make([]byte, 0, len(text)+6)
	state := jisASCII
	to := func(s jisState, esc string) {
		if state != s {
			out = append(out, esc...)
			state = s
		}
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		switch {
		case r == 0x0E || r == 0x0F || r == 0x1B:
			return nil, false
		case r < utf8.RuneSelf:
			if state != jisRoman || r == 0x5C || r == 0x7E {
				to(jisASCII, "\x1b(B")
			}
			out = append(out, text[i])
		case r == 0xA5:
			to(jisRoman, "\x1b(J")
			out = append(out, 0x5C)
		case r == 0x203E:
			to(jisRoman, "\x1b(J")
			out = append(out, 0x7E)
		default:
			code, ok := codes[r]
			if !ok {
				return nil, false
			}
			to(jisDouble, "\x1b$B")
			out = append(out, code[:]...)
		}
		i += size
	}
	to(jisASCII, "\x1b(B")
	return out, true
}
