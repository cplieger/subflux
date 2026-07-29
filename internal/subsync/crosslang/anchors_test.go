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
			a := extractAnchors(tt.input)
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
	a := extractAnchors("Hello. World")
	if len(a.ProperNouns) != 0 {
		t.Errorf("extractAnchors(%q).ProperNouns = %v, want []", "Hello. World", a.ProperNouns)
	}
}

func TestExtractAnchors_twoCharProperNoun(t *testing.T) {
	t.Parallel()
	// A capitalized two-rune word mid-sentence is the shortest accepted proper
	// noun; the classifier ignores words under two runes.
	a := extractAnchors("go Bo")
	if len(a.ProperNouns) != 1 || a.ProperNouns[0] != "Bo" {
		t.Errorf("extractAnchors(%q).ProperNouns = %v, want [Bo]", "go Bo", a.ProperNouns)
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
		{"empty", "", false},
		{"latin_simple", "hello", true},
		{"latin_accented", "télévision", true},
		{"latin_extended", "naïve", true},
		{"cyrillic", "Москва", false},
		{"cjk", "東京", false},
		{"mixed_latin_cyrillic", "helloМир", false},
		{"digits_only", "1234", false},
		{"latin_with_digit", "abc1", false},
		{"single_latin", "a", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isLatinWord(tt.in); got != tt.want {
				t.Errorf("isLatinWord(%q) = %v, want %v", tt.in, got, tt.want)
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
		{"identical", "television", "television", true},
		{"accent_difference", "television", "télévision", true},
		{"accent_short", "president", "président", true},
		{"identical_medium", "ridicule", "ridicule", true},
		{"identical_costume", "costume", "costume", true},
		{"suffix_cognate", "feministe", "feminist", true},
		{"longer_suffix", "charismatique", "charismatic", true},
		{"hyphenated", "hot-dog", "hot-dog", true},
		{"proper_noun_4", "Pete", "Pete", true},
		{"proper_noun_5", "Lemon", "Lemon", true},
		{"short_cognate", "cat", "chat", true},
		{"too_short", "cat", "bat", false},
		{"too_short_different", "the", "le", false},
		{"acronym_too_short", "NBC", "NBC", false},
		{"completely_different", "house", "maison", false},
		{"length_ratio_below_half", "ab", "abcdef", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCognate(tt.a, tt.b); got != tt.want {
				t.Errorf("isCognate(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
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
		{"both_empty", nil, nil, 0},
		{"a_empty", nil, []string{"hello"}, 0},
		{"b_empty", []string{"hello"}, nil, 0},
		{"exact_match_real_word", []string{"police"}, []string{"police"}, 1},
		{"cognate_pair", []string{"television"}, []string{"télévision"}, 1},
		{"no_match", []string{"house"}, []string{"maison"}, 0},
		{"one_to_one_consumption", []string{"police", "police"}, []string{"police"}, 1},
		{"multiple_matches", []string{"television", "president"}, []string{"télévision", "président"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := countCognates(tt.a, tt.b); got != tt.want {
				t.Errorf("countCognates(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
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
		{"both_empty", nil, nil, 0},
		{"a_empty", nil, []string{"X"}, 0},
		{"b_empty", []string{"X"}, nil, 0},
		{"exact_case_match", []string{"John"}, []string{"John"}, 1},
		{"multiple_matches", []string{"Paris", "John"}, []string{"paris", "john"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := countSharedFold(tt.a, tt.b); got != tt.want {
				t.Errorf("countSharedFold(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// The tests below were promoted from internal/subsync/anchors_test.go, which
// reached these functions through exported one-line wrappers
// (crosslang.ExtractAnchors, .EditDistance, .CountShared, ...) that had no
// production caller. The wrappers are gone; the assertions live here, against
// the same functions the production Align path calls.

// TestExtractAnchors_commaIsNotTerminalPunctuation pins that a trailing comma
// is not a sentence terminator: the anchor's Punctuation feature only carries
// . ? ! and the ellipsis, so a comma-ended cue contributes no punctuation
// signal to the cross-language match score.
func TestExtractAnchors_commaIsNotTerminalPunctuation(t *testing.T) {
	t.Parallel()
	if a := extractAnchors("Hello,"); a.Punctuation != "" {
		t.Errorf("extractAnchors(%q).Punctuation = %q, want empty", "Hello,", a.Punctuation)
	}
}

func TestEditDistance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"identical", "hello", "hello", 0},
		{"empty_vs_nonempty", "", "abc", 3},
		{"nonempty_vs_empty", "abc", "", 3},
		{"both_empty", "", "", 0},
		{"single_substitution", "cat", "bat", 1},
		{"single_insertion", "cat", "cart", 1},
		{"single_deletion", "cart", "cat", 1},
		{"completely_different", "abc", "xyz", 3},
		{"unicode_accents", "television", "télévision", 2},
		{"different_lengths", "short", "shorter", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := editDistance(tt.a, tt.b); got != tt.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCountShared(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"both_empty", nil, nil, 0},
		{"a_empty", nil, []string{"x"}, 0},
		{"b_empty", []string{"x"}, nil, 0},
		{"exact_match_single", []string{"x"}, []string{"x"}, 1},
		{"no_match", []string{"x"}, []string{"y"}, 0},
		{"duplicate_in_a_single_in_b", []string{"x", "x"}, []string{"x"}, 1},
		{"duplicate_in_both", []string{"x", "x"}, []string{"x", "x"}, 2},
		{"partial_overlap", []string{"a", "b", "c"}, []string{"b", "c", "d"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := countShared(tt.a, tt.b); got != tt.want {
				t.Errorf("countShared(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
