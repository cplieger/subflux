package subtitleenc

import (
	"bytes"
	"fmt"
	"slices"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
	bomUTF32LE = []byte{0xFF, 0xFE, 0, 0}
	bomUTF32BE = []byte{0, 0, 0xFE, 0xFF}
)

// codec reads and writes one Encoding's bytes whole; encode is false for a
// character it has no code for.
type codec interface {
	decode(b []byte) ([]byte, bool)
	encode(text []byte) ([]byte, bool)
}

// codecs holds every named Encoding's codec. The UTF-16 ones ignore a BOM:
// Decode strips one only when it matches the byte order, and Encode writes
// it.
var codecs = [...]codec{
	UTF8:        utf8Codec{},
	UTF16LE:     xtext{unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)},
	UTF16BE:     xtext{unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)},
	ISO2022JP:   iso2022jp{},
	Windows1250: xtext{charmap.Windows1250},
	Windows1251: xtext{charmap.Windows1251},
	Windows1252: xtext{charmap.Windows1252},
	Windows1253: xtext{charmap.Windows1253},
	Windows1254: xtext{charmap.Windows1254},
	Windows1255: xtext{charmap.Windows1255},
	Windows1256: xtext{charmap.Windows1256},
	ISO8859_2:   xtext{charmap.ISO8859_2},
	ISO8859_5:   xtext{charmap.ISO8859_5},
	ISO8859_6:   xtext{charmap.ISO8859_6},
	ISO8859_7:   xtext{charmap.ISO8859_7},
	ISO8859_8:   xtext{charmap.ISO8859_8},
	KOI8R:       xtext{charmap.KOI8R},
	KOI8U:       xtext{charmap.KOI8U},
	ShiftJIS:    indexed{japanese.ShiftJIS, shiftJISCodes},
	EUCJP:       xtext{japanese.EUCJP},
	GB18030:     xtext{simplifiedchinese.GB18030},
	Big5:        indexed{traditionalchinese.Big5, big5Codes},
	EUCKR:       xtext{korean.EUCKR},
}

// xtext is an x/text codec, for the pages whose two-byte codes x/text's
// encoder already writes at the standard's pointer
// (TestEncodeWritesTheStandardCode).
type xtext struct{ encoding.Encoding }

func (c xtext) decode(b []byte) ([]byte, bool) {
	out, err := c.NewDecoder().Bytes(b)
	return out, err == nil
}

func (c xtext) encode(text []byte) ([]byte, bool) {
	out, err := c.NewEncoder().Bytes(text)
	return out, err == nil
}

type utf8Codec struct{}

func (utf8Codec) decode(b []byte) ([]byte, bool) { return slices.Clone(b), utf8.Valid(b) }

func (utf8Codec) encode(text []byte) ([]byte, bool) { return slices.Clone(text), utf8.Valid(text) }

func (e Encoding) codec() (codec, bool) {
	if e == Unknown || int(e) >= len(codecs) {
		return nil, false
	}
	return codecs[e], true
}

// Decode returns b as UTF-8 in a new slice, without a BOM. Unknown, and
// bytes the encoding's encoder would not write back from the text (read),
// give ErrUndecodable.
func (e Encoding) Decode(b []byte) ([]byte, error) {
	if _, ok := e.codec(); !ok {
		return nil, fmt.Errorf("%w: %s", ErrUndecodable, e)
	}
	text, ok := e.read(e.trimBOM(b))
	if !ok {
		return nil, fmt.Errorf("%w: %s does not write these bytes back", ErrUndecodable, e)
	}
	return text, nil
}

// read is the acceptance rule Decode and every Detect step apply: the codec
// decodes b and its encoder writes the text back as b. ISO-2022-JP, stateful,
// writes one text in many escape forms, so its written form need only read
// as the same text. An undefined byte, a broken sequence, an odd UTF-16
// length or a lone surrogate (x/text reads each as U+FFFD), Big5's HKSCS
// pairs such as 0x88 0x62, and a code the standard writes elsewhere, such as
// an HKSCS duplicate or GBK's 0x80 for €, all fail it.
func (e Encoding) read(b []byte) ([]byte, bool) {
	c, ok := e.codec()
	if !ok {
		return nil, false
	}
	text, ok := c.decode(b)
	if !ok {
		return nil, false
	}
	back, ok := c.encode(text)
	if !ok {
		return nil, false
	}
	if e == ISO2022JP {
		again, ok := c.decode(back)
		return text, ok && bytes.Equal(again, text)
	}
	return text, bytes.Equal(back, b)
}

// Encode writes valid UTF-8 text in the encoding, UTF-16 behind its BOM so
// Detect names it again, as bytes Decode reads back as the same text.
// Unknown, invalid UTF-8, a character the encoding has no code for, and text
// Decode would read back otherwise (GB18030's U+E000) give ErrUndecodable.
func (e Encoding) Encode(text []byte) ([]byte, error) {
	c, ok := e.codec()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUndecodable, e)
	}
	if !utf8.Valid(text) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrUndecodable)
	}
	out, ok := c.encode(text)
	if !ok {
		return nil, fmt.Errorf("%w: %s has no code for a character of the text", ErrUndecodable, e)
	}
	switch e {
	case UTF16LE:
		out = append(slices.Clip(bomUTF16LE), out...)
	case UTF16BE:
		out = append(slices.Clip(bomUTF16BE), out...)
	case UTF8:
		// Decode strips one BOM, so a text opening with U+FEFF keeps it
		// only behind a written one.
		if bytes.HasPrefix(out, bomUTF8) {
			out = append(slices.Clip(bomUTF8), out...)
		}
	}
	if back, err := e.Decode(out); err != nil || !bytes.Equal(back, text) {
		return nil, fmt.Errorf("%w: %s does not read the written text back", ErrUndecodable, e)
	}
	return out, nil
}

// ASCIITransparent is false for UTF-16 and ISO-2022-JP: in every other, a
// line made only of bytes below 0x80 reads as ASCII.
func (e Encoding) ASCIITransparent() bool {
	switch e {
	case UTF16LE, UTF16BE, ISO2022JP:
		return false
	default:
		return true
	}
}

func (e Encoding) trimBOM(b []byte) []byte {
	switch e {
	case UTF8:
		return bytes.TrimPrefix(b, bomUTF8)
	case UTF16LE:
		return bytes.TrimPrefix(b, bomUTF16LE)
	case UTF16BE:
		return bytes.TrimPrefix(b, bomUTF16BE)
	default:
		return b
	}
}
