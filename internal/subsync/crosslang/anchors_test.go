package crosslang

import "testing"

func TestExtractAnchors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantPunct string
		wantNums  []string
		wantNouns []string
		wantCogs  []string
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
			name:     "single_digit_nine",
			input:    "9",
			wantNums: []string{"9"},
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

func TestExtractAnchors_sentenceStartIsNotProperNoun(t *testing.T) {
	t.Parallel()
	// "World" follows a sentence-terminating word, so it begins a new sentence
	// and must not be classified as a proper noun.
	a := ExtractAnchors("Hello. World")
	if len(a.ProperNouns) != 0 {
		t.Errorf("ExtractAnchors(%q).ProperNouns = %v, want []", "Hello. World", a.ProperNouns)
	}
}

func TestExtractAnchors_twoCharProperNoun(t *testing.T) {
	t.Parallel()
	// A capitalized two-rune word mid-sentence is the shortest accepted proper
	// noun; the classifier ignores words under two runes.
	a := ExtractAnchors("go Bo")
	if len(a.ProperNouns) != 1 || a.ProperNouns[0] != "Bo" {
		t.Errorf("ExtractAnchors(%q).ProperNouns = %v, want [Bo]", "go Bo", a.ProperNouns)
	}
}

func TestEndsWithSentence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"Hi.", true},
		{"Hi?", true},
		{"Hi!", true},
		{"Hi", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := endsWithSentence(tt.in); got != tt.want {
				t.Errorf("endsWithSentence(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsLatinWord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"ascii_word", "cafe", true},
		{"greek_rejected", "\u03b1\u03b2\u03b3\u03b4", false}, // αβγδ: codepoints above Latin-Extended-B
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLatinWord(tt.in); got != tt.want {
				t.Errorf("IsLatinWord(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsCognate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal_len4_cognate", "test", "test", true},
		{"equal_len3_too_short", "abc", "abc", false},
		{"distance_equals_threshold", "abcdefghij", "abcdefg000", true},
		{"distance_above_threshold", "abcdefghij", "abcde00000", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsCognate(tt.a, tt.b); got != tt.want {
				t.Errorf("IsCognate(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCountCognates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"exact_match_counts_one", []string{"test"}, []string{"test"}, 1},
		{"distinct_short_words_no_match", []string{"ab"}, []string{"cd"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CountCognates(tt.a, tt.b); got != tt.want {
				t.Errorf("CountCognates(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCountSharedFold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"case_insensitive_match", []string{"hello"}, []string{"hello"}, 1},
		{"mixed_case_match", []string{"Hello"}, []string{"hello"}, 1},
		// b has one "x" but a has two: the match budget is consumed, so only
		// one of a's duplicates can pair.
		{"budget_consumed_per_b_element", []string{"x", "x"}, []string{"x"}, 1},
		{"no_overlap", []string{"a"}, []string{"b"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CountSharedFold(tt.a, tt.b); got != tt.want {
				t.Errorf("CountSharedFold(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
