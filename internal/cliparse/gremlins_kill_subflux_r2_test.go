package cliparse

// Round-2 mutant-killing tests for internal/cliparse.
//
// Kills the "closest match" comparison mutants in suggestion() and
// SuggestName():
//
//	201:27 / 276:26 CONDITIONALS_BOUNDARY (`d < best` -> `d <= best`): on a
//	  distance tie the original keeps the FIRST candidate; `<=` keeps the LAST.
//	201:27 / 276:26 CONDITIONALS_NEGATION (`d < best` -> `d >= best`): the
//	  original keeps the CLOSEST candidate; `>=` keeps the farthest seen.
//
// (The hand-rolled max at line 70 and the minInt clamp at line 246 were turned
// into the Go builtins max/min in this round, deleting those comparison
// mutants outright.)

import "testing"

func TestGkSubfluxR2_SuggestNameTiePicksFirst(t *testing.T) {
	// "zoo" is edit distance 1 from both "foo" and "goo" (a tie). The original
	// `d < best` keeps the first ("foo"); the `<=` boundary mutant keeps "goo".
	got, ok := SuggestName("zoo", []string{"foo", "goo"})
	if !ok || got != "foo" {
		t.Errorf("SuggestName tie = (%q, %v), want (\"foo\", true)", got, ok)
	}
}

func TestGkSubfluxR2_SuggestNamePicksClosest(t *testing.T) {
	// "zoo"->"zoa" is distance 1, "zoo"->"zaa" is distance 2 (both within 2).
	// The original `d < best` keeps the closest ("zoa"); the `>=` negation
	// mutant ends on the farther "zaa".
	got, ok := SuggestName("zoo", []string{"zoa", "zaa"})
	if !ok || got != "zoa" {
		t.Errorf("SuggestName closest = (%q, %v), want (\"zoa\", true)", got, ok)
	}
}

func TestGkSubfluxR2_SuggestionTiePicksFirst(t *testing.T) {
	flags := []Flag{{Name: "foo"}, {Name: "goo"}}
	got := suggestion("zoo", flags)
	if want := " (did you mean --foo?)"; got != want {
		t.Errorf("suggestion tie = %q, want %q", got, want)
	}
}

func TestGkSubfluxR2_SuggestionPicksClosest(t *testing.T) {
	flags := []Flag{{Name: "zoa"}, {Name: "zaa"}}
	got := suggestion("zoo", flags)
	if want := " (did you mean --zoa?)"; got != want {
		t.Errorf("suggestion closest = %q, want %q", got, want)
	}
}
