// dialogue.go provides dialogue cue filtering: identifies which subtitle cues are
// actual dialogue vs non-dialogue (signs, karaoke, SDH, credits).

package subsync

import (
	"regexp"
	"strings"
)

// reSubTags matches HTML tags and ASS override tags in subtitle text.
var reSubTags = regexp.MustCompile(`</?[a-zA-Z][^>]*>|\{\\[^}]*\}`)

// reMusicLine matches lines that are music/lyrics indicators:
// * prefix (SDH convention), or standalone music notes.
var reMusicLine = regexp.MustCompile(`(?m)^\*.*$`)

// reASSNonDialogue matches ASS override patterns that indicate non-dialogue
// content: non-center-bottom alignment (\an1, \an3-9), drawing mode (\p1+),
// karaoke timing (\k/\K/\kf/\ko), and ASS inline comments with overrides.
// \an2 (bottom-center) is the default dialogue position; any other explicit
// \an override indicates signs, lyrics, titles, or typesetting.
var reASSNonDialogue = regexp.MustCompile(
	`\{[^}]*\\(?:` +
		`an[13-9]` + // non-default alignment (signs, lyrics, titles)
		`|p[1-9]` + // drawing mode
		`|[kK][fFoO]?\d` + // karaoke timing (\k, \K, \kf, \ko variants)
		`)` +
		`|\{\*\\`) // ASS inline comment with override ({*\frz...} = sign typesetting)

// reASSDrawing matches ASS vector drawing commands that appear as raw text
// after ffmpeg ASS→SRT conversion. These are shapes, not text.
var reASSDrawing = regexp.MustCompile(`(?:^|\s)m\s+-?\d+[\s.]-?\d+\s+[lbsnpc]\s`)

// reSoundEffect matches bare single-word sound effects on their own line
// (common in SDH subtitles without brackets).
var reSoundEffect = regexp.MustCompile(`(?mi)^(?:clang|crash|thud|slam|bang|boom|vroom|muah|whoosh|splash|crunch|screech|clatter|rumble|sizzle|hiss|buzz|beep|honk|thump|pop|snap|click|clap|knock|ring|ding|swoosh|whack|smack|crack|rip|zip|drip|gulp|burp|hiccup|cough|sneeze|gasp|sigh|groan|moan|scream|shriek|squeal|giggle|chuckle|sob|whimper|growl|roar|bark|meow|chirp|hoot)s?$`)

// reFansubCredit matches fansub credit/metadata lines in curly braces
// that aren't ASS override tags (no backslash after opening brace).
var reFansubCredit = regexp.MustCompile(`^\{[^\\]`)

// reFontFace extracts the font face name from subtitle text.
var reFontFace = regexp.MustCompile(`font face="([^"]+)"`)

// cleanTextLen returns the byte length of subtitle text after stripping
// HTML/ASS tags, hearing-impaired annotations, and music/lyrics lines.
// Returns 0 for non-dialogue ASS cues (signs, lyrics, drawings, karaoke).
// Note: fansub credits ({Fansub Group}) are not checked here because
// they have non-zero text length after stripping. They are filtered
// separately in filterDialogueCues via reFansubCredit.
func cleanTextLen(text string) int {
	if reASSNonDialogue.MatchString(text) {
		return 0
	}
	if reASSDrawing.MatchString(text) {
		return 0
	}
	text = reSubTags.ReplaceAllString(text, "")
	text = reMusicLine.ReplaceAllString(text, "")
	text = stripHI(text)
	// Strip any remaining music notes (handles multi-line ♪...♪ cues).
	text = strings.ReplaceAll(text, "♪", "")
	text = strings.ReplaceAll(text, "♫", "")
	// Strip bare sound-effect-only lines (consistent with music line handling).
	text = reSoundEffect.ReplaceAllString(text, "")
	trimmed := strings.TrimSpace(text)
	return len(trimmed)
}

// isNonDialogueCue returns true if the cue text is non-dialogue based on
// ASS tags, drawing commands, fansub credits, or other markers.
func isNonDialogueCue(text string) bool {
	if reASSNonDialogue.MatchString(text) || reASSDrawing.MatchString(text) {
		return true
	}
	if reFansubCredit.MatchString(strings.TrimSpace(text)) {
		return true
	}
	return false
}

// fontStats tracks per-font cue count and total screen time in
// milliseconds for dialogue font detection.
type fontStats struct {
	count   int
	totalMs int64
}

// detectDialogueFont finds the dialogue font by analyzing the middle
// 80% of cues (skipping first/last 10% to exclude OP/ED). Picks the
// font with the most total screen time among cues with reasonable
// per-cue duration (0.5-15s). Returns empty string if no clear winner.
func detectDialogueFont(cues []Cue) string {
	if len(cues) < 20 {
		return ""
	}
	// Analyze middle 80% only.
	start := len(cues) / 10
	end := len(cues) - len(cues)/10
	middle := cues[start:end]

	stats := make(map[string]*fontStats)
	for _, c := range middle {
		m := reFontFace.FindStringSubmatch(c.Text)
		if m == nil {
			continue
		}
		font := m[1]
		s, ok := stats[font]
		if !ok {
			s = &fontStats{}
			stats[font] = s
		}
		s.count++
		dur := c.End.Milliseconds() - c.Start.Milliseconds()
		if dur > 0 {
			s.totalMs += dur
		}
	}
	if len(stats) < 2 {
		return ""
	}
	return bestDialogueFont(stats, len(middle))
}

// bestDialogueFont selects the font with the most total screen time
// from the stats map, filtering out fonts with too few cues, extreme
// average duration, or low frequency relative to totalMiddle.
func bestDialogueFont(stats map[string]*fontStats, totalMiddle int) string {
	var bestFont string
	var bestTotal int64
	for font, s := range stats {
		if s.count < 5 {
			continue
		}
		avgMs := s.totalMs / int64(s.count)
		if avgMs < 500 || avgMs > 15000 {
			continue
		}
		// Require at least 10% of middle cues to avoid picking a
		// typesetting font that happens to have some longer cues.
		if float64(s.count)/float64(totalMiddle) < 0.10 {
			continue
		}
		if s.totalMs > bestTotal || (s.totalMs == bestTotal && font < bestFont) {
			bestTotal = s.totalMs
			bestFont = font
		}
	}
	return bestFont
}

// isForeignFont returns true if the cue uses a font different from the
// detected dialogue font. Only applies when a dialogue font is detected.
func isForeignFont(text, dialogueFont string) bool {
	if dialogueFont == "" {
		return false
	}
	m := reFontFace.FindStringSubmatch(text)
	if m == nil {
		return false // no font tag, can't determine
	}
	return m[1] != dialogueFont
}

// markKaraokePairs detects cues that share exact timestamps with
// non-dialogue cues (e.g. English translation lines paired with romaji
// karaoke lines). Returns a set of cue indices that should be treated
// as non-dialogue even though they lack ASS tags.
func markKaraokePairs(cues []Cue) map[int]bool {
	// Build a set of time ranges that are non-dialogue.
	type timeKey struct{ startMs, endMs int64 }
	nonDialogueTimes := make(map[timeKey]bool)
	nonDialogueIdx := make(map[int]bool)
	for i, c := range cues {
		if isNonDialogueCue(c.Text) {
			k := timeKey{c.Start.Milliseconds(), c.End.Milliseconds()}
			nonDialogueTimes[k] = true
			nonDialogueIdx[i] = true
		}
	}

	// Mark cues that share exact timestamps with non-dialogue cues.
	paired := make(map[int]bool)
	for i, c := range cues {
		if nonDialogueIdx[i] {
			continue // already non-dialogue
		}
		k := timeKey{c.Start.Milliseconds(), c.End.Milliseconds()}
		if nonDialogueTimes[k] {
			paired[i] = true
		}
	}
	return paired
}

// filterDialogueCues returns only cues that are dialogue (non-zero
// cleanTextLen, not karaoke pairs, not foreign-font typesetting,
// and not fansub credits).
func filterDialogueCues(cues []Cue, karaokePairs map[int]bool) []Cue {
	// Font detection runs on the full cue list intentionally: the 10%
	// frequency threshold and 0.5-15s duration filter in detectDialogueFont
	// already exclude typesetting fonts, and pre-filtering would require
	// a second pass over the cues.
	dialogueFont := detectDialogueFont(cues)
	var out []Cue
	for i, c := range cues {
		if karaokePairs[i] {
			continue
		}
		if isForeignFont(c.Text, dialogueFont) {
			continue
		}
		if reFansubCredit.MatchString(strings.TrimSpace(c.Text)) {
			continue
		}
		if cleanTextLen(c.Text) > 0 {
			out = append(out, c)
		}
	}
	return out
}
