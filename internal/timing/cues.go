package timing

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleenc"
)

// Format is a subtitle document format; its file extension is .srt, .ass or
// .ssa.
type Format uint8

// The formats; the zero Format is none.
const (
	SRT Format = iota + 1
	ASS
	SSA
)

const (
	maxDocumentBytes = 16 << 20
	// maxParsedBytes bounds the document parse reads after ParseCues' check:
	// WebVTT's SRT conversion at most doubles a document (a 22-byte timing
	// line becomes at most 41 bytes, a payload line gains at most its line
	// break, and the dropped header pays for the last line's), so a stored
	// conversion of a 16 MiB WebVTT decodes.
	maxParsedBytes = 2 * maxDocumentBytes
	maxEvents      = 100_000
)

// Cues is a parsed subtitle document: its bytes, Format and encoding, and
// per event its times and the byte span of its text. The zero Cues holds no
// document.
type Cues struct {
	doc    []byte
	events []cue
	format Format
	enc    subtitleenc.Encoding
}

// span is a byte range [from, to) of the document.
type span struct{ from, to int }

type cue struct {
	style          string
	startMs, endMs int64
	text           span
}

// Event is one cue as the dialogue filter reads it: an SRT cue or an ASS or
// SSA Dialogue line (Comment lines are kept as bytes and never timed).
type Event struct {
	// Text is the decoded view: line breaks as \n, ASS override tags kept.
	Text           string
	Style          string // ASS and SSA only
	StartMs, EndMs int64
}

// Format is the one answer to the document's format.
func (c Cues) Format() Format { return c.format }

// Len is the number of timed events.
func (c Cues) Len() int { return len(c.events) }

// Events returns every timed event in document order.
func (c Cues) Events() []Event {
	out := make([]Event, len(c.events))
	for i, e := range c.events {
		out[i] = Event{
			StartMs: e.startMs,
			EndMs:   e.endMs,
			Text:    c.eventText(e.text),
			Style:   e.style,
		}
	}
	return out
}

var (
	lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	assBreaks  = strings.NewReplacer(`\N`, "\n")
)

func (c Cues) eventText(s span) string {
	text := c.decode(c.doc[s.from:s.to])
	if c.format == SRT {
		return lineBreaks.Replace(text)
	}
	return assBreaks.Replace(text)
}

// decode reads b in the document's encoding; bytes it cannot read (an
// Unknown encoding) are shown as UTF-8 with U+FFFD for invalid sequences.
func (c Cues) decode(b []byte) string {
	if d, err := c.enc.Decode(b); err == nil {
		return string(d)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// ParseCues decides the format from the bytes, never a name: SRT, ASS, SSA,
// or WebVTT converted to SRT. Anything else, an empty document, one over
// 16 MiB or 100,000 events, a WebVTT timestamp past 999:59:59.999, or one in
// an encoding whose timestamps cannot be found without decoding
// (ISO-2022-JP), is ErrFormatUnsupported. The error text names what was
// found where the bytes say: an archive or VobSub container, MicroDVD,
// binary data. The encoding is detected with no language hint.
func ParseCues(b []byte) (Cues, error) {
	if err := checkSize(b, maxDocumentBytes); err != nil {
		return Cues{}, err
	}
	b = bytes.Clone(b) // Cues outlives the caller's buffer
	return parse(b, subtitleenc.Detect(b, language.Tag{}))
}

func checkSize(b []byte, limit int) error {
	if len(b) == 0 {
		return fmt.Errorf("%w: empty document", ErrFormatUnsupported)
	}
	if len(b) > limit {
		return fmt.Errorf("%w: %d bytes, over %d", ErrFormatUnsupported, len(b), limit)
	}
	return nil
}

// parse checks no size: its callers bound the bytes (ParseCues and
// decodeCues check them, and parseVTT's conversion at most doubles them,
// see maxParsedBytes).
func parse(b []byte, enc subtitleenc.Encoding) (Cues, error) {
	if name := container(b); name != "" {
		return Cues{}, fmt.Errorf("%w: %s", ErrFormatUnsupported, name)
	}
	v, err := newView(b, enc)
	if err != nil {
		return Cues{}, err
	}
	var c Cues
	switch {
	case v.isWebVTT():
		return parseVTT(b, v, enc)
	case v.isASSFamily():
		c, err = parseASS(b, v, enc)
	default:
		c, err = parseSRT(b, v, enc)
	}
	if err != nil {
		return Cues{}, err
	}
	if len(c.events) == 0 {
		return Cues{}, fmt.Errorf("%w: %s", ErrFormatUnsupported, v.untimed())
	}
	return c, nil
}

// view is the document with every timestamp findable byte by byte: the
// document itself for an ASCII-transparent encoding, and for UTF-16 one byte
// per 2-byte unit, 0x80 standing for any unit outside ASCII. A byte order
// mark is not part of the view.
type view struct {
	doc  []byte
	b    []byte
	base int
	unit int
}

// off maps a view offset to a document offset.
func (v view) off(i int) int { return v.base + i*v.unit }

func (v view) span(from, to int) span { return span{v.off(from), v.off(to)} }

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

func newView(b []byte, enc subtitleenc.Encoding) (view, error) {
	switch enc {
	case subtitleenc.UTF16LE, subtitleenc.UTF16BE:
		return utf16View(b, enc == subtitleenc.UTF16LE)
	}
	if !enc.ASCIITransparent() {
		return view{}, fmt.Errorf("%w: %v is not ASCII-transparent", ErrFormatUnsupported, enc)
	}
	base := 0
	if bytes.HasPrefix(b, bomUTF8) {
		base = len(bomUTF8)
	}
	return view{doc: b, b: b[base:], base: base, unit: 1}, nil
}

func utf16View(b []byte, little bool) (view, error) {
	bom, base := bomUTF16BE, 0
	if little {
		bom = bomUTF16LE
	}
	if bytes.HasPrefix(b, bom) {
		base = len(bom)
	}
	if (len(b)-base)%2 != 0 {
		return view{}, fmt.Errorf("%w: UTF-16 document of odd length", ErrFormatUnsupported)
	}
	nb := make([]byte, (len(b)-base)/2)
	for i := range nb {
		lo, hi := b[base+2*i], b[base+2*i+1]
		if !little {
			lo, hi = hi, lo
		}
		if hi != 0 || lo >= 0x80 {
			lo = 0x80
		}
		nb[i] = lo
	}
	return view{doc: b, b: nb, base: base, unit: 2}, nil
}

// appendASCII appends s, ASCII only, in enc's code units: a byte each, or a
// UTF-16 unit in enc's byte order.
func appendASCII(b []byte, s string, enc subtitleenc.Encoding) []byte {
	for k := range len(s) {
		switch enc {
		case subtitleenc.UTF16LE:
			b = append(b, s[k], 0)
		case subtitleenc.UTF16BE:
			b = append(b, 0, s[k])
		default:
			b = append(b, s[k])
		}
	}
	return b
}

// line is one line of the view: content [start, end), the next line at next.
// A line ends at \n, \r\n or a lone \r.
type line struct{ start, end, next int }

func (v view) line(i int) line {
	j := i
	for j < len(v.b) && v.b[j] != '\n' && v.b[j] != '\r' {
		j++
	}
	next := j
	if next < len(v.b) {
		if v.b[next] == '\r' && next+1 < len(v.b) && v.b[next+1] == '\n' {
			next++
		}
		next++
	}
	return line{start: i, end: j, next: next}
}

func (v view) content(l line) []byte { return v.b[l.start:l.end] }

// firstLine is the first line holding anything but spaces and tabs.
func (v view) firstLine() []byte {
	for i := 0; i < len(v.b); {
		l := v.line(i)
		if c := trimBlank(v.content(l)); len(c) > 0 {
			return c
		}
		i = l.next
	}
	return nil
}

func (v view) isWebVTT() bool {
	rest, ok := bytes.CutPrefix(v.b, []byte("WEBVTT"))
	return ok && (len(rest) == 0 || strings.IndexByte(" \t\r\n", rest[0]) >= 0)
}

func (v view) isASSFamily() bool { return bytes.EqualFold(v.firstLine(), []byte("[Script Info]")) }

func trimBlank(b []byte) []byte { return bytes.Trim(b, " \t") }

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// digits reads 1 to max decimal digits at b[i:], refusing a longer run.
func digits(b []byte, i, maxN int) (v int64, next int, ok bool) {
	j := i
	for j < len(b) && isDigit(b[j]) {
		if j-i == maxN {
			return 0, i, false
		}
		v = v*10 + int64(b[j]-'0')
		j++
	}
	return v, j, j > i
}

// clock reads H:M:S<sep>F at b[i:]: 1 to 3 hour digits, 1 or 2 minute and
// second digits, and 1 to fracDigits fraction digits counted in fracMs
// units. It returns the time in ms and the end of the fraction.
func clock(b []byte, i int, seps string, fracDigits int, fracMs int64) (ms int64, next int, ok bool) {
	var parts [3]int64
	for k := range parts {
		maxN := 2
		if k == 0 {
			maxN = 3
		}
		if parts[k], i, ok = digits(b, i, maxN); !ok {
			return 0, 0, false
		}
		sep := ":"
		if k == 2 {
			sep = seps
		}
		if i >= len(b) || strings.IndexByte(sep, b[i]) < 0 {
			return 0, 0, false
		}
		i++
	}
	frac, i, ok := digits(b, i, fracDigits)
	if !ok {
		return 0, 0, false
	}
	return ((parts[0]*60+parts[1])*60+parts[2])*1000 + frac*fracMs, i, true
}

func countEvent(n int) error {
	if n >= maxEvents {
		return fmt.Errorf("%w: over %d events", ErrFormatUnsupported, maxEvents)
	}
	return nil
}

// containers are refused by their magic before any parse: a stored zip
// holds its members' timestamps as plain text, which would parse as SRT.
var containers = []struct {
	name  string
	magic string
}{
	{"rar4 archive", "Rar!\x1a\x07\x00"},
	{"rar5 archive", "Rar!\x1a\x07\x01\x00"},
	{"7z archive", "7z\xbc\xaf\x27\x1c"},
	{"gzip archive", "\x1f\x8b"},
	{"xz archive", "\xfd7zXZ\x00"},
	{"bzip2 archive", "BZh"},
	{"zip archive", "PK\x03\x04"},
	{"empty zip archive", "PK\x05\x06"},
	{"VobSub (MPEG program stream)", "\x00\x00\x01\xba"},
}

func container(b []byte) string {
	for _, c := range containers {
		if bytes.HasPrefix(b, []byte(c.magic)) {
			return c.name
		}
	}
	return ""
}

// untimed names what a document with no timed event holds: MicroDVD, whose
// cues are frame numbers, binary data (over a tenth of the first 512 view
// bytes are control bytes other than tab, line breaks and ESC), or neither.
func (v view) untimed() string {
	if isMicroDVD(v.firstLine()) {
		return "MicroDVD"
	}
	head := v.b[:min(len(v.b), 512)]
	n := 0
	for _, c := range head {
		if c < '\t' || (c > '\r' && c < ' ' && c != 0x1b) {
			n++
		}
	}
	if n*10 > len(head) {
		return fmt.Sprintf("binary data (%d of the first %d bytes are not text)", n, len(head))
	}
	return "no timed event"
}

// isMicroDVD reports a line opening with {start}{end}, end possibly empty.
func isMicroDVD(l []byte) bool {
	if len(l) == 0 || l[0] != '{' {
		return false
	}
	_, i, ok := digits(l, 1, 9)
	if !ok || i+1 >= len(l) || l[i] != '}' || l[i+1] != '{' {
		return false
	}
	_, j, _ := digits(l, i+2, 9)
	return j < len(l) && l[j] == '}'
}
