package subsync

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestClassifyStyles verifies the ASS style classification logic.
func TestClassifyStyles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		styles   []string
		counts   map[string]int
		wantDlg  []string
		wantSkip []string
	}{
		{
			name:    "empty input returns empty",
			styles:  []string{},
			wantDlg: []string{},
		},
		{
			name:   "fallback skipped when all unknown have zero cues",
			styles: []string{"T-Logo", "T-Titulo", "T-Caderno", "T-Placa", "T-Monitor", "aGB", "ED-PT"},
			counts: map[string]int{
				"T-Logo": 0, "T-Titulo": 0, "T-Caderno": 0,
				"T-Placa": 0, "T-Monitor": 0, "aGB": 0, "ED-PT": 0,
			},
			wantDlg:  []string{},
			wantSkip: []string{"ED-PT"},
		},
		{
			name:     "empty and whitespace styles ignored",
			styles:   []string{"", "  ", "Default", "Signs"},
			wantDlg:  []string{"Default"},
			wantSkip: []string{"Signs"},
		},
		{
			name:     "all styles blacklisted returns empty",
			styles:   []string{"OP", "ED", "Signs", "Karaoke"},
			wantDlg:  []string{},
			wantSkip: []string{"OP", "ED", "Signs", "Karaoke"},
		},
		{
			name:    "all whitelisted styles with zero cues returns empty set",
			styles:  []string{"Default", "Italics"},
			counts:  map[string]int{"Default": 0, "Italics": 0},
			wantDlg: []string{"Default", "Italics"},
		},
		{
			name:     "basic default+signs",
			styles:   []string{"Default", "Signs", "OP", "ED"},
			wantDlg:  []string{"Default"},
			wantSkip: []string{"Signs", "OP", "ED"},
		},
		{
			name:     "subtitle not killed by title",
			styles:   []string{"Subtitle", "Subtitle-2", "Song", "Caption"},
			wantDlg:  []string{"Subtitle", "Subtitle-2"},
			wantSkip: []string{"Song", "Caption"},
		},
		{
			name:     "bleach heavy karaoke",
			styles:   []string{"Default", "Default Overlap", "Default top", "Sign", "Bleach OP 3 - Romanji", "Bleach ED 6 - Kanji"},
			wantDlg:  []string{"Default", "Default Overlap", "Default top"},
			wantSkip: []string{"Sign", "Bleach OP 3 - Romanji", "Bleach ED 6 - Kanji"},
		},
		{
			name:     "saiki font-named typesetting",
			styles:   []string{"Default", "Default ita", "Default alt", "op2", "op2 - romaji", "ed1", "ed1 romaji a", "obelixpro", "sass", "skrunch", "alin kid", "creativeblock", "ink", "pat"},
			wantDlg:  []string{"Default", "Default ita", "Default alt"},
			wantSkip: []string{"op2", "op2 - romaji", "ed1", "ed1 romaji a", "obelixpro", "sass", "skrunch", "alin kid", "creativeblock", "ink", "pat"},
		},
		{
			name:     "yuruyuri abbreviated styles",
			styles:   []string{"Default", "Dialogue", "EDE1", "EDE2", "EDR1", "EDR2", "OPE", "OPR", "TS"},
			wantDlg:  []string{"Default", "Dialogue"},
			wantSkip: []string{"EDE1", "EDE2", "EDR1", "EDR2", "OPE", "OPR", "TS"},
		},
		{
			name:     "one piece mixed",
			styles:   []string{"Default", "DefaultLow", "Italics", "On Top", "B1", "Ep Title", "Copy of OS", "OS"},
			wantDlg:  []string{"Default", "DefaultLow", "Italics"},
			wantSkip: []string{"Ep Title"},
			// On Top, B1, Copy of OS, OS: 4 unknown > 3, whitelist only
		},
		{
			name:     "few unknown kept",
			styles:   []string{"Default", "Alt", "B1"},
			wantDlg:  []string{"Default", "Alt", "B1"},
			wantSkip: []string{},
		},
		{
			name:     "op2 ed1 numbered",
			styles:   []string{"Default", "op2", "ed1", "ed12"},
			wantDlg:  []string{"Default"},
			wantSkip: []string{"op2", "ed1", "ed12"},
		},
		{
			name:     "fansub group prefix",
			styles:   []string{"GJM_Main", "GJM_Overlap", "Signs", "OP Romaji"},
			wantDlg:  []string{"GJM_Main", "GJM_Overlap"},
			wantSkip: []string{"Signs", "OP Romaji"},
		},
		{
			name:     "flashback and narration",
			styles:   []string{"Default", "Flashback", "Flashback Italics Dialogue", "Narration", "Italic", "Signs"},
			wantDlg:  []string{"Default", "Flashback", "Flashback Italics Dialogue", "Narration", "Italic"},
			wantSkip: []string{"Signs"},
		},
		{
			name:     "ending variants not dialogue",
			styles:   []string{"Default", "Ending Eng", "Ending Jap", "End Card", "End"},
			wantDlg:  []string{"Default"},
			wantSkip: []string{"Ending Eng", "Ending Jap"},
			// End Card, End: 2 unknown <= 3, kept
		},
		{
			name:     "nana subtitle style",
			styles:   []string{"Default", "Subtitle", "Subtitle-2", "Song", "Song-2", "Caption", "Caption-2"},
			wantDlg:  []string{"Default", "Subtitle", "Subtitle-2"},
			wantSkip: []string{"Song", "Song-2", "Caption", "Caption-2"},
		},
		{
			name:   "goldenboy opaque abbreviation fallback",
			styles: []string{"Default", "T-Logo", "T-Titulo", "T-Caderno", "T-Placa", "T-Monitor", "aGB", "ED-PT"},
			counts: map[string]int{
				"Default": 0, "T-Logo": 223, "T-Titulo": 6, "T-Caderno": 106,
				"T-Placa": 12, "T-Monitor": 8, "aGB": 373, "ED-PT": 0,
			},
			wantDlg:  []string{"aGB"},
			wantSkip: []string{"ED-PT", "T-Logo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			counts := tt.counts
			if counts == nil {
				counts = make(map[string]int)
				for _, s := range tt.styles {
					counts[s] = 100
				}
			}
			got := classifyStyles(tt.styles, counts)

			for _, s := range tt.wantDlg {
				if !got[s] {
					t.Errorf("style %q should be dialogue, got filtered", s)
				}
			}
			for _, s := range tt.wantSkip {
				if got[s] {
					t.Errorf("style %q should be filtered, got dialogue", s)
				}
			}
		})
	}
}

func TestParseASSTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"zero", "0:00:00.00", 0, false},
		{"one second", "0:00:01.00", time.Second, false},
		{"centiseconds", "0:00:00.50", 500 * time.Millisecond, false},
		{"full", "1:23:45.67", time.Hour + 23*time.Minute + 45*time.Second + 670*time.Millisecond, false},
		{"leading space", " 0:00:01.00", time.Second, false},
		{"trailing space", "0:00:01.00 ", time.Second, false},
		{"invalid", "not a time", 0, true},
		{"empty", "", 0, true},
		{"partial", "0:00", 0, true},
		{"max centiseconds", "0:00:00.99", 990 * time.Millisecond, false},
		{"large hour", "9:59:59.99", 9*time.Hour + 59*time.Minute + 59*time.Second + 990*time.Millisecond, false},
		{"missing centiseconds", "0:00:01", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseASSTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseASSTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseASSTime(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripASSOverrides(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{"no overrides", "Hello world", "Hello world"},
		{"simple override", `{\b1}Bold text`, "Bold text"},
		{"multiple overrides", `{\b1}Bold {\i1}italic`, "Bold italic"},
		{"complex override", `{\blur1.2\fade(40,160)}Text`, "Text"},
		{"empty override", `{}Text`, "Text"},
		{"override only", `{\an8}`, ""},
		{"mixed content", `Normal {\b1}bold{\b0} normal`, "Normal bold normal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripASSOverrides(tt.input)
			if got != tt.want {
				t.Errorf("stripASSOverrides(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseASSDialogue(t *testing.T) {
	t.Parallel()

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()
		dlg, mask, err := ParseASSDialogue(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dlg) != 0 || len(mask) != 0 {
			t.Errorf("expected empty results, got %d dialogue, %d mask", len(dlg), len(mask))
		}
	})

	t.Run("basic dialogue", func(t *testing.T) {
		t.Parallel()
		data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
			"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
			"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello world\n")
		dlg, mask, err := ParseASSDialogue(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dlg) != 1 {
			t.Fatalf("expected 1 dialogue cue, got %d", len(dlg))
		}
		if dlg[0].Text != "Hello world" {
			t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Hello world")
		}
		if dlg[0].Start != 1*time.Second {
			t.Errorf("dialogue start = %v, want %v", dlg[0].Start, 1*time.Second)
		}
		if dlg[0].End != 3*time.Second {
			t.Errorf("dialogue end = %v, want %v", dlg[0].End, 3*time.Second)
		}
		if len(mask) != 1 {
			t.Errorf("expected 1 mask cue, got %d", len(mask))
		}
	})

	t.Run("filters non-dialogue styles", func(t *testing.T) {
		t.Parallel()
		data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\nStyle: Signs,Arial,20,&H00FFFFFF\n\n" +
			"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
			"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello\n" +
			"Dialogue: 0,0:00:04.00,0:00:06.00,Signs,,0,0,0,,Sign text\n")
		dlg, mask, err := ParseASSDialogue(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(dlg) != 1 {
			t.Fatalf("expected 1 dialogue cue, got %d", len(dlg))
		}
		if dlg[0].Text != "Hello" {
			t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Hello")
		}
		// Both cues appear in mask.
		if len(mask) != 2 {
			t.Errorf("expected 2 mask cues, got %d", len(mask))
		}
	})
}

func TestParseASSDialogue_newline_replacement(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		`Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Line one\NLine two` + "\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(dlg))
	}
	if dlg[0].Text != "Line one\nLine two" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Line one\nLine two")
	}
}

func TestParseASSDialogue_soft_newline_replacement(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		`Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Line one\nLine two` + "\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(dlg))
	}
	if dlg[0].Text != "Line one\nLine two" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Line one\nLine two")
	}
}

func TestParseASSDialogue_non_breaking_space(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		`Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Word\hword` + "\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(dlg))
	}
	if dlg[0].Text != "Word\u00A0word" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Word\u00A0word")
	}
}

func TestParseASSDialogue_no_events_section(t *testing.T) {
	t.Parallel()
	data := []byte("[Script Info]\nTitle: Test\n\n[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n")
	dlg, mask, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 0 || len(mask) != 0 {
		t.Errorf("expected empty results, got %d dialogue, %d mask", len(dlg), len(mask))
	}
}

func TestParseASSDialogue_text_with_commas(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello, world, how are you?\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(dlg))
	}
	if dlg[0].Text != "Hello, world, how are you?" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Hello, world, how are you?")
	}
}

func TestParseASSDialogue_empty_text_after_strip(t *testing.T) {
	t.Parallel()
	// Dialogue line where text is only ASS overrides; after stripping, text is empty.
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		`Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\an8}` + "\n" +
		"Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,Real text\n")
	dlg, mask, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First cue has empty text after stripping, should be excluded from both.
	if len(dlg) != 1 {
		t.Fatalf("expected 1 dialogue cue, got %d", len(dlg))
	}
	if len(mask) != 1 {
		t.Errorf("expected 1 mask cue, got %d", len(mask))
	}
}

func TestParseASSDialogue_malformed_line_skipped(t *testing.T) {
	t.Parallel()
	// Dialogue line with fewer than 10 comma-separated fields.
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default\n" +
		"Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,Valid text\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue (malformed skipped), got %d", len(dlg))
	}
}

func TestParseASSDialogue_invalid_timestamp_skipped(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,bad:time,0:00:03.00,Default,,0,0,0,,Bad start\n" +
		"Dialogue: 0,0:00:01.00,bad:time,Default,,0,0,0,,Bad end\n" +
		"Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,Good\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue (bad timestamps skipped), got %d", len(dlg))
	}
	if dlg[0].Text != "Good" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Good")
	}
}

func TestIsASSContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{"script info header", []byte("[Script Info]\nTitle: Test"), true},
		{"script info with BOM", append([]byte{0xEF, 0xBB, 0xBF}, []byte("[Script Info]\n")...), true},
		{"dialogue line only", []byte("Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello"), true},
		{"dialogue with leading whitespace", []byte("  Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello"), true},
		{"srt content", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"), false},
		{"empty input", []byte{}, false},
		{"nil input", nil, false},
		{"plain text", []byte("Just some random text without any ASS markers"), false},
		{"script info deep in file", append(make([]byte, 600), []byte("[Script Info]")...), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsASSContent(tt.input)
			if got != tt.want {
				t.Errorf("IsASSContent(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseASSDialogue_section_after_events(t *testing.T) {
	t.Parallel()
	// A section header after [Events] should stop cue extraction.
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Before\n" +
		"[Fonts]\n" +
		"Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,After\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the cue before [Fonts] should be extracted.
	if len(dlg) != 1 {
		t.Fatalf("expected 1 cue (section break), got %d", len(dlg))
	}
	if dlg[0].Text != "Before" {
		t.Errorf("dialogue text = %q, want %q", dlg[0].Text, "Before")
	}
}

func TestParseASSDialogue_scanner_error_oversized_line(t *testing.T) {
	t.Parallel()
	// Build a Dialogue line exceeding the 1MB internal buffer.
	longText := strings.Repeat("A", 1024*1024+100)
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,," + longText + "\n")
	_, _, err := ParseASSDialogue(data)
	if err == nil {
		t.Fatal("expected scanner error for oversized line, got nil")
	}
}

func TestParseASSTime_round_trip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		h := rapid.IntRange(0, 9).Draw(t, "h")
		m := rapid.IntRange(0, 59).Draw(t, "m")
		s := rapid.IntRange(0, 59).Draw(t, "s")
		cs := rapid.IntRange(0, 99).Draw(t, "cs")
		input := fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
		got, err := parseASSTime(input)
		if err != nil {
			t.Fatalf("parseASSTime(%q) unexpected error: %v", input, err)
		}
		want := time.Duration(h)*time.Hour +
			time.Duration(m)*time.Minute +
			time.Duration(s)*time.Second +
			time.Duration(cs)*10*time.Millisecond
		if got != want {
			t.Errorf("parseASSTime(%q) = %v, want %v", input, got, want)
		}
	})
}

func TestStripASSOverrides_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "input")
		once := stripASSOverrides(input)
		twice := stripASSOverrides(once)
		if once != twice {
			t.Errorf("not idempotent: stripASSOverrides(%q) = %q, second pass = %q",
				input, once, twice)
		}
	})
}

func TestParseASSDialogue_malformed_style_lines(t *testing.T) {
	t.Parallel()
	// Style lines without commas or with empty names should be skipped gracefully.
	// Only the well-formed "Default" style should be recognized.
	data := []byte("[V4+ Styles]\n" +
		"Style: NoCommaHere\n" +
		"Style: ,Arial,20,&H00FFFFFF\n" +
		"Style: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Hello\n" +
		"Dialogue: 0,0:00:04.00,0:00:06.00,NoCommaHere,,0,0,0,,Orphan\n")
	dlg, mask, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "Default" is whitelisted dialogue. "NoCommaHere" was never collected as a style
	// name (no comma), so it's not in dialogueStyles. Its cue still appears in mask
	// (all cues go to mask) but not in dialogue.
	if len(dlg) != 1 {
		t.Fatalf("ParseASSDialogue() dialogue cues = %d, want 1", len(dlg))
	}
	if dlg[0].Text != "Hello" {
		t.Errorf("ParseASSDialogue() dialogue text = %q, want %q", dlg[0].Text, "Hello")
	}
	if len(mask) != 2 {
		t.Errorf("ParseASSDialogue() mask cues = %d, want 2", len(mask))
	}
}

func TestParseASSDialogue_multiple_adjacent_overrides(t *testing.T) {
	t.Parallel()
	data := []byte("[V4+ Styles]\nStyle: Default,Arial,20,&H00FFFFFF\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		`Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\pos(320,50)}{\fad(500,500)}Actual text` + "\n")
	dlg, _, err := ParseASSDialogue(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dlg) != 1 {
		t.Fatalf("ParseASSDialogue() dialogue cues = %d, want 1", len(dlg))
	}
	if dlg[0].Text != "Actual text" {
		t.Errorf("ParseASSDialogue() text = %q, want %q", dlg[0].Text, "Actual text")
	}
}

func TestClassifyStyles_blacklist_invariant(t *testing.T) {
	t.Parallel()
	// Known blacklisted style names that must never be classified as dialogue.
	blacklisted := []string{
		"OP", "ED", "OP2", "ED1", "OPE", "OPR", "EDE1", "EDR2",
		"Opening", "Ending",
		"Romaji", "Romanji", "Kanji",
		"Karaoke", "Kara", "Lyrics",
		"Sign", "Title", "Credit", "Note", "Caption",
		"Typeset", "TS",
		"Song", "Insert",
		"Furigana",
	}
	rapid.Check(t, func(t *rapid.T) {
		// Pick 1-3 blacklisted styles and 0-2 dialogue styles.
		numBlack := rapid.IntRange(1, 3).Draw(t, "numBlack")
		numDlg := rapid.IntRange(0, 2).Draw(t, "numDlg")
		dlgPool := []string{"Default", "Main", "Dialogue", "Italics", "Narrator"}

		var styles []string
		counts := make(map[string]int)

		for i := range numBlack {
			idx := rapid.IntRange(0, len(blacklisted)-1).Draw(t, fmt.Sprintf("blackIdx%d", i))
			name := blacklisted[idx]
			styles = append(styles, name)
			counts[name] = rapid.IntRange(0, 500).Draw(t, fmt.Sprintf("blackCount%d", i))
		}
		for i := range numDlg {
			idx := rapid.IntRange(0, len(dlgPool)-1).Draw(t, fmt.Sprintf("dlgIdx%d", i))
			name := dlgPool[idx]
			styles = append(styles, name)
			counts[name] = rapid.IntRange(1, 500).Draw(t, fmt.Sprintf("dlgCount%d", i))
		}

		result := classifyStyles(styles, counts)

		// Invariant: no blacklisted style should ever be in the result.
		for _, name := range blacklisted {
			if result[name] {
				t.Errorf("classifyStyles() included blacklisted style %q in dialogue", name)
			}
		}
	})
}
