package subsync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"subflux/internal/subsync/ffmpeg"
)

// styleRule defines a single non-dialogue classification pattern with its category.
type styleRule struct {
	Category string // "op_ed", "karaoke", "signs", "typesetting", "song"
	Pattern  string // regex fragment
}

// nonDialogueRules is the data-driven table of non-dialogue style classification
// patterns. Each rule is individually documentable and testable.
var nonDialogueRules = []styleRule{
	{"op_ed", `\bop[a-z\d]*\b`},
	{"op_ed", `\bed[a-z\d]*\b`},
	{"op_ed", `\bopening\b`},
	{"op_ed", `\bending\b`},
	{"karaoke", `romaji|romanji|kanji`},
	{"karaoke", `karaoke|\bkara\b|lyric`},
	{"signs", `sign|\btitle|credit|\bnote\b|\bcaption\b`},
	{"typesetting", `typeset|\bts\b`},
	{"song", `song|\binsert\b`},
	{"karaoke", `furigana`},
}

// reNonDialogueStyle matches ASS style names that indicate non-dialogue
// content: OP/ED lyrics, karaoke, signs, titles, credits, typesetting.
// Built from nonDialogueRules at init time.
var reNonDialogueStyle = func() *regexp.Regexp {
	parts := make([]string, len(nonDialogueRules))
	for i, r := range nonDialogueRules {
		parts[i] = r.Pattern
	}
	return regexp.MustCompile(`(?i)` + strings.Join(parts, "|"))
}()

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
		`)`)

// classifyStyles decides which ASS styles are dialogue.
//
// Three-phase approach:
//  1. Blacklist: any style matching reNonDialogueStyle is non-dialogue.
//  2. Whitelist: among remaining styles, if the file has many unrecognized
//     styles (font-named typesetting), only keep styles matching
//     reDialogueStyle. This handles fansubs that use font names as styles.
//  3. Fallback: if the whitelist produces no styles, pick the non-blacklisted
//     style with the most cues. This handles opaque abbreviations like "aGB".
//
// styleCounts maps style name → number of dialogue lines using that style.
// Returns a set of style names that are dialogue.
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
	} else {
		slog.Debug("ASS style classification: many unknown styles, using whitelist only",
			"unknown", len(unknown),
			"whitelisted", len(dialogue))
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
		slog.Debug("ASS style classification: fallback to most-used style",
			"style", bestName,
			"cues", bestCount)
	}
}

// FFmpegExtractASSDialogue extracts an ASS subtitle stream, parses it,
// and returns dialogue cues and mask cues separately.
//
// dialogueCues: filtered to dialogue styles only (for correlation signal).
// maskCues: all cues regardless of style (for dialogue mask time regions).
func ffmpegExtractASSDialogue(ctx context.Context, videoPath string, streamIndex int) (dialogueCues, maskCues []Cue, err error) {
	if !ffmpeg.Available() {
		return nil, nil, errors.New("ffmpeg not available")
	}

	slog.Debug("extracting ASS dialogue",
		"video", videoPath,
		"stream", streamIndex)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", "file:"+videoPath,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "copy",
		"-f", "ass",
		"-loglevel", "error",
		"pipe:1",
	)

	pipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", pipeErr)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if startErr := cmd.Start(); startErr != nil {
		return nil, nil, fmt.Errorf("ffmpeg start: %w", startErr)
	}

	// Cap stdout at 50 MB to prevent unbounded memory from pathological inputs.
	limited := io.LimitReader(pipe, ffmpeg.MaxExtractBytes)
	data, readErr := io.ReadAll(limited)

	if waitErr := cmd.Wait(); waitErr != nil {
		return nil, nil, fmt.Errorf("ffmpeg extract ASS stream %d: %w: %s",
			streamIndex, waitErr, stderr.String())
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("read ffmpeg output: %w", readErr)
	}

	if len(data) == 0 {
		slog.Debug("ASS stream empty",
			"video", videoPath,
			"stream", streamIndex)
		return nil, nil, nil
	}

	return ParseASSDialogue(data)
}

// IsASSContent reports whether data looks like ASS/SSA subtitle content
// by checking for the [Script Info] header or Dialogue: lines.
func IsASSContent(data []byte) bool {
	// Check first 512 bytes for the header (handles BOM, leading whitespace).
	peek := data
	if len(peek) > 512 {
		peek = peek[:512]
	}
	return bytes.Contains(peek, []byte("[Script Info")) ||
		bytes.HasPrefix(bytes.TrimSpace(peek), []byte("Dialogue:"))
}

// ParseASSDialogue parses raw ASS content and returns dialogue cues
// and mask cues separately.
//
// dialogueCues: only cues from classified dialogue styles (for subtitle
// envelope / correlation signal).
// maskCues: all cues regardless of style classification (for dialogue
// mask / time region detection). Even OP/ED/signs mark time regions
// with audio activity, giving broader mask coverage.
//
// Two-pass: first collects all style names and cue counts, classifies
// them, then extracts cues.
func ParseASSDialogue(data []byte) (dialogueCues, maskCues []Cue, err error) {
	// ASS files with heavy typesetting can have very long lines
	// (vector drawing commands, embedded fonts). Increase buffer
	// from the default 64KB to 1MB.
	const maxLine = 1024 * 1024

	// Pass 1: collect style names and count dialogue lines per style.
	styleNames, styleCounts, scanErr := assCollectStyles(data, maxLine)
	if scanErr != nil {
		return nil, nil, scanErr
	}

	dialogueStyles := classifyStyles(styleNames, styleCounts)

	slog.Debug("ASS style classification",
		"total_styles", len(styleNames),
		"dialogue_styles", len(dialogueStyles))

	// Pass 2: extract cues. Dialogue cues are filtered by style,
	// mask cues include ALL cues for broad time coverage.
	dialogueCues, maskCues, scanErr = assExtractCues(data, maxLine, dialogueStyles)
	if scanErr != nil {
		return nil, nil, scanErr
	}

	slog.Debug("parsed ASS dialogue",
		"dialogue_cues", len(dialogueCues),
		"mask_cues", len(maskCues))

	return dialogueCues, maskCues, nil
}

// assCollectStyles scans ASS data for style definitions and dialogue line
// counts per style (pass 1 of the two-pass parser).
func assCollectStyles(data []byte, maxLine int) (styleNames []string, styleCounts map[string]int, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, maxLine), maxLine)

	styleCounts = make(map[string]int)
	inEvents := false
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "Style:"); ok {
			rest = strings.TrimSpace(rest)
			if comma := strings.IndexByte(rest, ','); comma > 0 {
				styleNames = append(styleNames, rest[:comma])
			}
		}
		if strings.HasPrefix(line, "[Events]") {
			inEvents = true
			continue
		}
		if strings.HasPrefix(line, "[") && inEvents {
			break // next section; matches pass 2 behavior
		}
		if inEvents {
			if rest, ok := strings.CutPrefix(line, "Dialogue:"); ok {
				rest = strings.TrimSpace(rest)
				parts := strings.SplitN(rest, ",", 10)
				if len(parts) >= 10 {
					styleCounts[strings.TrimSpace(parts[3])]++
				}
			}
		}
	}
	return styleNames, styleCounts, scanner.Err()
}

// assExtractCues scans ASS data and extracts dialogue and mask cues
// based on the classified dialogue styles (pass 2 of the two-pass parser).
func assExtractCues(data []byte, maxLine int, dialogueStyles map[string]bool) (dialogueCues, maskCues []Cue, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, maxLine), maxLine)
	inEvents := false

	for scanner.Scan() {
		line := scanner.Text()

		// Detect [Events] section.
		if strings.HasPrefix(line, "[Events]") {
			inEvents = true
			continue
		}
		if strings.HasPrefix(line, "[") && inEvents {
			break // next section
		}

		if !inEvents {
			continue
		}

		// Parse Dialogue lines.
		// Format: Dialogue: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text
		rest, ok := strings.CutPrefix(line, "Dialogue:")
		if !ok {
			continue
		}

		// Split into at most 10 fields (text field may contain commas).
		rest = strings.TrimSpace(rest)
		parts := strings.SplitN(rest, ",", 10)
		if len(parts) < 10 {
			continue
		}

		styleName := strings.TrimSpace(parts[3])

		// Parse timestamps.
		startTime, err1 := parseASSTime(parts[1])
		endTime, err2 := parseASSTime(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}

		// Extract text (field 9), strip ASS override tags.
		text := parts[9]
		text = stripASSOverrides(text)
		text = strings.ReplaceAll(text, `\N`, "\n")
		text = strings.ReplaceAll(text, `\n`, "\n")
		text = strings.ReplaceAll(text, `\h`, "\u00A0")
		text = strings.TrimSpace(text)

		if text == "" {
			continue
		}

		cue := Cue{Start: startTime, End: endTime, Text: text}

		// ALL cues go to mask — even OP/ED/signs mark time regions
		// with audio activity, giving broader mask coverage.
		maskCues = append(maskCues, cue)

		// Only classified dialogue styles go to dialogue cues.
		if dialogueStyles[styleName] {
			dialogueCues = append(dialogueCues, cue)
		}
	}

	return dialogueCues, maskCues, scanner.Err()
}

// parseASSTime parses an ASS timestamp like "0:02:34.56" into time.Duration.
func parseASSTime(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	// Format: H:MM:SS.CC (centiseconds)
	var h, m, sec, cs int
	n, err := fmt.Sscanf(s, "%d:%d:%d.%d", &h, &m, &sec, &cs)
	if err != nil || n < 4 {
		return 0, fmt.Errorf("invalid ASS time: %q", s)
	}
	return time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second +
		time.Duration(cs)*10*time.Millisecond, nil
}

// reASSOverride matches ASS override tags like {\blur1.2\fade(40,160)}.
var reASSOverride = regexp.MustCompile(`\{[^}]*\}`)

// stripASSOverrides removes ASS override tags from text.
func stripASSOverrides(text string) string {
	return reASSOverride.ReplaceAllString(text, "")
}
