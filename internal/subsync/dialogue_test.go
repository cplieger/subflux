package subsync

import (
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// --- cleanTextLen ---

func TestCleanTextLen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		want     int
		wantNon0 bool // when true, assert got != 0 instead of exact match
	}{
		// Basic cases.
		{"plain_dialogue", "Hello, how are you?", 19, false},
		{"empty_string", "", 0, false},
		{"whitespace_only", "   \t  ", 0, false},
		{"html_tags_stripped", "<i>Hello</i>", 5, false},
		// ASS overrides — non-dialogue (should return 0).
		{"ass_an8", `{\an8}Sign text`, 0, false},
		{"ass_an1", `{\an1}Bottom left`, 0, false},
		{"ass_an9", `{\an9}Top right`, 0, false},
		{"ass_drawing_mode", `{\p1}m 0 0 l 100 0 100 100 0 100`, 0, false},
		{"ass_karaoke_k", `{\k50}Lyrics here`, 0, false},
		{"ass_karaoke_kf", `{\kf100}More lyrics`, 0, false},
		{"ass_inline_comment", `{*\frz45}Rotated sign`, 0, false},
		{"ass_karaoke_K", `{\K100}Fill lyrics`, 0, false},
		{"ass_karaoke_ko", `{\ko50}Outline lyrics`, 0, false},
		// ASS an2 is dialogue (default position).
		{"ass_an2_is_dialogue", `{\an2}Normal dialogue`, 0, true},
		// Drawing commands in text.
		{"drawing_commands", "m 0 0 l 100 100 b 50 50", 0, false},
		// HI annotations stripped.
		{"hi_brackets", "[gunshot] Run!", 4, false},
		{"hi_parens", "(laughing) Funny", 5, false},
		{"hi_speaker", "JOHN: Hello", 5, false},
		// Music notes stripped.
		{"music_pair", "♪ La di da ♪", 0, false},
		{"music_double", "♫ Song ♫", 0, false},
		{"music_standalone", "♪♪♪", 0, false},
		// Sound effects.
		{"sfx_crash", "crash", 0, false},
		{"sfx_bang", "bang", 0, false},
		{"sfx_screams", "screams", 0, false},
		{"sfx_gasps", "gasps", 0, false},
		// Multiline with dialogue.
		{"multiline_sfx_with_dialogue", "crash\nRun!", 0, true},
		// Music line prefix.
		{"music_line_prefix", "* Singing a song", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cleanTextLen(tt.input)
			if tt.wantNon0 {
				if got == 0 {
					t.Errorf("cleanTextLen(%q) = 0, want non-zero", tt.input)
				}
			} else if got != tt.want {
				t.Errorf("cleanTextLen(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// --- isNonDialogueCue ---

func TestIsNonDialogueCue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain dialogue", "Hello world", false},
		{"ass non-default alignment", `{\an8}Sign`, true},
		{"ass drawing mode", `{\p1}m 0 0 l 100 0`, true},
		{"ass karaoke", `{\k50}Lyrics`, true},
		{"drawing commands in text", "m 0 0 l 100 100 b 50 50", true},
		{"fansub credit", "{Fansub Group}", true},
		{"ass override tag (not credit)", `{\b1}Bold text`, false},
		{"ass inline comment", `{*\frz45}Rotated sign`, true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNonDialogueCue(tt.input)
			if got != tt.want {
				t.Errorf("isNonDialogueCue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- isForeignFont ---

func TestIsForeignFont(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		text         string
		dialogueFont string
		want         bool
	}{
		{"no dialogue font detected", `<font face="Arial">Hello</font>`, "", false},
		{"matching font", `<font face="Arial">Hello</font>`, "Arial", false},
		{"different font", `<font face="Impact">Sign</font>`, "Arial", true},
		{"no font tag in text", "Plain text", "Arial", false},
		{"empty text", "", "Arial", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isForeignFont(tt.text, tt.dialogueFont)
			if got != tt.want {
				t.Errorf("isForeignFont(%q, %q) = %v, want %v",
					tt.text, tt.dialogueFont, got, tt.want)
			}
		})
	}
}

// --- detectDialogueFont ---

func TestDetectDialogueFont_too_few_cues(t *testing.T) {
	t.Parallel()
	cues := make([]Cue, 19)
	for i := range cues {
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+1) * time.Second,
			Text:  `<font face="Arial">Hello</font>`,
		}
	}
	got := detectDialogueFont(cues)
	if got != "" {
		t.Errorf("detectDialogueFont(19 cues) = %q, want empty", got)
	}
}

func TestDetectDialogueFont_single_font_returns_empty(t *testing.T) {
	t.Parallel()
	// With only one font, there's no contrast to detect dialogue vs typesetting.
	cues := make([]Cue, 30)
	for i := range cues {
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+2) * time.Second,
			Text:  `<font face="Arial">Dialogue line</font>`,
		}
	}
	got := detectDialogueFont(cues)
	if got != "" {
		t.Errorf("detectDialogueFont(single font) = %q, want empty", got)
	}
}

func TestDetectDialogueFont_picks_dominant_font(t *testing.T) {
	t.Parallel()
	// Build 50 cues: 40 with Arial (dialogue), 10 with Impact (signs).
	cues := make([]Cue, 50)
	for i := range cues {
		font := "Arial"
		if i%5 == 0 {
			font = "Impact"
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+3) * time.Second,
			Text:  `<font face="` + font + `">Text</font>`,
		}
	}
	got := detectDialogueFont(cues)
	if got != "Arial" {
		t.Errorf("detectDialogueFont(dominant) = %q, want %q", got, "Arial")
	}
}

func TestDetectDialogueFont_skips_short_duration_cues(t *testing.T) {
	t.Parallel()
	// Font with very short cues (< 500ms avg) should be excluded.
	cues := make([]Cue, 50)
	for i := range cues {
		if i < 40 {
			// Short-duration font (100ms each).
			cues[i] = Cue{
				Start: time.Duration(i) * 100 * time.Millisecond,
				End:   time.Duration(i)*100*time.Millisecond + 100*time.Millisecond,
				Text:  `<font face="ShortFont">X</font>`,
			}
		} else {
			// Normal-duration font (3s each).
			cues[i] = Cue{
				Start: time.Duration(i) * time.Second,
				End:   time.Duration(i+3) * time.Second,
				Text:  `<font face="NormalFont">Dialogue</font>`,
			}
		}
	}
	got := detectDialogueFont(cues)
	if got != "NormalFont" {
		t.Errorf("detectDialogueFont(short duration) = %q, want %q", got, "NormalFont")
	}
}

func TestDetectDialogueFont_no_font_tags(t *testing.T) {
	t.Parallel()
	cues := make([]Cue, 30)
	for i := range cues {
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+2) * time.Second,
			Text:  "Plain dialogue without font tags",
		}
	}
	got := detectDialogueFont(cues)
	if got != "" {
		t.Errorf("detectDialogueFont(no fonts) = %q, want empty", got)
	}
}

// --- markKaraokePairs ---

func TestMarkKaraokePairs_no_non_dialogue(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "Hello"},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "World"},
	}
	got := markKaraokePairs(cues)
	if len(got) != 0 {
		t.Errorf("markKaraokePairs(no non-dialogue) = %v, want empty", got)
	}
}

func TestMarkKaraokePairs_detects_shared_timestamps(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		// Non-dialogue cue (karaoke with ASS tags).
		{Start: time.Second, End: 3 * time.Second, Text: `{\k50}Romaji lyrics`},
		// Dialogue cue sharing exact same timestamps (English translation).
		{Start: time.Second, End: 3 * time.Second, Text: "English translation"},
		// Unrelated dialogue cue.
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Normal dialogue"},
	}
	got := markKaraokePairs(cues)
	if !got[1] {
		t.Error("markKaraokePairs should mark index 1 (shared timestamps with karaoke)")
	}
	if got[2] {
		t.Error("markKaraokePairs should not mark index 2 (different timestamps)")
	}
	if got[0] {
		t.Error("markKaraokePairs should not mark index 0 (already non-dialogue)")
	}
}

func TestMarkKaraokePairs_empty_input(t *testing.T) {
	t.Parallel()
	got := markKaraokePairs(nil)
	if len(got) != 0 {
		t.Errorf("markKaraokePairs(nil) = %v, want empty", got)
	}
}

// --- filterDialogueCues ---

func TestFilterDialogueCues_keeps_plain_dialogue(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "Hello world"},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "How are you?"},
	}
	got := filterDialogueCues(cues, nil)
	if len(got) != 2 {
		t.Errorf("filterDialogueCues(plain) returned %d cues, want 2", len(got))
	}
}

func TestFilterDialogueCues_removes_non_dialogue(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "Hello"},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: `{\an8}Sign text`},
		{Start: 7 * time.Second, End: 9 * time.Second, Text: "m 0 0 l 100 100 b 50 50"},
	}
	got := filterDialogueCues(cues, nil)
	if len(got) != 1 {
		t.Errorf("filterDialogueCues(mixed) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Hello" {
		t.Errorf("filterDialogueCues(mixed)[0].Text = %q, want %q", got[0].Text, "Hello")
	}
}

func TestFilterDialogueCues_removes_karaoke_pairs(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: `{\k50}Romaji`},
		{Start: time.Second, End: 3 * time.Second, Text: "English translation"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Normal dialogue"},
	}
	pairs := markKaraokePairs(cues)
	got := filterDialogueCues(cues, pairs)
	if len(got) != 1 {
		t.Errorf("filterDialogueCues(karaoke pairs) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Normal dialogue" {
		t.Errorf("filterDialogueCues(karaoke pairs)[0].Text = %q, want %q",
			got[0].Text, "Normal dialogue")
	}
}

func TestFilterDialogueCues_removes_fansub_credits(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "{Fansub Group v2}"},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "Real dialogue"},
	}
	got := filterDialogueCues(cues, nil)
	if len(got) != 1 {
		t.Errorf("filterDialogueCues(credits) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Real dialogue" {
		t.Errorf("filterDialogueCues(credits)[0].Text = %q, want %q",
			got[0].Text, "Real dialogue")
	}
}

func TestFilterDialogueCues_empty_input(t *testing.T) {
	t.Parallel()
	got := filterDialogueCues(nil, nil)
	if got != nil {
		t.Errorf("filterDialogueCues(nil) = %v, want nil", got)
	}
}

func TestFilterDialogueCues_removes_empty_after_stripping(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: "[gunshot]"},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "Real dialogue"},
	}
	got := filterDialogueCues(cues, nil)
	if len(got) != 1 {
		t.Errorf("filterDialogueCues(empty after strip) returned %d cues, want 1", len(got))
	}
}

func TestDetectDialogueFont_skips_rare_font(t *testing.T) {
	t.Parallel()
	// A font appearing in fewer than 5 cues in the middle 80% should be excluded.
	// Need 3 fonts: MainFont (dominant), SecondFont (enough to trigger len(stats)>=2),
	// and RareFont (< 5 cues in middle).
	cues := make([]Cue, 60)
	for i := range cues {
		font := "MainFont"
		if i >= 20 && i < 30 {
			font = "SecondFont"
		}
		if i >= 30 && i < 33 {
			font = "RareFont"
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+3) * time.Second,
			Text:  `<font face="` + font + `">Text</font>`,
		}
	}
	got := detectDialogueFont(cues)
	// RareFont has < 5 cues in the middle 80%, so it's excluded.
	// MainFont should win by total screen time.
	if got != "MainFont" {
		t.Errorf("detectDialogueFont(rare font) = %q, want %q", got, "MainFont")
	}
}

func TestDetectDialogueFont_skips_low_percentage_font(t *testing.T) {
	t.Parallel()
	// A font appearing in < 10% of middle cues should be excluded.
	cues := make([]Cue, 100)
	for i := range cues {
		font := "DominantFont"
		// Put 6 cues with MinorFont (6% of middle 80 = 80 cues).
		if i >= 45 && i < 51 {
			font = "MinorFont"
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+3) * time.Second,
			Text:  `<font face="` + font + `">Text</font>`,
		}
	}
	got := detectDialogueFont(cues)
	// MinorFont has 6 cues (>5) but only ~7.5% of middle, below 10% threshold.
	if got != "DominantFont" {
		t.Errorf("detectDialogueFont(low percentage) = %q, want %q", got, "DominantFont")
	}
}

func TestDetectDialogueFont_skips_long_duration_font(t *testing.T) {
	t.Parallel()
	// A font with avg duration > 15s should be excluded (likely typesetting).
	cues := make([]Cue, 50)
	for i := range cues {
		if i < 25 {
			// Long-duration font (20s each).
			cues[i] = Cue{
				Start: time.Duration(i) * 20 * time.Second,
				End:   time.Duration(i)*20*time.Second + 20*time.Second,
				Text:  `<font face="LongFont">X</font>`,
			}
		} else {
			// Normal-duration font (3s each).
			cues[i] = Cue{
				Start: time.Duration(i) * time.Second,
				End:   time.Duration(i+3) * time.Second,
				Text:  `<font face="NormalFont">Dialogue</font>`,
			}
		}
	}
	got := detectDialogueFont(cues)
	if got != "NormalFont" {
		t.Errorf("detectDialogueFont(long duration) = %q, want %q", got, "NormalFont")
	}
}

func TestFilterDialogueCues_removes_foreign_font(t *testing.T) {
	t.Parallel()
	// Build enough cues to trigger font detection, then add a foreign-font cue.
	cues := make([]Cue, 52)
	for i := range 50 {
		font := "Arial"
		if i%10 == 0 {
			font = "Impact"
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+3) * time.Second,
			Text:  `<font face="` + font + `">Dialogue line</font>`,
		}
	}
	// Add a foreign-font cue at the end.
	cues[50] = Cue{
		Start: 50 * time.Second,
		End:   53 * time.Second,
		Text:  `<font face="Impact">Sign text</font>`,
	}
	cues[51] = Cue{
		Start: 54 * time.Second,
		End:   57 * time.Second,
		Text:  `<font face="Arial">Normal dialogue</font>`,
	}

	got := filterDialogueCues(cues, nil)
	// All Impact-font cues should be filtered as foreign font.
	for _, c := range got {
		if c.Text == `Sign text` {
			t.Error("filterDialogueCues should have removed foreign-font cue")
		}
	}
}

func TestCleanTextLen_multiline_mixed_music_and_dialogue(t *testing.T) {
	t.Parallel()
	got := cleanTextLen("* Singing a song\nWhat did you say?")
	if got == 0 {
		t.Error("cleanTextLen(multiline with music + dialogue) should not return 0")
	}
}

func TestDetectDialogueFont_tiebreaker_alphabetical(t *testing.T) {
	t.Parallel()
	cues := make([]Cue, 40)
	for i := range cues {
		font := "Bravo"
		if i%2 == 0 {
			font = "Alpha"
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i+3) * time.Second,
			Text:  `<font face="` + font + `">Text</font>`,
		}
	}
	got := detectDialogueFont(cues)
	if got != "Alpha" {
		t.Errorf("detectDialogueFont(tiebreaker) = %q, want %q (alphabetical)", got, "Alpha")
	}
}

func TestDetectDialogueFont_ignores_zero_duration_cues(t *testing.T) {
	t.Parallel()
	cues := make([]Cue, 50)
	for i := range cues {
		font := "MainFont"
		dur := 3 * time.Second
		if i%5 == 0 {
			font = "ZeroFont"
			dur = 0
		}
		cues[i] = Cue{
			Start: time.Duration(i) * time.Second,
			End:   time.Duration(i)*time.Second + dur,
			Text:  `<font face="` + font + `">Text</font>`,
		}
	}
	got := detectDialogueFont(cues)
	if got != "MainFont" {
		t.Errorf("detectDialogueFont(zero duration) = %q, want %q", got, "MainFont")
	}
}

func TestMarkKaraokePairs_multiple_dialogue_at_same_timestamp(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: `{\k50}Karaoke`},
		{Start: time.Second, End: 3 * time.Second, Text: "Romaji text"},
		{Start: time.Second, End: 3 * time.Second, Text: "English text"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Normal"},
	}
	got := markKaraokePairs(cues)
	if !got[1] || !got[2] {
		t.Errorf("markKaraokePairs should mark indices 1 and 2, got %v", got)
	}
	if got[0] || got[3] {
		t.Errorf("markKaraokePairs should not mark indices 0 or 3, got %v", got)
	}
}

func TestFilterDialogueCues_all_filtered_returns_nil(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 3 * time.Second, Text: `{\an8}Sign`},
		{Start: 4 * time.Second, End: 6 * time.Second, Text: "[gunshot]"},
		{Start: 7 * time.Second, End: 9 * time.Second, Text: "♪ Music ♪"},
	}
	got := filterDialogueCues(cues, nil)
	if len(got) != 0 {
		t.Errorf("filterDialogueCues(all non-dialogue) returned %d cues, want 0", len(got))
	}
}

func TestCleanTextLen_combined_html_and_ass_tags(t *testing.T) {
	t.Parallel()
	got := cleanTextLen(`<i>{\b1}Bold italic</i>`)
	if got != len("Bold italic") {
		t.Errorf("cleanTextLen(combined tags) = %d, want %d", got, len("Bold italic"))
	}
}

func TestCleanTextLen_nested_html_tags(t *testing.T) {
	t.Parallel()
	got := cleanTextLen("<i><b>text</b></i>")
	if got != 4 {
		t.Errorf("cleanTextLen(nested HTML) = %d, want 4", got)
	}
}

func TestCleanTextLen_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")
		got := cleanTextLen(text)
		if got < 0 {
			t.Errorf("cleanTextLen(%q) = %d, want >= 0", text, got)
		}
	})
}

func TestIsNonDialogueCue_implies_zero_cleanTextLen(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")
		if isNonDialogueCue(text) && !reFansubCredit.MatchString(strings.TrimSpace(text)) {
			// Fansub credits are intentionally not checked by cleanTextLen
			// (filtered separately in filterDialogueCues). For all other
			// non-dialogue markers (ASS overrides, drawings), cleanTextLen
			// should return 0.
			got := cleanTextLen(text)
			if got != 0 {
				t.Errorf("isNonDialogueCue(%q)=true but cleanTextLen=%d, want 0", text, got)
			}
		}
	})
}
