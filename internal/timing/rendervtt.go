package timing

import (
	"fmt"
	"strings"
	"time"
)

// PreviewSpan is the media one preview window covers from its start.
const PreviewSpan = 10 * time.Minute

// RenderVTT renders the events Render would write under t as WebVTT for the
// preview window [from, from+PreviewSpan): the events meeting it, shifted
// by -from and clipped to it, since the preview fragment starts at time 0.
// A player styles SRT's <i>, <b> and <u> and hides other SRT tags, so in SRT
// those six exact tags pass and other tags are removed; ASS override blocks
// are removed, every other '&', '<' and '>' is escaped, and "-->" is broken
// by U+2060. It refuses what Render refuses, and a negative from as
// ErrTimingInvalid.
func RenderVTT(c Cues, t Timing, from time.Duration) ([]byte, error) {
	if from < 0 {
		return nil, fmt.Errorf("%w: preview from %v, before 0", ErrTimingInvalid, from)
	}
	mapped, err := mapEvents(c, t)
	if err != nil {
		return nil, err
	}
	fromMs, spanMs := from.Milliseconds(), PreviewSpan.Milliseconds()
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for i, e := range c.events {
		m := mapped[i]
		if !m.keep || m.endMs <= fromMs || m.startMs >= fromMs+spanMs {
			continue
		}
		fmt.Fprintf(&b, "\n%s --> %s\n", vttStampOf(max(m.startMs-fromMs, 0)), vttStampOf(min(m.endMs-fromMs, spanMs)))
		for line := range strings.SplitSeq(vttMarkup(c.eventText(e.text), c.format != SRT), "\n") {
			// A blank line would end the cue.
			if strings.TrimSpace(line) != "" {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return []byte(b.String()), nil
}

func vttStampOf(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}

// vttMarkup turns one event's decoded text into WebVTT cue text.
func vttMarkup(text string, ass bool) string {
	var b strings.Builder
	// A '{' opens a block only with a '}' after it; testing that against the
	// last '}' keeps the scan linear on a line of unclosed braces.
	lastClose := strings.LastIndexByte(text, '}')
	for i := 0; i < len(text); {
		rest := text[i:]
		n := 1
		switch {
		case strings.HasPrefix(rest, "-->"):
			b.WriteString("--\u2060&gt;")
			n = 3
		case rest[0] == '{' && (ass || strings.HasPrefix(rest, `{\`)) && lastClose > i:
			n = strings.IndexByte(rest, '}') + 1
		case ass && rest[0] == '\\' && len(rest) > 1 && (rest[1] == 'h' || rest[1] == 'n'):
			b.WriteString(assSpaces[rest[1]])
			n = 2
		case rest[0] == '<' && !ass:
			n = writeTag(&b, rest)
		default:
			writeEscaped(&b, rest[0])
		}
		i += n
	}
	return b.String()
}

func writeEscaped(b *strings.Builder, c byte) {
	switch c {
	case '<':
		b.WriteString("&lt;")
	case '>':
		b.WriteString("&gt;")
	case '&':
		b.WriteString("&amp;")
	default:
		b.WriteByte(c)
	}
}

// assSpaces: \h is a hard space; \n breaks only under WrapStyle 2 and is a
// space otherwise (libass), which a preview line reads as.
var assSpaces = map[byte]string{'h': "\u00a0", 'n': " "}

// writeTag writes the tag text starts with as cue text and returns the bytes
// it consumed.
func writeTag(b *strings.Builder, text string) int {
	switch tag, kind := srtTag(text); kind {
	case tagPass:
		b.WriteString(tag)
		return len(tag)
	case tagHidden:
		return len(tag)
	default:
		writeEscaped(b, '<')
		return 1
	}
}

type tagKind uint8

const (
	notTag tagKind = iota
	tagPass
	tagHidden
)

// srtTag reads the tag text starts with: one of the six exact tags a player
// styles, another tag a player hides (i, b, u, s or font in any case, with
// attributes or not, and their closers), or no tag (malformed, or a name no
// player reads, such as <script>), which is text.
func srtTag(text string) (string, tagKind) {
	end := strings.IndexAny(text[1:], "<>")
	if end < 0 || text[1+end] != '>' {
		return "", notTag
	}
	tag := text[:end+2]
	switch tag {
	case "<i>", "</i>", "<b>", "</b>", "<u>", "</u>":
		return tag, tagPass
	}
	name := strings.TrimPrefix(tag[1:len(tag)-1], "/")
	if k := strings.IndexAny(name, " \t"); k >= 0 {
		name = name[:k]
	}
	switch strings.ToLower(name) {
	case "i", "b", "u", "s", "font":
		return tag, tagHidden
	}
	return "", notTag
}
