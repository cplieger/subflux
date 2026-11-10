package subsync

import (
	"regexp"
	"strings"

	"github.com/cplieger/subflux/internal/timing"
)

// nonDialogueStylePatterns are the regex fragments of ASS style names that
// carry non-dialogue content, grouped by kind.
var nonDialogueStylePatterns = []string{
	// OP/ED
	`\bop[a-z\d]*\b`,
	`\bed[a-z\d]*\b`,
	`\bopening\b`,
	`\bending\b`,
	// karaoke
	`romaji|romanji|kanji`,
	`karaoke|\bkara\b|lyric`,
	// signs, typesetting, songs
	`sign|\btitle|credit|\bnote\b|\bcaption\b|\blogos?\b`,
	`typeset|\bts\b`,
	`song|\binsert\b`,
	// karaoke
	`furigana`,
}

// reNonDialogueStyle matches ASS style names that indicate non-dialogue
// content: OP/ED lyrics, karaoke, signs, titles, credits, typesetting.
var reNonDialogueStyle = regexp.MustCompile(`(?i)` + strings.Join(nonDialogueStylePatterns, "|"))

// reDialogueStyle matches ASS style names that are known dialogue patterns.
// Used as a whitelist when the blacklist alone can't distinguish dialogue
// from typesetting (e.g. font-named styles like "obelixpro", "sass").
var reDialogueStyle = regexp.MustCompile(
	`(?i)^(?:` +
		`default|main|dialogue|subtitle|narrator|narration|` +
		`italics?|italic|flashback|thought|internal|overlap|` +
		`alt(?:ernate|ernative)?|secondary|` +
		`top|bottom|eng\b|` +
		`ef\b|fsn\b|b&w|` + // common fansub abbreviations for dialogue variants
		`letter|loudspeaker|` +
		`gjm_` + // GJM fansub prefix
		`)`,
)

// classifyStyles decides which ASS styles are dialogue: a style matching
// reNonDialogueStyle never is; when many styles are unrecognized (fansubs
// naming typesetting styles after fonts) only those matching reDialogueStyle
// are; and when that leaves none, the non-blacklisted style with the most
// events in styleCounts is, which catches opaque names such as "aGB".
func classifyStyles(styleNames []string, styleCounts map[string]int) map[string]bool {
	dialogue := make(map[string]bool)
	var unknown []string

	for _, name := range styleNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if reNonDialogueStyle.MatchString(trimmed) {
			continue // blacklisted
		}
		if reDialogueStyle.MatchString(trimmed) {
			dialogue[trimmed] = true
		} else {
			unknown = append(unknown, trimmed)
		}
	}

	// If few styles are unrecognized, they're probably dialogue variants
	// (e.g. "On Top", "B1") — keep them.
	if len(unknown) <= 3 {
		for _, name := range unknown {
			dialogue[name] = true
		}
	}

	fallbackToMostUsed(dialogue, unknown, styleCounts)

	return dialogue
}

// fallbackToMostUsed picks the non-blacklisted style with the most cues
// when no whitelisted style has actual cues in [Events]. This handles
// opaque abbreviations (e.g. "aGB") and files where "Default" is defined
// but unused.
func fallbackToMostUsed(dialogue map[string]bool, unknown []string, styleCounts map[string]int) {
	for name := range dialogue {
		if styleCounts[name] > 0 {
			return // at least one whitelisted style has cues
		}
	}
	if len(unknown) == 0 {
		return
	}
	var bestName string
	var bestCount int
	for _, name := range unknown {
		if c := styleCounts[name]; c > bestCount {
			bestCount = c
			bestName = name
		}
	}
	if bestName != "" {
		dialogue[bestName] = true
	}
}

// dialogueStyles classifies the styles events use, in first-use order.
func dialogueStyles(events []timing.Event) map[string]bool {
	var names []string
	counts := make(map[string]int)
	for _, e := range events {
		if counts[e.Style] == 0 {
			names = append(names, e.Style)
		}
		counts[e.Style]++
	}
	return classifyStyles(names, counts)
}
