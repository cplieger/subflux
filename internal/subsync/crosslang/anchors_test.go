package crosslang

import (
	"testing"

	"pgregory.net/rapid"
)

func TestExtractAnchors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantNums  []string
		wantNouns []string
		wantCogs  []string
		wantPunct string
		wantWords int
		wantChars int
	}{
		{
			name:      "plain_text",
			input:     "Hello world",
			wantPunct: "",
			wantWords: 2,
			wantChars: 11,
		},
		{
			name:     "numbers",
			input:    "There are 150 people and 57 dogs",
			wantNums: []string{"150", "57"},
		},
		{
			name:      "question_mark",
			input:     "Are you sure?",
			wantPunct: "?",
		},
		{
			name:      "exclamation",
			input:     "Watch out!",
			wantPunct: "!",
		},
		{
			name:      "period",
			input:     "I see.",
			wantPunct: ".",
		},
		{
			name:      "ellipsis_ascii",
			input:     "Well...",
			wantPunct: "...",
		},
		{
			name:      "ellipsis_unicode",
			input:     "Well\u2026",
			wantPunct: "...",
		},
		{
			name:      "no_punctuation",
			input:     "Hello world",
			wantPunct: "",
		},
		{
			name:      "proper_nouns_mid_sentence",
			input:     "I saw John in Paris yesterday",
			wantNouns: []string{"John", "Paris"},
		},
		{
			name:     "cognates_latin_words",
			input:    "The television president arrived",
			wantCogs: []string{"television", "president", "arrived"},
		},
		{
			name:      "strips_html_tags",
			input:     "<i>Hello</i> world",
			wantChars: 11,
			wantWords: 2,
		},
		{
			name:      "strips_ass_tags",
			input:     "{\\an8}Hello world",
			wantChars: 11,
			wantWords: 2,
		},
		{
			name:      "empty_string",
			input:     "",
			wantWords: 0,
			wantChars: 0,
		},
		{
			name:     "number_normalization_commas_dots",
			input:    "The cost is 1,500 dollars and 3.14 percent",
			wantNums: []string{"1500", "314"},
		},
		{
			name:      "multiline_dialogue",
			input:     "- Hello John!\n- How are you?",
			wantWords: 5,
			wantPunct: "?",
		},
		{
			name:      "cjk_excluded_from_cognates",
			input:     "The 東京タワー is tall",
			wantCogs:  []string{"tall"},
			wantWords: 4,
		},
		{
			name:      "cyrillic_excluded_from_cognates",
			input:     "Москва is beautiful",
			wantCogs:  []string{"beautiful"},
			wantWords: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := ExtractAnchors(tt.input)
			if tt.wantWords > 0 && a.WordCount != tt.wantWords {
				t.Errorf("WordCount = %d, want %d", a.WordCount, tt.wantWords)
			}
			if tt.wantChars > 0 && a.CharLen != tt.wantChars {
				t.Errorf("CharLen = %d, want %d", a.CharLen, tt.wantChars)
			}
			if tt.wantPunct != "" || tt.name == "no_punctuation" || tt.name == "plain_text" || tt.name == "empty_string" {
				if a.Punctuation != tt.wantPunct {
					t.Errorf("Punctuation = %q, want %q", a.Punctuation, tt.wantPunct)
				}
			}
			if tt.wantNums != nil {
				if len(a.Numbers) != len(tt.wantNums) {
					t.Fatalf("Numbers = %v, want %v", a.Numbers, tt.wantNums)
				}
				for i, want := range tt.wantNums {
					if a.Numbers[i] != want {
						t.Errorf("Numbers[%d] = %q, want %q", i, a.Numbers[i], want)
					}
				}
			}
			if tt.wantNouns != nil {
				found := map[string]bool{}
				for _, n := range a.ProperNouns {
					found[n] = true
				}
				for _, want := range tt.wantNouns {
					if !found[want] {
						t.Errorf("ProperNouns missing %q, got %v", want, a.ProperNouns)
					}
				}
			}
			if tt.wantCogs != nil {
				found := map[string]bool{}
				for _, c := range a.Cognates {
					found[c] = true
				}
				for _, want := range tt.wantCogs {
					if !found[want] {
						t.Errorf("Cognates missing %q, got %v", want, a.Cognates)
					}
				}
			}
		})
	}
}

func TestEditDistance_properties(t *testing.T) {
	t.Parallel()
	t.Run("non_negativity", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			a := rapid.String().Draw(t, "a")
			b := rapid.String().Draw(t, "b")
			d := EditDistance(a, b)
			if d < 0 {
				t.Fatalf("EditDistance(%q, %q) = %d, want >= 0", a, b, d)
			}
		})
	})
	t.Run("identity", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			s := rapid.String().Draw(t, "s")
			d := EditDistance(s, s)
			if d != 0 {
				t.Fatalf("EditDistance(%q, %q) = %d, want 0", s, s, d)
			}
		})
	})
	t.Run("symmetry", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			a := rapid.String().Draw(t, "a")
			b := rapid.String().Draw(t, "b")
			ab := EditDistance(a, b)
			ba := EditDistance(b, a)
			if ab != ba {
				t.Fatalf("EditDistance(%q,%q)=%d != EditDistance(%q,%q)=%d",
					a, b, ab, b, a, ba)
			}
		})
	})
	t.Run("triangle_inequality", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			a := rapid.String().Draw(t, "a")
			b := rapid.String().Draw(t, "b")
			c := rapid.String().Draw(t, "c")
			ab := EditDistance(a, b)
			bc := EditDistance(b, c)
			ac := EditDistance(a, c)
			if ac > ab+bc {
				t.Fatalf("triangle inequality: d(%q,%q)=%d > d(%q,%q)=%d + d(%q,%q)=%d",
					a, c, ac, a, b, ab, b, c, bc)
			}
		})
	})
}
