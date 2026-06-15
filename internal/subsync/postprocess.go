package subsync

import (
	"bytes"
	"regexp"
	"strings"
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
		text := result[i].Text

		if opts.StripTags {
			text = stripTags(text)
		}
		// Trim whitespace before HI stripping so speaker labels at
		// line start are matched even when the input has leading spaces.
		// Without this, a second PostProcess pass would trim first and
		// then match the speaker pattern, breaking idempotency.
		if opts.CleanWhitespace {
			text = cleanWhitespace(text)
		}
		if opts.StripHI {
			text = stripHI(text)
		}
		if opts.CleanWhitespace {
			text = cleanWhitespace(text)
		}

		result[i].Text = text
	}

	if opts.RemoveEmpty {
		result = removeEmpty(result)
	}

	return result
}

// PostProcessBytes applies encoding normalization and line ending fixes
// to raw subtitle bytes. Call this before parsing, or on the final output.
func PostProcessBytes(data []byte, opts PostProcessOptions) []byte {
	if opts.NormalizeEncoding {
		data = NormalizeEncoding(data)
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
	text = reMusic.ReplaceAllString(text, "")
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
