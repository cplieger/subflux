package cliparse

import (
	"bytes"
	"strings"
	"testing"
)

// renderHelp renders s via PrintHelp and returns the produced text.
func renderHelp(t *testing.T, s *Spec) string {
	t.Helper()
	var buf bytes.Buffer
	PrintHelp(&buf, s)
	return buf.String()
}

// validateTestSpec returns a spec with several flags, none required, so
// Validate reaches the unknown-flag and type-check paths without tripping
// the required-flag guard.
func validateTestSpec() *Spec {
	return &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "host", Type: "string"},
			{Name: "lang", Type: "string"},
			{Name: "port", Type: "int"},
		},
	}
}

// --- PrintHelp ---

// With Args set, the usage line must carry the Args suffix.
func TestPrintHelp_includes_args_suffix(t *testing.T) {
	out := renderHelp(t, &Spec{Name: "search", Args: "<imdb-id>"})
	if !strings.Contains(out, "Usage: subflux search [flags] <imdb-id>") {
		t.Errorf("PrintHelp(Args=<imdb-id>) = %q, want usage line ending in the Args suffix", out)
	}
}

// With Help set, the description body must be printed.
func TestPrintHelp_includes_help_body(t *testing.T) {
	out := renderHelp(t, &Spec{Name: "search", Help: "Find subtitles by id."})
	if !strings.Contains(out, "Find subtitles by id.") {
		t.Errorf("PrintHelp(Help set) = %q, want it to include the help body", out)
	}
}

// With zero flags, the "Flags:" section must not be printed.
func TestPrintHelp_omits_flags_section_when_no_flags(t *testing.T) {
	out := renderHelp(t, &Spec{Name: "health"})
	if strings.Contains(out, "Flags:") {
		t.Errorf("PrintHelp(no flags) = %q, want no \"Flags:\" section", out)
	}
}

// Shorter flag signatures must be padded to the widest signature so the
// descriptions line up in a column.
func TestPrintHelp_aligns_flag_signatures(t *testing.T) {
	out := renderHelp(t, &Spec{
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

// A flag with a default must render a "(default: X)" annotation.
func TestPrintHelp_includes_default_annotation(t *testing.T) {
	out := renderHelp(t, &Spec{
		Name:  "demo",
		Flags: []Flag{{Name: "port", Type: "int", Default: "8374", Help: "server port"}},
	})
	if !strings.Contains(out, "(default: 8374)") {
		t.Errorf("PrintHelp(Default=8374) = %q, want it to include \"(default: 8374)\"", out)
	}
}

// --- flagSignature ---

// Empty and bool types render "--name"; other types render "--name <type>".
func TestFlagSignature_type_rendering(t *testing.T) {
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

// --- Validate ---

// A "--help" token is treated as help, not an unknown flag.
func TestValidate_skips_help_tokens(t *testing.T) {
	if err := Validate([]string{"--help"}, map[string]string{}, validateTestSpec()); err != nil {
		t.Errorf("Validate([--help]) = %v, want nil (help token must be skipped, not treated as unknown)", err)
	}
}

// An unknown --flag must be rejected with a message naming it.
func TestValidate_rejects_unknown_flag(t *testing.T) {
	err := Validate([]string{"--unknownxyz"}, map[string]string{}, validateTestSpec())
	if err == nil {
		t.Fatalf("Validate([--unknownxyz]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag --unknownxyz") {
		t.Errorf("Validate([--unknownxyz]) error = %q, want it to mention the unknown flag", err.Error())
	}
}

// The --flag=value form is reduced to the flag name before the unknown-flag
// check, so "--=foo" (empty name after stripping) is skipped, not rejected.
func TestValidate_strips_equals_value_form(t *testing.T) {
	if err := Validate([]string{"--=foo"}, map[string]string{}, validateTestSpec()); err != nil {
		t.Errorf("Validate([--=foo]) = %v, want nil ('=' at index 0 must still be stripped)", err)
	}
}

// A bare "--" yields an empty flag name and must be skipped.
func TestValidate_skips_bare_dash_dash(t *testing.T) {
	if err := Validate([]string{"--"}, map[string]string{}, validateTestSpec()); err != nil {
		t.Errorf("Validate([--]) = %v, want nil (bare \"--\" yields empty name and must be skipped)", err)
	}
}

// A near-miss flag produces a "did you mean" suggestion.
func TestValidate_suggests_close_flag(t *testing.T) {
	err := Validate([]string{"--hostt"}, map[string]string{}, validateTestSpec())
	if err == nil {
		t.Fatalf("Validate([--hostt]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "did you mean --host?") {
		t.Errorf("Validate([--hostt]) error = %q, want a \"did you mean --host?\" suggestion", err.Error())
	}
}

// A type error from a typed flag must propagate out of Validate.
func TestValidate_rejects_bad_typed_value(t *testing.T) {
	err := Validate([]string{"--port", "abc"}, map[string]string{"port": "abc"}, validateTestSpec())
	if err == nil {
		t.Fatalf("Validate(port=abc) = nil, want a type error for non-integer --port")
	}
	if !strings.Contains(err.Error(), "is not a valid integer") {
		t.Errorf("Validate(port=abc) error = %q, want an integer-validation error", err.Error())
	}
}

// A required flag that is absent is rejected; supplying it succeeds.
func TestValidate_enforces_required_flag(t *testing.T) {
	spec := &Spec{
		Name:  "search",
		Flags: []Flag{{Name: "imdb", Type: "string", Required: true}},
	}
	err := Validate(nil, map[string]string{}, spec)
	if err == nil {
		t.Fatalf("Validate(missing required --imdb) = nil, want a required-flag error")
	}
	if !strings.Contains(err.Error(), "--imdb is required") {
		t.Errorf("Validate(missing required --imdb) error = %q, want \"--imdb is required\"", err.Error())
	}
	if err := Validate([]string{"--imdb", "tt1"}, map[string]string{"imdb": "tt1"}, spec); err != nil {
		t.Errorf("Validate(required --imdb present) = %v, want nil", err)
	}
}

// --- checkType ---

// int and duration values are validated; string/bool/untyped flags are not.
func TestCheckType_validates_typed_values(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		value   string
		wantErr bool
	}{
		{"valid int", "int", "42", false},
		{"invalid int", "int", "abc", true},
		{"negative int", "int", "-7", false},
		{"valid duration", "duration", "30s", false},
		{"invalid duration", "duration", "soon", true},
		{"string not validated", "string", "anything", false},
		{"bool not validated", "bool", "notabool", false},
		{"untyped not validated", "", "whatever", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkType(Flag{Name: "n", Type: c.typ}, c.value)
			if (err != nil) != c.wantErr {
				t.Errorf("checkType(%q, %q) error = %v, wantErr %v", c.typ, c.value, err, c.wantErr)
			}
		})
	}
}

// --- suggestion ---

// suggestion returns the closest flag within edit distance 2, preferring the
// first on a tie, and "" when nothing is close enough.
func TestSuggestion(t *testing.T) {
	cases := []struct {
		name  string
		input string
		flags []Flag
		want  string
	}{
		{"near miss", "hostt", []Flag{{Name: "host"}}, " (did you mean --host?)"},
		{"tie picks first", "zoo", []Flag{{Name: "foo"}, {Name: "goo"}}, " (did you mean --foo?)"},
		{"picks closest", "zoo", []Flag{{Name: "zoa"}, {Name: "zaa"}}, " (did you mean --zoa?)"},
		{"distance two accepted", "ho", []Flag{{Name: "host"}}, " (did you mean --host?)"},
		{"none within distance two", "zzzzzz", []Flag{{Name: "host"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := suggestion(c.input, c.flags); got != c.want {
				t.Errorf("suggestion(%q, %v) = %q, want %q", c.input, c.flags, got, c.want)
			}
		})
	}
}

// --- editDistance ---

// editDistance returns the exact Levenshtein distance for representative pairs.
func TestEditDistance_exact_values(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"abc", "abd", 1},
		{"host", "hostt", 1},
		{"ho", "host", 2},
		{"kitten", "sitting", 3},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// --- minInt ---

// minInt returns the smallest of its arguments.
func TestMinInt_returns_minimum(t *testing.T) {
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

// --- SuggestName ---

// SuggestName returns the closest candidate within edit distance 2 (first on a
// tie), or ("", false) when none is close enough.
func TestSuggestName(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		candidates []string
		wantName   string
		wantOK     bool
	}{
		{"no candidate within distance two", "xxxxxxxx", []string{"search", "scan"}, "", false},
		{"exact match", "search", []string{"search", "scan"}, "search", true},
		{"distance one match", "searc", []string{"search"}, "search", true},
		{"distance two boundary accepted", "sear", []string{"search"}, "search", true},
		{"distance three rejected", "sea", []string{"search"}, "", false},
		{"tie picks first", "zoo", []string{"foo", "goo"}, "foo", true},
		{"picks closest", "zoo", []string{"zoa", "zaa"}, "zoa", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotOK := SuggestName(c.input, c.candidates)
			if gotName != c.wantName || gotOK != c.wantOK {
				t.Errorf("SuggestName(%q, %v) = (%q, %t), want (%q, %t)",
					c.input, c.candidates, gotName, gotOK, c.wantName, c.wantOK)
			}
		})
	}
}
