package timing

import (
	"bytes"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleenc"
)

// CleanOptions is the post_processing section's content; mediawrite owns
// the section and passes these.
type CleanOptions struct {
	StripHI, StripTags, CleanWhitespace, RemoveEmpty bool
	NormalizeUTF8, NormalizeLineEndings              bool
}

// Clean applies opts to c once, at delivery, in time linear in the document,
// and returns the result parsed under the encoding Detect names with tag as
// the hint. The text options edit event text by textRules, outside ASS and
// SSA {...} blocks, as deletions of the original bytes; RemoveEmpty removes
// an event left with no text and renumbers SRT indexes. Other bytes change
// only by NormalizeUTF8 (an Unknown encoding keeps its bytes) and
// NormalizeLineEndings (CRLF). A result ParseCues refuses, and the zero
// Cues, is ErrFormatUnsupported.
func Clean(c Cues, opts CleanOptions, tag language.Tag) (Cues, error) {
	if c.format == 0 {
		return Cues{}, fmt.Errorf("%w: no document", ErrFormatUnsupported)
	}
	if enc := subtitleenc.Detect(c.doc, tag); enc != c.enc {
		var err error
		if c, err = parse(c.doc, enc); err != nil {
			return Cues{}, err
		}
	}
	doc, enc := c.edit(opts), c.enc
	if opts.NormalizeUTF8 {
		doc, enc = toUTF8(doc, enc)
	}
	if opts.NormalizeLineEndings {
		doc = crlf(doc, enc)
	}
	if err := checkSize(doc, maxDocumentBytes); err != nil {
		return Cues{}, err
	}
	return parse(doc, enc)
}

// cleaned is one event's text after the text rules: the source ranges kept,
// and whether a rune that is not blank survives.
type cleaned struct {
	kept []span
	text bool
}

func (c Cues) edit(opts CleanOptions) []byte {
	rules := textRules{hi: opts.StripHI, tags: opts.StripTags, blanks: opts.CleanWhitespace}
	if rules == (textRules{}) && !opts.RemoveEmpty {
		return c.doc
	}
	var s lineScan
	events := make([]cleaned, len(c.events))
	renumber := false
	for i, e := range c.events {
		events[i] = c.cleanText(e.text, rules, &s)
		renumber = renumber || opts.RemoveEmpty && !events[i].text
	}
	out := make([]byte, 0, len(c.doc))
	pos, n := 0, 0
	for i, e := range c.events {
		if renumber && !events[i].text {
			out = append(out, c.doc[pos:e.block.from]...)
			pos = e.block.to
			continue
		}
		n++
		if renumber && e.index.to > e.index.from {
			out = append(out, c.doc[pos:e.index.from]...)
			out = appendASCII(out, strconv.Itoa(n), c.enc)
			pos = e.index.to
		}
		out = append(out, c.doc[pos:e.text.from]...)
		for _, k := range events[i].kept {
			out = append(out, c.doc[k.from:k.to]...)
		}
		pos = e.text.to
	}
	return append(out, c.doc[pos:]...)
}

// cleanText runs the text rules over the lines of the event text at t: SRT
// lines end at CR, LF or CRLF, ASS and SSA lines at \N. In ASS and SSA a
// backslash and the rune after it read as one '\\', so no deletion joins a
// '\\' to an 'N' into a line break, and a {...} block is skipped whole. A
// dropped line takes the line break before it, or after it when it is the
// first kept; its blocks stay.
func (c Cues) cleanText(t span, rules textRules, s *lineScan) cleaned {
	var out cleaned
	var blocks []span
	ass := c.format != SRT
	sep, lines, noClose := span{}, 0, false
	s.reset(rules)
	endLine := func() {
		keepLine, text := s.finish()
		if keepLine && lines > 0 {
			out.kept = append(out.kept, sep)
		}
		if keepLine {
			lines++
			out.text = out.text || text
		}
		out.kept = mergeKept(out.kept, s.runs, blocks, keepLine)
		blocks = blocks[:0]
		s.reset(rules)
	}
	for i := t.from; i < t.to; {
		r, n := runeAt(c.doc, c.enc, i, t.to)
		switch k := c.lineBreak(i, r, n, t.to); {
		case k > 0:
			endLine()
			sep = span{i, i + k}
			n = k
		case ass && r == '\\' && i+n < t.to:
			_, k := runeAt(c.doc, c.enc, i+n, t.to)
			n += k
			s.feed(r, i, n)
		case ass && r == '{' && !noClose:
			end, ok := c.blockEnd(i+n, t.to)
			if ok {
				blocks = append(blocks, span{i, end})
				n = end - i
				break
			}
			noClose = true
			s.feed(r, i, n)
		default:
			s.feed(r, i, n)
		}
		i += n
	}
	endLine()
	return out
}

func (c Cues) lineBreak(i int, r rune, n, end int) int {
	switch {
	case c.format == SRT && r == '\n':
		return n
	case c.format == SRT && r == '\r':
		if next, k := runeAt(c.doc, c.enc, i+n, end); next == '\n' {
			return n + k
		}
		return n
	case c.format != SRT && r == '\\':
		if next, k := runeAt(c.doc, c.enc, i+n, end); next == 'N' {
			return n + k
		}
	}
	return 0
}

// blockEnd is the end of the first '}' at or after i; false when none
// follows, so no later '{' of the text opens a block either.
func (c Cues) blockEnd(i, end int) (int, bool) {
	for i < end {
		r, n := runeAt(c.doc, c.enc, i, end)
		i += n
		if r == '}' {
			return i, true
		}
	}
	return 0, false
}

// mergeKept appends a line's kept runs (when the line is kept) and its
// blocks to kept in source order.
func mergeKept(kept []span, runs []kept, blocks []span, keepLine bool) []span {
	if !keepLine {
		runs = nil
	}
	for len(runs) > 0 || len(blocks) > 0 {
		if len(blocks) == 0 || len(runs) > 0 && runs[0].from < blocks[0].from {
			kept = append(kept, span{runs[0].from, runs[0].to})
			runs = runs[1:]
		} else {
			kept = append(kept, blocks[0])
			blocks = blocks[1:]
		}
	}
	return kept
}

// runeAt decodes the rune at doc[i:end] in enc and its length in bytes; a
// byte nothing decodes reads as U+FFFD of length 1. Below 0x80 a byte at a
// character boundary is its ASCII rune in every encoding but UTF-16.
func runeAt(doc []byte, enc subtitleenc.Encoding, i, end int) (r rune, n int) {
	b := doc[i:end]
	if len(b) == 0 {
		return utf8.RuneError, 0
	}
	switch enc {
	case subtitleenc.UTF16LE, subtitleenc.UTF16BE:
		return utf16RuneAt(b, enc == subtitleenc.UTF16LE)
	}
	if b[0] < utf8.RuneSelf {
		return rune(b[0]), 1
	}
	if enc == subtitleenc.UTF8 {
		return utf8.DecodeRune(b)
	}
	for n = 1; n <= min(utf8.UTFMax, len(b)); n++ {
		d, err := enc.Decode(b[:n])
		if r, k := utf8.DecodeRune(d); err == nil && k == len(d) && r != utf8.RuneError {
			return r, n
		}
	}
	return utf8.RuneError, 1
}

// utf16RuneAt reads one UTF-16 unit; a surrogate reads as U+FFFD, which no
// rule matches, so a pair is never split.
func utf16RuneAt(b []byte, little bool) (r rune, n int) {
	if len(b) < 2 {
		return utf8.RuneError, len(b)
	}
	if little {
		r = rune(b[0]) | rune(b[1])<<8
	} else {
		r = rune(b[0])<<8 | rune(b[1])
	}
	if 0xD800 <= r && r <= 0xDFFF {
		return utf8.RuneError, 2
	}
	return r, 2
}

// toUTF8 re-encodes doc to UTF-8 without a byte order mark; bytes enc cannot
// decode (Unknown) are kept as they are.
func toUTF8(doc []byte, enc subtitleenc.Encoding) ([]byte, subtitleenc.Encoding) {
	body := doc
	if bom := bomOf(enc); bom != nil {
		body, _ = bytes.CutPrefix(doc, bom)
	}
	if enc == subtitleenc.UTF8 {
		return bytes.Clone(body), enc
	}
	d, err := enc.Decode(body)
	if err != nil {
		return doc, enc
	}
	return d, subtitleenc.UTF8
}

func bomOf(enc subtitleenc.Encoding) []byte {
	switch enc {
	case subtitleenc.UTF8:
		return bomUTF8
	case subtitleenc.UTF16LE:
		return bomUTF16LE
	case subtitleenc.UTF16BE:
		return bomUTF16BE
	}
	return nil
}

// crlf writes every line break of doc, and a missing last one, as CRLF in
// enc's code units.
func crlf(doc []byte, enc subtitleenc.Encoding) []byte {
	out := make([]byte, 0, len(doc)+len(doc)/16)
	ended := true
	for i := 0; i < len(doc); {
		r, n := runeAt(doc, enc, i, len(doc))
		switch r {
		case '\r':
			if next, k := runeAt(doc, enc, i+n, len(doc)); next == '\n' {
				n += k
			}
			fallthrough
		case '\n':
			out = appendASCII(out, "\r\n", enc)
			ended = true
		default:
			out = append(out, doc[i:i+n]...)
			ended = false
		}
		i += n
	}
	if !ended {
		out = appendASCII(out, "\r\n", enc)
	}
	return out
}
