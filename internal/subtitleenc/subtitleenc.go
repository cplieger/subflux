// Package subtitleenc is the single authority for what encoding a subtitle's
// bytes are in and how to read them as UTF-8. Every site that asks "is this
// text, and what does it say?" reads the same decoder, so the content gate, the
// SRT parser and the post-processing step cannot disagree about whether a file
// is subtitle text or binary.
//
// Normalize COMPUTES a UTF-8 view. It never decides whether that view replaces
// the original bytes, and no caller may treat it as having done so. That split
// is load-bearing: subflux persists a converted subtitle only when
// post_processing.normalize_utf8 is on, and a subtitle otherwise reaches disk
// byte-for-byte as the provider sent it. So a caller that needs to JUDGE the
// bytes (subtitlefile.Validate) or PARSE them (the sync paths) decodes a view
// and throws it away, while post-processing decodes and keeps the result. A
// caller that persists this function's output without consulting that setting
// has silently converted a user's file.
//
// Zero dependencies on the rest of subflux, deliberately: it sits below the
// packages that read subtitle content so any of them can reach it.
package subtitleenc

import (
	"bytes"
	"unicode/utf8"
)

// Common byte order marks.
var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16BE = []byte{0xFE, 0xFF}
	bomUTF16LE = []byte{0xFF, 0xFE}
)

// Encoding is what detection could positively conclude about a subtitle's bytes.
type Encoding int

// The encodings detection can name. Unknown means nothing identified the bytes
// and only the Windows-1252 catch-all would apply, which is a fallback rather
// than a finding: that decoder maps every possible byte to some rune, so it
// turns arbitrary binary into text-shaped output.
const (
	Unknown Encoding = iota
	UTF8
	UTF16LE
	UTF16BE
)

// Detect reports the encoding it can positively identify, from a byte-order mark
// or from the NUL-interleaving pattern of ASCII-dominant UTF-16.
//
// The pattern check must run before the utf8.Valid test, because NUL-interleaved
// ASCII ('H' 0x00 'i' 0x00) is itself technically valid UTF-8 and would
// otherwise be named UTF8 and passed through uncorrected. The pattern is only
// reliable for ASCII-dominant text: non-ASCII-heavy content with no BOM can be
// read BE for LE or the reverse, which real subtitle files never are.
//
// Normalize decodes strictly more than this names, on purpose: it must always
// produce something, so it ends at Windows-1252 and at NUL stripping. A caller
// deciding whether bytes are TEXT must not lean on either, because both accept
// anything, so it asks this instead.
func Detect(data []byte) Encoding {
	if bytes.HasPrefix(data, bomUTF16BE) {
		return UTF16BE
	}
	if bytes.HasPrefix(data, bomUTF16LE) {
		return UTF16LE
	}
	body := bytes.TrimPrefix(data, bomUTF8)
	if len(body) >= 4 {
		if body[0] == 0 && body[1] != 0 && body[2] == 0 && body[3] != 0 {
			return UTF16BE
		}
		if body[0] != 0 && body[1] == 0 && body[2] != 0 && body[3] == 0 {
			return UTF16LE
		}
	}
	if utf8.Valid(body) {
		return UTF8
	}
	return Unknown
}

// TextView returns the bytes a text probe should judge, decoding first only when
// the bytes cannot be judged without it. It is the one answer to "what do I run a
// text test against?", so the content gate and the archive probe cannot disagree.
//
// A UTF-16 subtitle is about half NUL bytes, so any byte-level text test calls it
// binary; measured, that refused four of the eight members of a real SubSource
// pack. Decoding is therefore necessary for that family and for no other: an
// encoding this package could not NAME gets its raw bytes back, so a text verdict
// is never manufactured by the Windows-1252 fallback or by NUL stripping.
//
// The result is a view. It is never the bytes anyone persists; see the package
// comment.
func TextView(data []byte) []byte {
	switch Detect(data) {
	case UTF16LE, UTF16BE:
		return Normalize(data)
	case Unknown, UTF8:
		return data
	}
	return data
}

// Normalize returns the UTF-8 view of subtitle data, detecting UTF-8 (with or
// without BOM), UTF-16 LE/BE (with or without BOM), and the common single-byte
// encodings (Windows-1252, ISO-8859-1).
//
// Any byte-order mark and every embedded NUL byte are removed. When the input is
// already valid, BOM-free, NUL-free UTF-8 the original slice is returned (not a
// copy); callers must not mutate the returned slice if they need the original
// data unchanged. The result is a fixed point: Normalize(Normalize(x)) always
// equals Normalize(x), which is what lets a caller decode for a decision and a
// later step decode again for persistence without the two disagreeing.
//
// The caller owns persistence. See the package comment.
func Normalize(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	out := decodeToUTF8(data)
	// Drop embedded NUL bytes BEFORE stripping BOMs. A NUL never appears in
	// legitimate subtitle text; it only arises when malformed input decodes to
	// U+0000 code points (e.g. a UTF-16 BOM followed by 0x0000 code units). Left
	// in place, a NUL breaks idempotency: detectBOMlessUTF16 keys on NUL bytes at
	// alternating positions, so a second Normalize pass would misread the
	// already-decoded output as BOM-less UTF-16 and re-decode it into different
	// bytes. Removing every NUL guarantees the second pass falls through to the
	// utf8.Valid fast path and returns the bytes unchanged. Done first so a NUL
	// sitting in front of a BOM (e.g. 00 EF BB BF) doesn't hide that BOM from the
	// loop below.
	out = stripNUL(out)
	// Strip ALL leading UTF-8 BOMs from the FINAL result, not just the raw
	// input: a UTF-16 decode can surface one or more U+FEFF (e.g. redundant
	// BOMs as the first post-BOM code units) that re-encode to EF BB BF.
	// A single TrimPrefix leaves a second consecutive BOM in place, which a
	// second pass then strips, breaking idempotency. The caller's contract is
	// BOM-free UTF-8, so loop until no leading BOM remains.
	for bytes.HasPrefix(out, bomUTF8) {
		out = out[len(bomUTF8):]
	}
	return out
}

// stripNUL removes every NUL byte from b. It returns b unchanged (same backing
// array, no allocation) when b contains no NUL, which is the overwhelmingly
// common case for real subtitle text. Removing whole 0x00 bytes preserves UTF-8
// validity: U+0000 is a complete single-byte code point, never part of a
// multi-byte sequence, so deleting it can't split a rune.
func stripNUL(b []byte) []byte {
	if bytes.IndexByte(b, 0) < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c != 0 {
			out = append(out, c)
		}
	}
	return out
}

// decodeToUTF8 converts data to UTF-8 by detecting its encoding. The
// leading-UTF-8-BOM strip is applied by Normalize on the result.
//
// The detection is Detect's, never a second copy of it: two detections that can
// drift apart is precisely how a gate ends up refusing bytes the converter
// would have read. What this adds is the fallback Detect withholds, because
// Normalize must always produce something while a text verdict must not rest on
// a decoder that accepts every byte.
func decodeToUTF8(data []byte) []byte {
	body := bytes.TrimPrefix(data, bomUTF8)
	switch Detect(data) {
	case UTF16BE:
		// A BOM is consumed here; a stream identified by the NUL pattern has
		// none, and TrimPrefix then leaves it whole.
		return decodeUTF16BE(bytes.TrimPrefix(body, bomUTF16BE))
	case UTF16LE:
		return decodeUTF16LE(bytes.TrimPrefix(body, bomUTF16LE))
	case UTF8:
		// Valid UTF-8 with any BOM off the front. Stripping it here is what
		// keeps a UTF-8 BOM followed by non-UTF-8 bytes from surviving as
		// invalid bytes a second pass re-reads as Windows-1252, which would
		// break idempotency.
		return body
	case Unknown:
		return decodeWindows1252(body)
	}

	// Unreachable: Detect returns one of the four above.
	return decodeWindows1252(data)
}

func decodeUTF16LE(data []byte) []byte { return decodeUTF16(data, false) }
func decodeUTF16BE(data []byte) []byte { return decodeUTF16(data, true) }

// decodeUTF16 converts UTF-16 bytes to UTF-8. Set bigEndian to true for
// big-endian byte order, false for little-endian. Lone surrogates are
// replaced with U+FFFD by WriteRune.
func decodeUTF16(data []byte, bigEndian bool) []byte {
	if len(data) < 2 {
		// No code units to decode (e.g. a lone BOM with no payload).
		return nil
	}
	var buf bytes.Buffer
	buf.Grow(len(data)) // rough estimate

	readU16 := func(i int) uint16 {
		if bigEndian {
			return uint16(data[i])<<8 | uint16(data[i+1])
		}
		return uint16(data[i]) | uint16(data[i+1])<<8
	}

	for i := 0; i+1 < len(data); i += 2 {
		code := readU16(i)
		if isSurrogateHigh(code) && i+3 < len(data) {
			low := readU16(i + 2)
			if isSurrogateLow(low) {
				buf.WriteRune(decodeSurrogatePair(code, low))
				i += 2
				continue
			}
		}
		buf.WriteRune(rune(code))
	}
	return buf.Bytes()
}

// decodeWindows1252 converts Windows-1252 encoded bytes to UTF-8.
// Windows-1252 is a superset of ISO-8859-1 with printable characters
// in the 0x80-0x9F range (where ISO-8859-1 has control characters).
func decodeWindows1252(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.Grow(len(data)) // most bytes are ASCII (1:1); non-ASCII expand slightly

	for _, b := range data {
		if b < 0x80 {
			buf.WriteByte(b)
			continue
		}
		if b <= 0x9F {
			if r := windows1252[b-0x80]; r != 0 {
				buf.WriteRune(r)
				continue
			}
		}
		// ISO-8859-1 range (0xA0-0xFF) or undefined Windows-1252 byte:
		// code point equals the byte value.
		buf.WriteRune(rune(b))
	}
	return buf.Bytes()
}

func isSurrogateHigh(c uint16) bool { return c >= 0xD800 && c <= 0xDBFF }
func isSurrogateLow(c uint16) bool  { return c >= 0xDC00 && c <= 0xDFFF }

func decodeSurrogatePair(high, low uint16) rune {
	return 0x10000 + rune(high-0xD800)*0x400 + rune(low-0xDC00)
}

// windows1252 maps the 0x80-0x9F range to Unicode code points.
// Bytes outside this range (0xA0-0xFF) map directly to their Unicode
// equivalents (same as ISO-8859-1). Array indexed by b-0x80; zero
// entries (0x81, 0x8D, 0x8F, 0x90, 0x9D) are undefined in Windows-1252
// and fall through to rune(b) in decodeWindows1252.
var windows1252 = [32]rune{
	0x00: 0x20AC, // 0x80 €
	0x01: 0,      // 0x81 undefined
	0x02: 0x201A, // 0x82 ‚
	0x03: 0x0192, // 0x83 ƒ
	0x04: 0x201E, // 0x84 „
	0x05: 0x2026, // 0x85 …
	0x06: 0x2020, // 0x86 †
	0x07: 0x2021, // 0x87 ‡
	0x08: 0x02C6, // 0x88 ˆ
	0x09: 0x2030, // 0x89 ‰
	0x0A: 0x0160, // 0x8A Š
	0x0B: 0x2039, // 0x8B ‹
	0x0C: 0x0152, // 0x8C Œ
	0x0D: 0,      // 0x8D undefined
	0x0E: 0x017D, // 0x8E Ž
	0x0F: 0,      // 0x8F undefined
	0x10: 0,      // 0x90 undefined
	0x11: 0x2018, // 0x91 '
	0x12: 0x2019, // 0x92 '
	0x13: 0x201C, // 0x93 "
	0x14: 0x201D, // 0x94 "
	0x15: 0x2022, // 0x95 •
	0x16: 0x2013, // 0x96 –
	0x17: 0x2014, // 0x97 —
	0x18: 0x02DC, // 0x98 ˜
	0x19: 0x2122, // 0x99 ™
	0x1A: 0x0161, // 0x9A š
	0x1B: 0x203A, // 0x9B ›
	0x1C: 0x0153, // 0x9C œ
	0x1D: 0,      // 0x9D undefined
	0x1E: 0x017E, // 0x9E ž
	0x1F: 0x0178, // 0x9F Ÿ
}
