package subsync

import (
	"testing"

	"subflux/internal/subsync/crosslang"

	"pgregory.net/rapid"
)

func TestExtractAnchors_plain_text(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("Hello world")
	if a.WordCount != 2 {
		t.Errorf("crosslang.ExtractAnchors(plain).WordCount = %d, want 2", a.WordCount)
	}
	if a.CharLen != 11 {
		t.Errorf("crosslang.ExtractAnchors(plain).CharLen = %d, want 11", a.CharLen)
	}
	if a.Punctuation != "" {
		t.Errorf("crosslang.ExtractAnchors(plain).Punctuation = %q, want empty", a.Punctuation)
	}
}

func TestExtractAnchors_with_numbers(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("There are 150 people and 57 dogs")
	if len(a.Numbers) != 2 {
		t.Fatalf("crosslang.ExtractAnchors(numbers).Numbers = %v, want 2 entries", a.Numbers)
	}
	if a.Numbers[0] != "150" || a.Numbers[1] != "57" {
		t.Errorf("crosslang.ExtractAnchors(numbers).Numbers = %v, want [150 57]", a.Numbers)
	}
}

func TestExtractAnchors_punctuation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, wantPunct string
	}{
		{"question", "Are you sure?", "?"},
		{"exclamation", "Watch out!", "!"},
		{"period", "I see.", "."},
		{"ellipsis", "Well...", "..."},
		{"unicode_ellipsis", "Well\u2026", "..."},
		{"no punctuation", "Hello world", ""},
		{"comma ending", "Hello,", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := crosslang.ExtractAnchors(tt.input)
			if a.Punctuation != tt.wantPunct {
				t.Errorf("crosslang.ExtractAnchors(%q).Punctuation = %q, want %q",
					tt.input, a.Punctuation, tt.wantPunct)
			}
		})
	}
}

func TestExtractAnchors_proper_nouns(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("I saw John in Paris yesterday")
	// "I" is at sentence start, "John" and "Paris" are mid-sentence capitals.
	found := map[string]bool{}
	for _, n := range a.ProperNouns {
		found[n] = true
	}
	if !found["John"] {
		t.Errorf("crosslang.ExtractAnchors(proper nouns) missing 'John', got %v", a.ProperNouns)
	}
	if !found["Paris"] {
		t.Errorf("crosslang.ExtractAnchors(proper nouns) missing 'Paris', got %v", a.ProperNouns)
	}
}

func TestExtractAnchors_cognates(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("The television president arrived")
	found := map[string]bool{}
	for _, c := range a.Cognates {
		found[c] = true
	}
	if !found["television"] {
		t.Errorf("crosslang.ExtractAnchors(cognates) missing 'television', got %v", a.Cognates)
	}
	if !found["president"] {
		t.Errorf("crosslang.ExtractAnchors(cognates) missing 'president', got %v", a.Cognates)
	}
	if !found["arrived"] {
		t.Errorf("crosslang.ExtractAnchors(cognates) missing 'arrived', got %v", a.Cognates)
	}
}

func TestExtractAnchors_strips_tags(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("<i>Hello</i> world")
	if a.CharLen != 11 {
		t.Errorf("crosslang.ExtractAnchors(tags).CharLen = %d, want 11", a.CharLen)
	}
}

func TestExtractAnchors_empty(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("")
	if a.WordCount != 0 || a.CharLen != 0 {
		t.Errorf("crosslang.ExtractAnchors(\"\") = {WordCount:%d, CharLen:%d}, want zeros",
			a.WordCount, a.CharLen)
	}
}

func TestExtractAnchors_non_latin_excluded_from_cognates(t *testing.T) {
	t.Parallel()
	// CJK characters are not Latin, so words containing them should not
	// appear as cognate candidates even if they are ≥4 runes.
	a := crosslang.ExtractAnchors("The 東京タワー is tall")
	for _, c := range a.Cognates {
		if c == "東京タワー" {
			t.Errorf("crosslang.ExtractAnchors(CJK).Cognates should not contain CJK word, got %v", a.Cognates)
		}
	}
	// "tall" is only 4 chars and Latin, so it should be a cognate candidate.
	found := false
	for _, c := range a.Cognates {
		if c == "tall" {
			found = true
		}
	}
	if !found {
		t.Errorf("crosslang.ExtractAnchors(CJK).Cognates should contain 'tall', got %v", a.Cognates)
	}
}

func TestExtractAnchors_cyrillic_excluded_from_cognates(t *testing.T) {
	t.Parallel()
	// Cyrillic words (> U+024F, not Latin) should be excluded from cognates.
	a := crosslang.ExtractAnchors("Москва is beautiful")
	for _, c := range a.Cognates {
		if c == "москва" {
			t.Errorf("crosslang.ExtractAnchors(Cyrillic).Cognates should not contain Cyrillic word, got %v", a.Cognates)
		}
	}
	found := false
	for _, c := range a.Cognates {
		if c == "beautiful" {
			found = true
		}
	}
	if !found {
		t.Errorf("crosslang.ExtractAnchors(Cyrillic).Cognates should contain 'beautiful', got %v", a.Cognates)
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
			got := crosslang.EditDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("crosslang.EditDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestEditDistance_symmetry(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.String().Draw(t, "a")
		b := rapid.String().Draw(t, "b")
		ab := crosslang.EditDistance(a, b)
		ba := crosslang.EditDistance(b, a)
		if ab != ba {
			t.Errorf("crosslang.EditDistance(%q,%q)=%d != crosslang.EditDistance(%q,%q)=%d", a, b, ab, b, a, ba)
		}
	})
}

func TestEditDistance_identity(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		got := crosslang.EditDistance(s, s)
		if got != 0 {
			t.Errorf("crosslang.EditDistance(%q, %q) = %d, want 0", s, s, got)
		}
	})
}

func TestEditDistance_upper_bound(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.String().Draw(t, "a")
		b := rapid.String().Draw(t, "b")
		got := crosslang.EditDistance(a, b)
		maxLen := max(len([]rune(a)), len([]rune(b)))
		if got > maxLen {
			t.Errorf("crosslang.EditDistance(%q,%q)=%d > max(len(%d),len(%d))=%d",
				a, b, got, len([]rune(a)), len([]rune(b)), maxLen)
		}
	})
}

func TestIsLatinWord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
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
		{"greek", "αβγ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crosslang.IsLatinWord(tt.input)
			if got != tt.want {
				t.Errorf("crosslang.IsLatinWord(%q) = %v, want %v", tt.input, got, tt.want)
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
		{"exact_match_short_4", "test", "test", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crosslang.IsCognate(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("crosslang.IsCognate(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
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
		{"both_empty", nil, nil, 0},
		{"a_empty", nil, []string{"hello"}, 0},
		{"b_empty", []string{"hello"}, nil, 0},
		{"exact_match", []string{"police"}, []string{"police"}, 1},
		{"cognate_pair", []string{"television"}, []string{"télévision"}, 1},
		{"no_match", []string{"house"}, []string{"maison"}, 0},
		{"one_to_one_consumption", []string{"police", "police"}, []string{"police"}, 1},
		{"multiple_matches", []string{"television", "president"}, []string{"télévision", "président"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crosslang.CountCognates(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("crosslang.CountCognates(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestExtractAnchors_number_normalization(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("The cost is 1,500 dollars and 3.14 percent")
	wantNums := []string{"1500", "314"}
	if len(a.Numbers) != len(wantNums) {
		t.Fatalf("crosslang.ExtractAnchors(number normalization).Numbers = %v, want %v", a.Numbers, wantNums)
	}
	for i, want := range wantNums {
		if a.Numbers[i] != want {
			t.Errorf("Numbers[%d] = %q, want %q", i, a.Numbers[i], want)
		}
	}
}

func TestExtractAnchors_multiline_dialogue(t *testing.T) {
	t.Parallel()
	a := crosslang.ExtractAnchors("- Hello John!\n- How are you?")
	if a.WordCount != 5 {
		t.Errorf("crosslang.ExtractAnchors(multiline).WordCount = %d, want 5", a.WordCount)
	}
	if a.Punctuation != "?" {
		t.Errorf("crosslang.ExtractAnchors(multiline).Punctuation = %q, want %q", a.Punctuation, "?")
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
			got := crosslang.CountShared(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("crosslang.CountShared(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
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
		{"both_empty", nil, nil, 0},
		{"a_empty", nil, []string{"X"}, 0},
		{"b_empty", []string{"X"}, nil, 0},
		{"case_insensitive_match", []string{"Paris"}, []string{"paris"}, 1},
		{"exact_case_match", []string{"John"}, []string{"John"}, 1},
		{"no_match", []string{"Paris"}, []string{"London"}, 0},
		{"duplicate_consumption", []string{"Paris", "Paris"}, []string{"paris"}, 1},
		{"multiple_matches", []string{"Paris", "John"}, []string{"paris", "john"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crosslang.CountSharedFold(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("crosslang.CountSharedFold(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestExtractAnchors_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "input")
		a := crosslang.ExtractAnchors(input)
		if a.WordCount < 0 {
			t.Errorf("crosslang.ExtractAnchors(%q).WordCount = %d, want >= 0", input, a.WordCount)
		}
		if a.CharLen < 0 {
			t.Errorf("crosslang.ExtractAnchors(%q).CharLen = %d, want >= 0", input, a.CharLen)
		}
		for i, n := range a.Numbers {
			for _, r := range n {
				if r < '0' || r > '9' {
					t.Errorf("crosslang.ExtractAnchors(%q).Numbers[%d] = %q contains non-digit", input, i, n)
				}
			}
		}
	})
}

func TestIsCognate_reflexive(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "word")
		if !crosslang.IsCognate(s, s) {
			t.Errorf("crosslang.IsCognate(%q, %q) = false, want true (reflexive)", s, s)
		}
	})
}

func TestIsCognate_symmetric(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "a")
		b := rapid.StringMatching(`[a-z]{4,12}`).Draw(t, "b")
		ab := crosslang.IsCognate(a, b)
		ba := crosslang.IsCognate(b, a)
		if ab != ba {
			t.Errorf("crosslang.IsCognate(%q,%q)=%v != crosslang.IsCognate(%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}

func TestEditDistance_triangle_inequality(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.String().Draw(t, "a")
		b := rapid.String().Draw(t, "b")
		c := rapid.String().Draw(t, "c")
		ab := crosslang.EditDistance(a, b)
		bc := crosslang.EditDistance(b, c)
		ac := crosslang.EditDistance(a, c)
		if ac > ab+bc {
			t.Errorf("triangle inequality violated: crosslang.EditDistance(%q,%q)=%d > crosslang.EditDistance(%q,%q)=%d + crosslang.EditDistance(%q,%q)=%d",
				a, c, ac, a, b, ab, b, c, bc)
		}
	})
}

func TestCountShared_upper_bound(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.SliceOf(rapid.StringMatching(`[a-z]{1,5}`)).Draw(t, "a")
		b := rapid.SliceOf(rapid.StringMatching(`[a-z]{1,5}`)).Draw(t, "b")
		got := crosslang.CountShared(a, b)
		upper := min(len(a), len(b))
		if got > upper {
			t.Errorf("crosslang.CountShared(%v, %v) = %d, want <= min(%d, %d) = %d",
				a, b, got, len(a), len(b), upper)
		}
	})
}

func TestCountSharedFold_upper_bound(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.SliceOf(rapid.StringMatching(`[a-zA-Z]{1,5}`)).Draw(t, "a")
		b := rapid.SliceOf(rapid.StringMatching(`[a-zA-Z]{1,5}`)).Draw(t, "b")
		got := crosslang.CountSharedFold(a, b)
		upper := min(len(a), len(b))
		if got > upper {
			t.Errorf("crosslang.CountSharedFold(%v, %v) = %d, want <= min(%d, %d) = %d",
				a, b, got, len(a), len(b), upper)
		}
	})
}
