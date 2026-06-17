package cliparse

import (
	"bytes"
	"strings"
	"testing"
)

// Tests added to kill surviving gremlins mutants in cliparse.go (unit
// subflux-u13). Internal test package so unexported helpers (flagSignature,
// checkType, suggestion, editDistance, minInt) are reachable. All identifiers
// defined here are prefixed gk_subflux_u13_ to avoid colliding with a sibling
// unit that may share this package.

// gk_subflux_u13_renderHelp renders s via PrintHelp and returns the text.
func gk_subflux_u13_renderHelp(t *testing.T, s *Spec) string {
	t.Helper()
	var buf bytes.Buffer
	PrintHelp(&buf, s)
	return buf.String()
}

// gk_subflux_u13_spec returns a spec with several flags, none required, so
// Validate reaches the unknown-flag / type-check paths without tripping the
// required-flag guard.
func gk_subflux_u13_spec() *Spec {
	return &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "host", Type: "string"},
			{Name: "lang", Type: "string"},
			{Name: "port", Type: "int"},
		},
	}
}

// cliparse.go:55:12 CONDITIONALS_NEGATION — `if s.Args != ""`.
// With Args set the usage line must carry the Args suffix; the `==` mutant
// would drop it.
func Test_gk_subflux_u13_PrintHelp_includesArgsSuffix(t *testing.T) {
	out := gk_subflux_u13_renderHelp(t, &Spec{Name: "search", Args: "<imdb-id>"})
	if !strings.Contains(out, "Usage: subflux search [flags] <imdb-id>") {
		t.Errorf("PrintHelp(Args=<imdb-id>) = %q, want usage line ending in the Args suffix", out)
	}
}

// cliparse.go:59:12 CONDITIONALS_NEGATION — `if s.Help != ""`.
// With Help set the body must be printed; the `==` mutant would omit it.
func Test_gk_subflux_u13_PrintHelp_includesHelpBody(t *testing.T) {
	out := gk_subflux_u13_renderHelp(t, &Spec{Name: "search", Help: "Find subtitles by id."})
	if !strings.Contains(out, "Find subtitles by id.") {
		t.Errorf("PrintHelp(Help set) = %q, want it to include the help body", out)
	}
}

// cliparse.go:63:18 CONDITIONALS_NEGATION — `if len(s.Flags) == 0 { return }`.
// With zero flags the "Flags:" section must NOT be printed; the `!=` mutant
// would emit it for an empty flag list.
func Test_gk_subflux_u13_PrintHelp_omitsFlagsSectionWhenNoFlags(t *testing.T) {
	out := gk_subflux_u13_renderHelp(t, &Spec{Name: "health"})
	if strings.Contains(out, "Flags:") {
		t.Errorf("PrintHelp(no flags) = %q, want no \"Flags:\" section", out)
	}
}

// cliparse.go:70:36 CONDITIONALS_NEGATION — `if n > maxSig`.
// maxSig must equal the widest signature so shorter flags are padded for
// column alignment. The `<=` mutant leaves maxSig at 0 (no padding).
func Test_gk_subflux_u13_PrintHelp_alignsFlagSignatures(t *testing.T) {
	out := gk_subflux_u13_renderHelp(t, &Spec{
		Name: "demo",
		Flags: []Flag{
			{Name: "x", Help: "the x flag"},
			{Name: "timeout", Type: "duration", Help: "wait time"},
		},
	})
	// len("--timeout <duration>") == 20, so "--x" (len 3) is padded to 20 then
	// followed by the 2-space separator: 19 spaces precede the description.
	want := "  --x" + strings.Repeat(" ", 19) + "the x flag"
	if !strings.Contains(out, want) {
		t.Errorf("PrintHelp alignment = %q, want short flag padded to max signature width (%q)", out, want)
	}
}

// cliparse.go:76:16 CONDITIONALS_NEGATION — `if f.Default != ""`.
// A flag with a default must render "(default: X)"; the `==` mutant would
// skip the annotation for a non-empty default.
func Test_gk_subflux_u13_PrintHelp_includesDefaultAnnotation(t *testing.T) {
	out := gk_subflux_u13_renderHelp(t, &Spec{
		Name:  "demo",
		Flags: []Flag{{Name: "port", Type: "int", Default: "8374", Help: "server port"}},
	})
	if !strings.Contains(out, "(default: 8374)") {
		t.Errorf("PrintHelp(Default=8374) = %q, want it to include \"(default: 8374)\"", out)
	}
}

// cliparse.go:87:12 and 87:28 CONDITIONALS_NEGATION —
// `if f.Type == "" || f.Type == "bool"`. Empty/bool types render "--name";
// other types render "--name <type>". The `!=` mutants flip these.
func Test_gk_subflux_u13_flagSignature_typeRendering(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{"", "--name"},
		{"bool", "--name"},
		{"int", "--name <int>"},
		{"string", "--name <string>"},
		{"duration", "--name <duration>"},
	}
	for _, c := range cases {
		got := flagSignature(Flag{Name: "name", Type: c.typ})
		if got != c.want {
			t.Errorf("flagSignature(Type=%q) = %q, want %q", c.typ, got, c.want)
		}
	}
}

// cliparse.go:130:8 CONDITIONALS_NEGATION — `if a == "--help" || a == "-h"`.
// A "--help" token must be skipped (treated as help, not an unknown flag).
// The `!=` mutant on the first comparison processes it and errors.
func Test_gk_subflux_u13_Validate_skipsHelpTokens(t *testing.T) {
	if err := Validate([]string{"--help"}, map[string]string{}, gk_subflux_u13_spec()); err != nil {
		t.Errorf("Validate([--help]) = %v, want nil (help token must be skipped, not treated as unknown)", err)
	}
}

// cliparse.go:130:8 and 130:25 CONDITIONALS_NEGATION — an unknown --flag must
// be rejected. Either negation makes the loop skip the unknown flag and
// return nil.
func Test_gk_subflux_u13_Validate_rejectsUnknownFlag(t *testing.T) {
	err := Validate([]string{"--unknownxyz"}, map[string]string{}, gk_subflux_u13_spec())
	if err == nil {
		t.Fatalf("Validate([--unknownxyz]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag --unknownxyz") {
		t.Errorf("Validate([--unknownxyz]) error = %q, want it to mention the unknown flag", err.Error())
	}
}

// cliparse.go:138:45 CONDITIONALS_BOUNDARY — `if eq := IndexByte(...); eq >= 0`.
// "--=foo" yields name "=foo" with '=' at index 0; `>= 0` strips it to ""
// which is then skipped. The `> 0` mutant leaves "=foo" and errors.
func Test_gk_subflux_u13_Validate_stripsEqualsValueForm(t *testing.T) {
	if err := Validate([]string{"--=foo"}, map[string]string{}, gk_subflux_u13_spec()); err != nil {
		t.Errorf("Validate([--=foo]) = %v, want nil ('=' at index 0 must still be stripped)", err)
	}
}

// cliparse.go:141:11 CONDITIONALS_NEGATION — `if name == "" { continue }`.
// Bare "--" yields an empty name that must be skipped; the `!=` mutant
// processes the empty name and errors.
func Test_gk_subflux_u13_Validate_skipsBareDashDash(t *testing.T) {
	if err := Validate([]string{"--"}, map[string]string{}, gk_subflux_u13_spec()); err != nil {
		t.Errorf("Validate([--]) = %v, want nil (bare \"--\" yields empty name and must be skipped)", err)
	}
}

// cliparse.go:153:47 CONDITIONALS_NEGATION — `if msg := suggestion(...); msg != ""`.
// A near-miss flag must produce a "did you mean" suggestion; the `==` mutant
// drops the suggestion text.
func Test_gk_subflux_u13_Validate_suggestsCloseFlag(t *testing.T) {
	err := Validate([]string{"--hostt"}, map[string]string{}, gk_subflux_u13_spec())
	if err == nil {
		t.Fatalf("Validate([--hostt]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "did you mean --host?") {
		t.Errorf("Validate([--hostt]) error = %q, want a \"did you mean --host?\" suggestion", err.Error())
	}
}

// cliparse.go:166:47 CONDITIONALS_NEGATION — `if err := checkType(...); err != nil`.
// A type error from checkType must propagate; the `== nil` mutant swallows it
// and returns nil.
func Test_gk_subflux_u13_Validate_rejectsBadTypedValue(t *testing.T) {
	err := Validate([]string{"--port", "abc"}, map[string]string{"port": "abc"}, gk_subflux_u13_spec())
	if err == nil {
		t.Fatalf("Validate(port=abc) = nil, want a type error for non-integer --port")
	}
	if !strings.Contains(err.Error(), "is not a valid integer") {
		t.Errorf("Validate(port=abc) error = %q, want an integer-validation error", err.Error())
	}
}

// cliparse.go:176:41 CONDITIONALS_NEGATION — `if _, err := Atoi(value); err != nil`.
// Valid ints pass, invalid ints error. The `== nil` mutant inverts both.
func Test_gk_subflux_u13_checkType_intValidation(t *testing.T) {
	if err := checkType(Flag{Name: "n", Type: "int"}, "42"); err != nil {
		t.Errorf("checkType(int, \"42\") = %v, want nil", err)
	}
	if err := checkType(Flag{Name: "n", Type: "int"}, "abc"); err == nil {
		t.Errorf("checkType(int, \"abc\") = nil, want an integer-validation error")
	}
}

// cliparse.go:195:14 INVERT_NEGATIVES + ARITHMETIC_BASE (best.dist = -1) and
// cliparse.go:205:15 CONDITIONALS_NEGATION + 205:18 INVERT/ARITHMETIC
// (`if best.dist == -1`). The -1 sentinel and its check must resolve to the
// closest flag's name; any mutation either returns "" or a name-less
// suggestion.
func Test_gk_subflux_u13_suggestion_returnsClosestName(t *testing.T) {
	flags := []Flag{{Name: "host"}}
	got := suggestion("hostt", flags)
	want := " (did you mean --host?)"
	if got != want {
		t.Errorf("suggestion(\"hostt\", [host]) = %q, want %q", got, want)
	}
}

// cliparse.go:198:8 CONDITIONALS_NEGATION + CONDITIONALS_BOUNDARY — `if d > 2`.
// A candidate at edit distance exactly 2 must be accepted ("ho" -> "host").
// `d <= 2` (negation) and `d >= 2` (boundary) both drop it.
func Test_gk_subflux_u13_suggestion_distanceTwoBoundary(t *testing.T) {
	flags := []Flag{{Name: "host"}}
	got := suggestion("ho", flags) // editDistance("ho","host") == 2
	want := " (did you mean --host?)"
	if got != want {
		t.Errorf("suggestion(\"ho\", [host]) = %q, want %q (distance 2 must be accepted)", got, want)
	}
}

// cliparse.go:205:15 / 205:18 — when no flag is within distance 2, the sentinel
// path must return "". Guards the no-candidate direction of the sentinel
// mutations, which would instead return a name-less suggestion.
func Test_gk_subflux_u13_suggestion_emptyWhenNoneClose(t *testing.T) {
	flags := []Flag{{Name: "host"}}
	if got := suggestion("zzzzzz", flags); got != "" {
		t.Errorf("suggestion(\"zzzzzz\", [host]) = %q, want \"\" (no flag within edit distance 2)", got)
	}
}

// cliparse.go:233:14 CONDITIONALS_NEGATION — `if a[i-1] == b[j-1] { cost = 0 }`.
// The match cost must be 0 on equal chars; the `!=` mutant inverts it and
// inflates distances ("abc","abd" goes from 1 to 2).
func Test_gk_subflux_u13_editDistance_exactValues(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"abc", "abd", 1},
		{"host", "hostt", 1},
		{"ho", "host", 2}, // distance exactly 2: pins the 198:8 BOUNDARY kill
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// cliparse.go:246:8 CONDITIONALS_NEGATION — `if v < m { m = v }`.
// minInt must return the minimum; the `>=` mutant returns (roughly) the
// maximum instead.
func Test_gk_subflux_u13_minInt_returnsMinimum(t *testing.T) {
	cases := []struct {
		in   []int
		want int
	}{
		{[]int{3, 1, 2}, 1},
		{[]int{5, 9, 4, 7}, 4},
		{[]int{2, 2, 8}, 2},
	}
	for _, c := range cases {
		if got := minInt(c.in...); got != c.want {
			t.Errorf("minInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
