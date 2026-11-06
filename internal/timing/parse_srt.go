package timing

import (
	"bytes"

	"github.com/cplieger/subflux/internal/subtitleenc"
)

// parseSRT reads blocks the way ffmpeg's SubRip demuxer does
// (libavformat/srtdec.c): every timing line starts an event, a lone number
// on the last non-blank line before it is that event's index, and an
// event's text runs to the next event's block. Lines before the first
// timing line are kept as bytes.
func parseSRT(doc []byte, v view, enc subtitleenc.Encoding) (Cues, error) {
	p := srtParser{c: Cues{doc: doc, format: SRT, enc: enc}, v: v}
	for i := 0; i < len(v.b); {
		l := v.line(i)
		i = l.next
		content := v.content(l)
		if t, ok := srtTiming(content); ok {
			if err := p.timing(l, t); err != nil {
				return Cues{}, err
			}
		} else if len(trimBlank(content)) > 0 {
			p.textLine(l, content)
		}
	}
	p.closeOpen()
	return p.c, nil
}

type srtParser struct {
	c        Cues
	v        view
	index    line // a lone-number line not yet known to be text
	textEnd  int  // view offset where the open event's text ends
	hasIndex bool
}

func (p *srtParser) timing(l line, t srtStamps) error {
	if err := countEvent(len(p.c.events)); err != nil {
		return err
	}
	// A lone number before the timing line is this event's index, not the
	// open event's text.
	p.hasIndex = false
	p.closeOpen()
	p.c.events = append(p.c.events, cue{
		startMs: t.startMs, endMs: t.endMs,
		text: span{from: p.v.off(l.next)},
	})
	p.textEnd = l.next
	return nil
}

// textLine takes a non-blank line that is not a timing line: the open
// event's text, or a lone number that may be the next event's index.
func (p *srtParser) textLine(l line, content []byte) {
	open := len(p.c.events) > 0
	if open && p.hasIndex {
		p.textEnd = p.index.end
	}
	p.hasIndex = isLoneNumber(content)
	if p.hasIndex {
		p.index = l
	} else if open {
		p.textEnd = l.end
	}
}

// closeOpen ends the open event's text, if any. A pending lone number here is
// the last text line: timing clears it first.
func (p *srtParser) closeOpen() {
	if len(p.c.events) == 0 {
		return
	}
	if p.hasIndex {
		p.textEnd = p.index.end
	}
	p.c.events[len(p.c.events)-1].text.to = p.v.off(p.textEnd)
}

type srtStamps struct{ startMs, endMs int64 }

// srtTiming reads an ASCII timing line, H:MM:SS,mmm --> H:MM:SS,mmm with
// '.' or ',' and 1 to 3 fraction digits, followed by anything that does not
// continue the last number (a position suffix). The fraction is read as an
// integer count of milliseconds, as ffmpeg and VLC read it, so ",5" is 5 ms.
func srtTiming(b []byte) (srtStamps, bool) {
	var t srtStamps
	if !isASCII(b) {
		return t, false
	}
	i := skipBlank(b, 0)
	var ok bool
	if t.startMs, i, ok = clock(b, i, ",.", 3, 1); !ok {
		return t, false
	}
	i = skipBlank(b, i)
	rest, ok := bytes.CutPrefix(b[i:], []byte("-->"))
	if !ok {
		return t, false
	}
	i = skipBlank(b, len(b)-len(rest))
	if t.endMs, _, ok = clock(b, i, ",.", 3, 1); !ok {
		return t, false
	}
	return t, true
}

func skipBlank(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return i
}

func isLoneNumber(b []byte) bool {
	lo, hi := trimmedBounds(b)
	if lo == hi {
		return false
	}
	for _, c := range b[lo:hi] {
		if !isDigit(c) {
			return false
		}
	}
	return true
}

// trimmedBounds is b's content without leading and trailing spaces and tabs.
func trimmedBounds(b []byte) (lo, hi int) {
	lo, hi = 0, len(b)
	for lo < hi && (b[lo] == ' ' || b[lo] == '\t') {
		lo++
	}
	for hi > lo && (b[hi-1] == ' ' || b[hi-1] == '\t') {
		hi--
	}
	return lo, hi
}
