// srt.go implements subtitle timing synchronization,
// ported from the alass constant-offset alignment algorithm.

package subsync

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// --- Types ---

// Cue represents a single subtitle cue with timing and text content.
// This is a local definition that mirrors api.SubtitleCue, decoupling
// the subsync package from the application's domain types.
type Cue struct {
	Text  string
	Start time.Duration
	End   time.Duration
}

// TimeSpan is a start/end pair used by the alignment algorithm.
type TimeSpan struct {
	Start int64 // milliseconds
	End   int64
}

// --- Public API ---

// CuesToSpans converts subtitle cues to time spans.
func cuesToSpans(cues []Cue) []TimeSpan {
	spans := make([]TimeSpan, len(cues))
	for i, c := range cues {
		spans[i] = TimeSpan{
			Start: c.Start.Milliseconds(),
			End:   c.End.Milliseconds(),
		}
	}
	return spans
}

var timeRe = regexp.MustCompile(
	`(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})`,
)

// ParseSRT parses an SRT subtitle file into cues.
func ParseSRT(r io.Reader) ([]Cue, error) {
	var cues []Cue
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Look for timing line.
		match := timeRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		start, err := parseTime(match[1], match[2], match[3], match[4])
		if err != nil {
			continue
		}
		end, err := parseTime(match[5], match[6], match[7], match[8])
		if err != nil {
			continue
		}

		// Collect text lines until blank line.
		// TrimRight strips trailing \r from CRLF line endings that
		// bufio.Scanner leaves on the text content.
		var textLines []string
		for scanner.Scan() {
			tl := strings.TrimRight(scanner.Text(), "\r")
			if strings.TrimSpace(tl) == "" {
				break
			}
			textLines = append(textLines, tl)
		}

		cues = append(cues, Cue{
			Start: start,
			End:   end,
			Text:  strings.Join(textLines, "\n"),
		})
	}

	return cues, scanner.Err()
}

// WriteSRT writes cues as an SRT file.
func WriteSRT(w io.Writer, cues []Cue) error {
	for i, c := range cues {
		_, err := fmt.Fprintf(w, "%d\n%s --> %s\n%s\n\n",
			i+1,
			formatTime(c.Start),
			formatTime(c.End),
			c.Text,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// ShiftCues applies a time offset to all cues.
func ShiftCues(cues []Cue, offset time.Duration) []Cue {
	shifted := make([]Cue, len(cues))
	for i, c := range cues {
		shifted[i] = Cue{
			Start: max(0, c.Start+offset),
			End:   max(0, c.End+offset),
			Text:  c.Text,
		}
	}
	return shifted
}

// --- Helpers ---

// parseTime converts individual SRT time components (hours, minutes, seconds,
// milliseconds as strings) into a single time.Duration.
func parseTime(h, m, s, ms string) (time.Duration, error) {
	hours, err := strconv.Atoi(h)
	if err != nil {
		return 0, err
	}
	mins, err := strconv.Atoi(m)
	if err != nil {
		return 0, err
	}
	secs, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	millis, err := strconv.Atoi(ms)
	if err != nil {
		return 0, err
	}
	return time.Duration(hours)*time.Hour +
		time.Duration(mins)*time.Minute +
		time.Duration(secs)*time.Second +
		time.Duration(millis)*time.Millisecond, nil
}

// formatTime renders a duration as an SRT timestamp (HH:MM:SS,mmm).
// Negative durations are clamped to zero.
func formatTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := d.Milliseconds()
	h := total / 3_600_000
	m := total / 60_000 % 60
	s := total / 1000 % 60
	ms := total % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
