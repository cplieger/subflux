package timing

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cplieger/subflux/internal/subtitleenc"
)

// parseVTT converts a WebVTT document to SRT once and parses that: Render
// writes SRT, so the stored baseline is the SRT. It drops cue identifiers,
// cue settings, NOTE, STYLE and REGION blocks and every tag but i, b and u
// (their classes dropped). The conversion copies text bytes and writes only
// ASCII in the document's units, so the encoding survives as it does for SRT.
func parseVTT(doc []byte, v view, enc subtitleenc.Encoding) (Cues, error) {
	w := vttWriter{v: v, out: append([]byte(nil), doc[:v.base]...), enc: enc}
	w.nl = "\n"
	if first := v.line(0); first.next-first.end == 2 {
		w.nl = "\r\n"
	}
	i := v.line(0).next
	for i < len(v.b) && len(trimBlank(v.content(v.line(i)))) > 0 { // the header block
		i = v.line(i).next
	}
	for i < len(v.b) {
		l := v.line(i)
		content := v.content(l)
		switch {
		case len(trimBlank(content)) == 0:
			i = l.next
		case isVTTBlockKeyword(content):
			i = skipBlock(v, l.next)
		case bytes.Contains(content, []byte("-->")):
			i = w.cue(l)
		default: // a cue identifier, or a stray line
			next := v.line(l.next)
			if l.next < len(v.b) && bytes.Contains(v.content(next), []byte("-->")) {
				i = w.cue(next)
			} else {
				i = skipBlock(v, l.next)
			}
		}
	}
	if w.err != nil {
		return Cues{}, w.err
	}
	if w.n == 0 {
		return Cues{}, fmt.Errorf("%w: WebVTT with no cue", ErrFormatUnsupported)
	}
	return parse(w.out, enc)
}

func isVTTBlockKeyword(content []byte) bool {
	for _, kw := range [...]string{"NOTE", "STYLE", "REGION"} {
		if rest, ok := bytes.CutPrefix(content, []byte(kw)); ok && (len(rest) == 0 || rest[0] == ' ' || rest[0] == '\t') {
			return true
		}
	}
	return false
}

// skipBlock returns the view offset of the first blank line at or after i.
func skipBlock(v view, i int) int {
	for i < len(v.b) {
		l := v.line(i)
		if len(trimBlank(v.content(l))) == 0 {
			return i
		}
		i = l.next
	}
	return i
}

type vttWriter struct {
	err error
	out []byte
	nl  string
	v   view
	n   int
	enc subtitleenc.Encoding
}

func (w *vttWriter) ascii(s string) { w.out = appendASCII(w.out, s, w.enc) }

// copyText copies view range [from, to) of the document unchanged.
func (w *vttWriter) copyText(from, to int) {
	w.out = append(w.out, w.v.doc[w.v.off(from):w.v.off(to)]...)
}

// cue writes the cue whose timing line is l and returns where the next block
// starts: the first blank line, or a payload line holding "-->", which per
// the WebVTT parser starts the next cue. An unreadable timing line drops the
// block; a stamp past maxSRTHour stops the conversion with w.err set.
func (w *vttWriter) cue(l line) int {
	startMs, endMs, err := vttTiming(w.v.content(l))
	if errors.Is(err, errNoVTTTiming) {
		return skipBlock(w.v, l.next)
	}
	if err != nil {
		w.err = err
		return len(w.v.b)
	}
	w.n++
	w.ascii(fmt.Sprintf("%d%s%s --> %s%s", w.n, w.nl, srtStamp(startMs), srtStamp(endMs), w.nl))
	i := l.next
	for i < len(w.v.b) {
		p := w.v.line(i)
		content := w.v.content(p)
		if len(trimBlank(content)) == 0 || bytes.Contains(content, []byte("-->")) {
			break
		}
		w.payload(p)
		w.ascii(w.nl)
		i = p.next
	}
	w.ascii(w.nl)
	return i
}

// payload writes one cue text line with tags and character references
// rewritten for SRT, which has neither escapes nor classes.
func (w *vttWriter) payload(p line) {
	b := w.v.b
	from := p.start
	for k := p.start; k < p.end; {
		switch b[k] {
		case '<':
			w.copyText(from, k)
			end := bytes.IndexByte(b[k:p.end], '>')
			if end < 0 {
				return // an unterminated tag runs to the line end
			}
			w.tag(b[k+1 : k+end])
			k += end + 1
			from = k
		case '&':
			// The window bounds the work on a line of '&' with no ';'.
			name, _, ok := bytes.Cut(b[k+1:min(p.end, k+2+maxReferenceName)], []byte(";"))
			r, known := vttReferences[string(name)]
			if !ok || !known {
				k++
				continue
			}
			w.copyText(from, k)
			w.reference(r)
			k += len(name) + 2
			from = k
		default:
			k++
		}
	}
	w.copyText(from, p.end)
}

// tag keeps <i>, <b>, <u> and their closers, classes and annotations
// dropped, and drops every other tag (<c>, <v>, <lang>, <ruby>, timestamps).
func (w *vttWriter) tag(body []byte) {
	closer := bytes.HasPrefix(body, []byte("/"))
	name := bytes.TrimPrefix(body, []byte("/"))
	if k := bytes.IndexAny(name, ". \t"); k >= 0 {
		name = name[:k]
	}
	if len(name) != 1 || bytes.IndexByte([]byte("ibu"), name[0]) < 0 {
		return
	}
	if closer {
		w.ascii("</" + string(name) + ">")
	} else {
		w.ascii("<" + string(name) + ">")
	}
}

// vttReferences are WebVTT's named character references; outside UTF-8 a
// non-ASCII one is written as its ASCII stand-in (a space, or nothing for
// the direction marks), since only ASCII is written in the document's units.
var vttReferences = map[string]struct {
	utf8, ascii string
}{
	"amp":  {"&", "&"},
	"lt":   {"<", "<"},
	"gt":   {">", ">"},
	"nbsp": {"\u00a0", " "},
	"lrm":  {"\u200e", ""},
	"rlm":  {"\u200f", ""},
}

// maxReferenceName is the longest name in vttReferences.
const maxReferenceName = 4

func (w *vttWriter) reference(r struct{ utf8, ascii string }) {
	if w.enc == subtitleenc.UTF8 {
		w.out = append(w.out, r.utf8...)
		return
	}
	w.ascii(r.ascii)
}

var (
	errNoVTTTiming = errors.New("no WebVTT timing")
	errVTTPastSRT  = fmt.Errorf("%w: WebVTT timestamp past 999:59:59.999", ErrFormatUnsupported)
)

// maxSRTHour is the last hour srtStamp writes that clock reads back: clock
// reads at most three hour digits.
const maxSRTHour = 999

// vttTiming reads "start --> end" and ignores the cue settings after it.
func vttTiming(b []byte) (startMs, endMs int64, err error) {
	if !isASCII(b) {
		return 0, 0, errNoVTTTiming
	}
	i := skipBlank(b, 0)
	if startMs, i, err = vttStamp(b, i); err != nil {
		return 0, 0, err
	}
	rest, ok := bytes.CutPrefix(b[skipBlank(b, i):], []byte("-->"))
	if !ok {
		return 0, 0, errNoVTTTiming
	}
	i = skipBlank(b, len(b)-len(rest))
	if endMs, i, err = vttStamp(b, i); err != nil {
		return 0, 0, err
	}
	if i < len(b) && b[i] != ' ' && b[i] != '\t' {
		return 0, 0, errNoVTTTiming
	}
	return startMs, endMs, nil
}

// vttStamp reads [H:]MM:SS.mmm by the WebVTT timestamp grammar
// (https://www.w3.org/TR/webvtt1/#collect-a-webvtt-timestamp): hours any
// number of digits, minutes and seconds two digits from 00 to 59, three
// fraction digits.
func vttStamp(b []byte, i int) (ms int64, next int, err error) {
	hms, i, ok := vttClock(b, i)
	if !ok || i >= len(b) || b[i] != '.' {
		return 0, 0, errNoVTTTiming
	}
	frac, i, ok := fixedDigits(b, i+1, 3, 3)
	if !ok || hms[1] > 59 || hms[2] > 59 {
		return 0, 0, errNoVTTTiming
	}
	if hms[0] > maxSRTHour {
		return 0, 0, errVTTPastSRT
	}
	return ((hms[0]*60+hms[1])*60+hms[2])*1000 + frac, i, nil
}

// vttClock reads the hours, minutes and seconds before the fraction; a first
// field that is not two digits, or is over 59, is the hours.
func vttClock(b []byte, i int) (hms [3]int64, next int, ok bool) {
	first, j := hourDigits(b, i)
	if j == i || j >= len(b) || b[j] != ':' {
		return hms, 0, false
	}
	hours := j-i != 2 || first > 59
	second, j, ok := fixedDigits(b, j+1, 2, 2)
	if !ok {
		return hms, 0, false
	}
	if !hours && (j >= len(b) || b[j] != ':') {
		return [3]int64{0, first, second}, j, true
	}
	if j >= len(b) || b[j] != ':' {
		return hms, 0, false
	}
	third, j, ok := fixedDigits(b, j+1, 2, 2)
	return [3]int64{first, second, third}, j, ok
}

// hourDigits reads every digit at b[i:]; the value stops growing once past
// maxSRTHour, so no digit count overflows it.
func hourDigits(b []byte, i int) (v int64, next int) {
	for next = i; next < len(b) && isDigit(b[next]); next++ {
		if v <= maxSRTHour {
			v = v*10 + int64(b[next]-'0')
		}
	}
	return v, next
}

// fixedDigits reads minN to maxN decimal digits at b[i:].
func fixedDigits(b []byte, i, minN, maxN int) (v int64, next int, ok bool) {
	v, next, ok = digits(b, i, maxN)
	return v, next, ok && next-i >= minN
}

// srtStamp writes ms as HH:MM:SS,mmm.
func srtStamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}
