package subsync

import (
	"fmt"
	"testing"

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
			// Exactly three unrecognized styles is still "few": they are kept
			// as dialogue variants rather than dropped to the whitelist.
			name:     "three unknown kept",
			styles:   []string{"Default", "End Card", "B1", "On Top"},
			wantDlg:  []string{"Default", "End Card", "B1", "On Top"},
			wantSkip: []string{},
		},
		{
			// Two opaque styles carry the same number of cues and nothing
			// else separates them, so the fallback takes the one defined
			// first: the choice decides which cues drive correlation, and it
			// must not depend on where the tie sits in the style list.
			name:   "most-used fallback tie takes the first style defined",
			styles: []string{"Default", "aGB", "bXQ", "kMz", "nWp"},
			counts: map[string]int{
				"Default": 0, "aGB": 500, "bXQ": 500, "kMz": 12, "nWp": 3,
			},
			wantDlg:  []string{"Default", "aGB"},
			wantSkip: []string{"bXQ", "kMz", "nWp"},
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
		{
			// A signs-and-songs track: its logo card is no dialogue.
			name:     "logo style on a track without dialogue",
			styles:   []string{"Logo", "Kara-OP-Jap", "Signs", "Kara-ED-Jap", "Preview"},
			counts:   map[string]int{"Logo": 886, "Kara-OP-Jap": 1377, "Signs": 1362, "Kara-ED-Jap": 348, "Preview": 2},
			wantSkip: []string{"Logo", "Kara-OP-Jap", "Signs", "Kara-ED-Jap"},
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
