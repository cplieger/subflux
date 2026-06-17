package confighandlers

// Round-2 mutant-killing tests for internal/server/confighandlers.
//
// StripYAMLComment strips an inline " #" comment that follows a quoted value.
//
//	50:17 ARITHMETIC_BASE   (`rest := val[ci+1:]` -> `val[ci-1:]`): the slice
//	  used to locate the comment starts at the wrong offset, so the computed cut
//	  point shifts and trailing comment bytes leak into the result.
//	51:50 CONDITIONALS_BOUNDARY (`if idx >= 0` -> `idx > 0`): when the comment
//	  sits immediately after the closing quote (idx==0), the `> 0` mutant fails
//	  to strip it.

import "testing"

func TestGkSubfluxR2_StripYAMLCommentAfterQuote(t *testing.T) {
	// Comment separated from the closing quote by a space: idx of " #" in the
	// post-quote remainder is 0 for the original (`val[ci+1:]`), nonzero for the
	// `val[ci-1:]` mutant -> the cut point differs.
	got := string(StripYAMLComment([]byte(`"abc" # x`)))
	if got != `"abc"` {
		t.Errorf("StripYAMLComment(`\"abc\" # x`) = %q, want %q", got, `"abc"`)
	}
}

func TestGkSubfluxR2_StripYAMLCommentImmediatelyAfterQuote(t *testing.T) {
	// " #" begins at offset 0 of the post-quote remainder, so the `idx >= 0`
	// guard must be inclusive of 0; the `idx > 0` mutant leaves the comment in.
	got := string(StripYAMLComment([]byte(`"abc" #x`)))
	if got != `"abc"` {
		t.Errorf("StripYAMLComment(`\"abc\" #x`) = %q, want %q", got, `"abc"`)
	}
}
