package subsync

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/cplieger/subflux/internal/subtitleenc"
)

// PostProcessOptions configures subtitle post-processing.
// All fields default to false (no processing). Enable what you need.
type PostProcessOptions struct {
	// StripHI removes hearing-impaired annotations:
	// [sound effects], (music playing), ♪ lyrics ♪, and speaker labels (JOHN:).
	StripHI bool

	// StripTags removes HTML-like tags: <i>, </i>, <b>, </b>, <u>, </u>, <font ...>, </font>.
	StripTags bool

	// NormalizeEncoding converts the subtitle to UTF-8 from any detected encoding
	// (UTF-16 LE/BE, Windows-1252, ISO-8859-1). Also strips UTF-8 BOM.
	NormalizeEncoding bool

	// NormalizeLineEndings converts all line endings to CRLF (SRT standard)
	// and ensures a single trailing CRLF.
	NormalizeLineEndings bool

	// CleanWhitespace trims leading/trailing whitespace from each text line
	// and removes blank lines and bare dialogue dashes.
	CleanWhitespace bool

	// RemoveEmpty drops cues that have no text content after all other
	// processing steps. Cue numbers are assigned when writing SRT output.
	RemoveEmpty bool
}

// PostProcess applies the configured processing steps to subtitle cues.
// Steps run in order: encoding normalization (on raw bytes before parsing)
// is handled by the caller; this function operates on parsed cues.
func PostProcess(cues []Cue, opts PostProcessOptions) []Cue {
	if len(cues) == 0 {
		return cues
	}

	result := make([]Cue, len(cues))
	copy(result, cues)

	for i := range result {
		result[i].Text = cleanCueText(result[i].Text, opts)
	}

	if opts.RemoveEmpty {
		result = removeEmpty(result)
	}

	return result
}

// cleanCueText runs the per-cue text steps to a fixed point.
//
// The steps feed each other in BOTH directions, so no single ordered pass can
// be idempotent: cleanWhitespace's trim can expose text that stripHI's
// line-anchored rules then match, and stripHI's deletions can expose leading
// whitespace for cleanWhitespace to trim. Two fuzz crashers came from that
// second-order reveal, both rooted in the same mismatch — Go's regexp \s is
// only [\t\n\f\r ] while strings.TrimSpace follows unicode.IsSpace, so a byte
// like \v (U+000B) is invisible to every pattern here and still trimmed:
//
//	"A0:\vA0:" -> stripHI drops the leading label -> "\vA0:" -> trim -> "A0:",
//	              itself a speaker label the NEXT call would have stripped.
//	"♪♪\v♪"    -> stripHI drops the "♪♪" span     -> "\v♪"   -> trim -> "♪",
//	              itself a music-only line the NEXT call would have dropped.
//
// Looping to a fixed point makes idempotency structural rather than resting on
// every pattern's whitespace class agreeing with unicode.IsSpace. It mirrors
// how stripHI and stripTags already absorb their own splice-reveals-more
// problem internally, and it terminates because every step only ever deletes:
// each iteration either shrinks the text or leaves it identical and stops.
func cleanCueText(text string, opts PostProcessOptions) string {
	for {
		out := text
		if opts.StripTags {
			out = stripTags(out)
		}
		// Trim ahead of HI stripping so a speaker label at line start is
		// matched even when the input indents it. The loop would converge on
		// the same text without this, just a wasted iteration later.
		if opts.CleanWhitespace {
			out = cleanWhitespace(out)
		}
		if opts.StripHI {
			out = stripHI(out)
		}
		if opts.CleanWhitespace {
			out = cleanWhitespace(out)
		}
		if out == text {
			return out
		}
		text = out
	}
}

// PostProcessBytes applies encoding normalization and line ending fixes
// to raw subtitle bytes. Call this before parsing, or on the final output.
func PostProcessBytes(data []byte, opts PostProcessOptions) []byte {
	if opts.NormalizeEncoding {
		data = subtitleenc.Normalize(data)
	}
	if opts.NormalizeLineEndings {
		data = normalizeLineEndings(data)
	}
	return data
}

// --- HI tag removal ---

// Patterns for hearing-impaired annotations.
var (
	// [sound effect], [GUNSHOT], [door closes]
	reBrackets = regexp.MustCompile(`\[.*?\]`)

	// (music playing), (laughing), (sighs)
	reParens = regexp.MustCompile(`\(.*?\)`)

	// ♪ lyrics ♪, ♫ music ♫
	reMusic = regexp.MustCompile(`[♪♫].*?[♪♫]`)

	// Standalone music notes on a line (♪, ♫, or sequences like ♪♪♪)
	reMusicOnly = regexp.MustCompile(`^[♪♫\s]+$`)

	// SPEAKER: or SPEAKER (V.O.): at the start of a line
	// Matches: "JOHN:", "MAN 1:", "DR. SMITH:", "NARRATOR (V.O.):"
	// Requires at least 2 characters before the colon to avoid matching
	// single-letter patterns like "A:" inside words (idempotency).
	reSpeaker = regexp.MustCompile(`(?m)^[A-Z][A-Z0-9 .'-]+(?:\s*\([A-Z.]+\))?\s*:\s*`)

	// "- " prefix on lines (dialogue dash), only removed if the line
	// becomes empty after other stripping (handled in cleanWhitespace)
)

// stripHI removes hearing-impaired annotations from subtitle text.
func stripHI(text string) string {
	text = reBrackets.ReplaceAllString(text, "")
	text = reParens.ReplaceAllString(text, "")
	// Strip music spans to a fixed point. Removing a "♪...♪" span can splice
	// the surrounding bytes into a NEW span: an incomplete leading "\xe2\x99"
	// and a trailing "\xaa" become adjacent and form another ♪ (E2 99 AA), so
	// a single ReplaceAll pass leaves a residual span that a second
	// PostProcess call would strip, breaking idempotency. Loop until stable.
	for {
		out := reMusic.ReplaceAllString(text, "")
		if out == text {
			break
		}
		text = out
	}
	// Strip stacked speaker labels to a fixed point. The `(?m)^` anchor
	// only matches one label per line-start per ReplaceAll pass, so an
	// input like "JOHN: MARY: line" would drop one label per PostProcess
	// call and never stabilize. Looping until no change makes stripHI
	// idempotent regardless of how many labels are stacked.
	for {
		out := reSpeaker.ReplaceAllString(text, "")
		if out == text {
			break
		}
		text = out
	}

	// Process line by line to remove music-only lines.
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if reMusicOnly.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// --- Tag removal ---

// reHTMLTag matches common subtitle HTML tags.
var reHTMLTag = regexp.MustCompile(`</?(?:i|b|u|font)[^>]*>`)

// stripTags removes HTML-like formatting tags from subtitle text. It
// iterates to a fixed point because removing an inner tag can splice
// surrounding fragments into a new tag (e.g. "</<b0>b>" → "</b>" after the
// first pass), which a single ReplaceAll would leave behind.
func stripTags(text string) string {
	for {
		out := reHTMLTag.ReplaceAllString(text, "")
		if out == text {
			return out
		}
		text = out
	}
}

// --- Whitespace cleaning ---

// cleanWhitespace trims each line and removes lines that are empty
// or contain only a dialogue dash.
func cleanWhitespace(text string) string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		// Remove lines that are just a dialogue dash.
		if line == "-" {
			continue
		}
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// --- Empty cue removal ---

// removeEmpty filters out cues with no text content.
func removeEmpty(cues []Cue) []Cue {
	var result []Cue
	for _, c := range cues {
		if strings.TrimSpace(c.Text) != "" {
			result = append(result, c)
		}
	}
	return result
}

// --- Line ending normalization ---

// normalizeLineEndings converts all line endings to CRLF and ensures
// a single trailing CRLF.
func normalizeLineEndings(data []byte) []byte {
	// Normalize to LF first, then to CRLF.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	data = bytes.TrimSpace(data)
	if len(data) > 0 {
		data = append(data, '\r', '\n')
	}
	return data
}
