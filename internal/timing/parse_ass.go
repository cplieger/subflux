package timing

import (
	"bytes"
	"cmp"
	"strings"

	"github.com/cplieger/subflux/internal/subtitleenc"
)

// assFields is where Start, End, Style and Text sit in a Dialogue line, read
// from the [Events] Format: line; Text is always the last field.
type assFields struct {
	n, start, end, style int
}

// defaultASSFields is the Format line both v4.00 and v4.00+ define when a
// document omits it (Marked or Layer first, Text tenth).
var defaultASSFields = assFields{n: 10, start: 1, end: 2, style: 3}

// parseASS reads ASS and SSA as libass does (libass/ass.c): ScriptType
// decides the format, else the styles section header, else ASS; in
// [Events] the Format: line names the fields, a Dialogue: line is an event,
// and every other line, Comment: included, is kept as bytes and never timed.
func parseASS(doc []byte, v view, enc subtitleenc.Encoding) (Cues, error) {
	p := assParser{c: Cues{doc: doc, enc: enc}, v: v, fields: defaultASSFields}
	for i := 0; i < len(v.b); {
		l := v.line(i)
		i = l.next
		if err := p.line(l); err != nil {
			return Cues{}, err
		}
	}
	p.c.format = cmp.Or(p.scriptType, p.stylesHeader, ASS)
	return p.c, nil
}

type assParser struct {
	section                  string
	c                        Cues
	v                        view
	fields                   assFields
	scriptType, stylesHeader Format
}

func (p *assParser) line(l line) error {
	content := p.v.content(l)
	if trimmed := trimBlank(content); len(trimmed) > 1 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' {
		p.section = strings.ToLower(string(trimmed))
		switch p.section {
		case "[v4+ styles]":
			p.stylesHeader = ASS
		case "[v4 styles]":
			p.stylesHeader = SSA
		}
		return nil
	}
	switch p.section {
	case "[script info]":
		if val, ok := cutKey(content, "ScriptType:"); ok {
			p.scriptType = scriptTypes[strings.ToLower(string(val))]
		}
	case "[events]":
		return p.eventLine(l, content)
	}
	return nil
}

var scriptTypes = map[string]Format{"v4.00+": ASS, "v4.00": SSA}

func (p *assParser) eventLine(l line, content []byte) error {
	if val, ok := cutKey(content, "Format:"); ok {
		if f, ok := readASSFormat(val); ok {
			p.fields = f
		}
		return nil
	}
	e, style, ok := assDialogue(p.v, l, p.fields)
	if !ok {
		return nil
	}
	if err := countEvent(len(p.c.events)); err != nil {
		return err
	}
	e.style = p.c.decode(p.c.doc[style.from:style.to])
	p.c.events = append(p.c.events, e)
	return nil
}

// cutKey returns the trimmed value of a "Key: value" line whose key matches
// exactly, as libass compares keys.
func cutKey(content []byte, key string) ([]byte, bool) {
	val, ok := bytes.CutPrefix(content, []byte(key))
	return trimBlank(val), ok
}

// readASSFormat reads a Format: line's field names. A line naming no Start,
// End or Text, or Text not last, is ignored, as libass falls back to the
// default order.
func readASSFormat(val []byte) (assFields, bool) {
	names := bytes.Split(val, []byte(","))
	f := assFields{n: len(names), start: -1, end: -1, style: -1}
	for k, name := range names {
		switch strings.ToLower(string(trimBlank(name))) {
		case "start":
			f.start = k
		case "end":
			f.end = k
		case "style":
			f.style = k
		case "text":
			if k != len(names)-1 {
				return assFields{}, false
			}
		}
	}
	text := strings.ToLower(string(trimBlank(names[len(names)-1])))
	if f.start < 0 || f.end < 0 || text != "text" {
		return assFields{}, false
	}
	return f, true
}

// assDialogue reads one Dialogue: line under fields and returns its cue and
// the document span of its style; a line whose times do not read is not an
// event.
func assDialogue(v view, l line, fields assFields) (cue, span, bool) {
	content := v.content(l)
	rest, ok := bytes.CutPrefix(content, []byte("Dialogue:"))
	if !ok {
		return cue{}, span{}, false
	}
	at := l.start + len(content) - len(rest)
	// Only the fields read are kept, so the work is linear in the line
	// whatever field count a Format: line declares.
	var startF, endF, styleF span
	for k := range fields.n - 1 {
		comma := bytes.IndexByte(v.b[at:l.end], ',')
		if comma < 0 {
			return cue{}, span{}, false
		}
		f := span{at, at + comma}
		switch k {
		case fields.start:
			startF = f
		case fields.end:
			endF = f
		case fields.style:
			styleF = f
		}
		at += comma + 1
	}
	startMs, ok := assTime(v, startF)
	if !ok {
		return cue{}, span{}, false
	}
	endMs, ok := assTime(v, endF)
	if !ok {
		return cue{}, span{}, false
	}
	var style span
	if fields.style >= 0 {
		lo, hi := trimmedBounds(v.b[styleF.from:styleF.to])
		style = v.span(styleF.from+lo, styleF.from+hi)
	}
	return cue{
		startMs: startMs, endMs: endMs,
		text: v.span(at, l.end),
	}, style, true
}

// assTime reads one H:MM:SS.cc field, blanks around it allowed; the fraction
// counts centiseconds, as libass reads it.
func assTime(v view, field span) (int64, bool) {
	b := v.b[field.from:field.to]
	lo, hi := trimmedBounds(b)
	if !isASCII(b[lo:hi]) {
		return 0, false
	}
	ms, next, ok := clock(b[:hi], lo, ".", 2, 10)
	if !ok || next != hi {
		return 0, false
	}
	return ms, true
}
