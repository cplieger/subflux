package cliparse

import (
	"bytes"
	"maps"
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

// --- ParseAndValidate: validation semantics ---

// A "--help" token is treated as help, not an unknown flag.
func TestParseAndValidate_skips_help_tokens(t *testing.T) {
	if _, err := ParseAndValidate([]string{"--help", "-h"}, validateTestSpec()); err != nil {
		t.Errorf("ParseAndValidate([--help -h]) = %v, want nil (help tokens must be skipped, not treated as unknown)", err)
	}
}

// An unknown --flag must be rejected with a message naming it.
func TestParseAndValidate_rejects_unknown_flag(t *testing.T) {
	_, err := ParseAndValidate([]string{"--unknownxyz", "v"}, validateTestSpec())
	if err == nil {
		t.Fatalf("ParseAndValidate([--unknownxyz]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag --unknownxyz") {
		t.Errorf("ParseAndValidate([--unknownxyz]) error = %q, want it to mention the unknown flag", err.Error())
	}
}

// "--=foo" (empty name after stripping) is skipped, not rejected.
func TestParseAndValidate_drops_empty_flag_name(t *testing.T) {
	p, err := ParseAndValidate([]string{"--=foo"}, validateTestSpec())
	if err != nil {
		t.Fatalf("ParseAndValidate([--=foo]) = %v, want nil ('=' at index 0 must still be stripped)", err)
	}
	if got := p.String(""); got != "" {
		t.Errorf("ParseAndValidate([--=foo]) stored empty key = %q, want it dropped", got)
	}
}

// A bare "--" yields an empty flag name and must be skipped.
func TestParseAndValidate_skips_bare_dash_dash(t *testing.T) {
	if _, err := ParseAndValidate([]string{"--"}, validateTestSpec()); err != nil {
		t.Errorf("ParseAndValidate([--]) = %v, want nil (bare \"--\" yields empty name and must be skipped)", err)
	}
}

// A near-miss flag produces a "did you mean" suggestion.
func TestParseAndValidate_suggests_close_flag(t *testing.T) {
	_, err := ParseAndValidate([]string{"--hostt", "v"}, validateTestSpec())
	if err == nil {
		t.Fatalf("ParseAndValidate([--hostt]) = nil, want an unknown-flag error")
	}
	if !strings.Contains(err.Error(), "did you mean --host?") {
		t.Errorf("ParseAndValidate([--hostt]) error = %q, want a \"did you mean --host?\" suggestion", err.Error())
	}
}

// A type error from a typed flag must propagate.
func TestParseAndValidate_rejects_bad_typed_value(t *testing.T) {
	_, err := ParseAndValidate([]string{"--port", "abc"}, validateTestSpec())
	if err == nil {
		t.Fatalf("ParseAndValidate(port=abc) = nil, want a type error for non-integer --port")
	}
	if !strings.Contains(err.Error(), "is not a valid integer") {
		t.Errorf("ParseAndValidate(port=abc) error = %q, want an integer-validation error", err.Error())
	}
}

// A required flag that is absent is rejected; supplying it succeeds.
func TestParseAndValidate_enforces_required_flag(t *testing.T) {
	spec := &Spec{
		Name:  "search",
		Flags: []Flag{{Name: "imdb", Type: "string", Required: true}},
	}
	_, err := ParseAndValidate(nil, spec)
	if err == nil {
		t.Fatalf("ParseAndValidate(missing required --imdb) = nil, want a required-flag error")
	}
	if !strings.Contains(err.Error(), "--imdb is required") {
		t.Errorf("ParseAndValidate(missing required --imdb) error = %q, want \"--imdb is required\"", err.Error())
	}
	if _, err := ParseAndValidate([]string{"--imdb", "tt1"}, spec); err != nil {
		t.Errorf("ParseAndValidate(required --imdb present) = %v, want nil", err)
	}
}

// A required flag whose value token is missing (trailing flag) is treated as
// unset and therefore errors — the "missing value" edge semantic.
func TestParseAndValidate_trailing_required_flag_errors(t *testing.T) {
	spec := &Spec{
		Name:  "unlock",
		Flags: []Flag{{Name: "id", Type: "string", Required: true}},
	}
	_, err := ParseAndValidate([]string{"--id"}, spec)
	if err == nil {
		t.Fatal("ParseAndValidate([--id]) = nil, want a required-flag error (trailing flag has no value)")
	}
	if !strings.Contains(err.Error(), "--id is required") {
		t.Errorf("ParseAndValidate([--id]) error = %q, want \"--id is required\"", err.Error())
	}
}

// A non-bool flag whose next token is another flag is a missing-value
// error, never a silent consumption of that flag as the value (pre-fix,
// "--lang --download" set lang to "--download" and suppressed download).
// The "--flag=--literal" form stays the escape hatch for intentional
// flag-like values.
func TestParseAndValidate_adjacent_flag_is_missing_value(t *testing.T) {
	spec := &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "imdb", Type: "string", Required: true},
			{Name: "lang", Type: "string"},
			{Name: "limit", Type: "int"},
			{Name: "download", Type: "bool"},
		},
	}
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "optional flag followed by a flag",
			args:    []string{"--imdb", "tt1", "--lang", "--download"},
			wantErr: "--lang requires a value",
		},
		{
			name:    "required flag followed by a flag",
			args:    []string{"--imdb", "--download"},
			wantErr: "--imdb requires a value",
		},
		{
			name:    "typed flag followed by a flag",
			args:    []string{"--imdb", "tt1", "--limit", "--download"},
			wantErr: "--limit requires a value",
		},
		{
			name:    "bare double dash as value is rejected too",
			args:    []string{"--imdb", "tt1", "--lang", "--"},
			wantErr: "--lang requires a value",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseAndValidate(c.args, spec)
			if err == nil {
				t.Fatalf("ParseAndValidate(%v) = nil, want %q error", c.args, c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("ParseAndValidate(%v) error = %q, want it to contain %q", c.args, err.Error(), c.wantErr)
			}
		})
	}

	// Escape hatch: the "=" form passes a flag-like value intentionally,
	// and the following bool flag keeps its own meaning.
	p, err := ParseAndValidate([]string{"--imdb", "tt1", "--lang=--download", "--download"}, spec)
	if err != nil {
		t.Fatalf("ParseAndValidate(--lang=--download) = %v, want nil (escape hatch)", err)
	}
	if got := p.String("lang"); got != "--download" {
		t.Errorf("String(lang) = %q, want %q (escape hatch value)", got, "--download")
	}
	if !p.Bool("download") {
		t.Error("Bool(download) = false, want true (the bare flag after the escape hatch)")
	}
}

// A stray positional token is rejected — subcommands take flags only.
func TestParseAndValidate_rejects_positional(t *testing.T) {
	_, err := ParseAndValidate([]string{"stray", "--lang", "fr"}, validateTestSpec())
	if err == nil {
		t.Fatal("ParseAndValidate([stray ...]) = nil, want an unexpected-argument error")
	}
	if !strings.Contains(err.Error(), `unexpected argument "stray"`) {
		t.Errorf("ParseAndValidate([stray ...]) error = %q, want it to name the stray token", err.Error())
	}
}

// A duplicated flag keeps the last value (GNU convention, matching the
// pre-consolidation parser).
func TestParseAndValidate_duplicate_flag_last_wins(t *testing.T) {
	p, err := ParseAndValidate([]string{"--lang", "fr", "--lang", "en"}, validateTestSpec())
	if err != nil {
		t.Fatalf("ParseAndValidate(duplicate --lang) = %v, want nil", err)
	}
	if got := p.String("lang"); got != "en" {
		t.Errorf("ParseAndValidate(duplicate --lang) lang = %q, want %q (last value wins)", got, "en")
	}
}

// Defaults are applied for absent flags and NOT for explicitly provided ones.
func TestParseAndValidate_applies_defaults(t *testing.T) {
	spec := &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "lang", Default: "fr"},
			{Name: "pick", Type: "int", Default: "1"},
		},
	}
	p, err := ParseAndValidate(nil, spec)
	if err != nil {
		t.Fatalf("ParseAndValidate(no args) = %v, want nil", err)
	}
	if got := p.String("lang"); got != "fr" {
		t.Errorf("String(lang) = %q, want default %q", got, "fr")
	}
	if got := p.Int("pick"); got != 1 {
		t.Errorf("Int(pick) = %d, want default 1", got)
	}

	p, err = ParseAndValidate([]string{"--lang", "en", "--pick", "3"}, spec)
	if err != nil {
		t.Fatalf("ParseAndValidate(explicit values) = %v, want nil", err)
	}
	if got := p.String("lang"); got != "en" {
		t.Errorf("String(lang) = %q, want explicit %q", got, "en")
	}
	if got := p.Int("pick"); got != 3 {
		t.Errorf("Int(pick) = %d, want explicit 3", got)
	}
}

// Bool flags are parsed from the spec table: bare form sets true, the
// "=value" form goes through strconv.ParseBool, and garbage errors. No
// value token is consumed by a bare bool flag.
func TestParseAndValidate_bool_from_spec_table(t *testing.T) {
	spec := &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "download", Type: "bool"},
			{Name: "lang"},
		},
	}
	cases := []struct {
		name     string
		args     []string
		want     bool
		wantLang string
		wantErr  bool
	}{
		{name: "absent is false", args: nil, want: false},
		{name: "bare form sets true", args: []string{"--download"}, want: true},
		{name: "bare form does not consume the next flag", args: []string{"--download", "--lang", "en"}, want: true, wantLang: "en"},
		{name: "equals true", args: []string{"--download=true"}, want: true},
		{name: "equals false", args: []string{"--download=false"}, want: false},
		{name: "equals garbage errors", args: []string{"--download=maybe"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParseAndValidate(c.args, spec)
			if (err != nil) != c.wantErr {
				t.Fatalf("ParseAndValidate(%v) error = %v, wantErr %v", c.args, err, c.wantErr)
			}
			if err != nil {
				return
			}
			if got := p.Bool("download"); got != c.want {
				t.Errorf("Bool(download) = %t, want %t", got, c.want)
			}
			if got := p.String("lang"); got != c.wantLang {
				t.Errorf("String(lang) = %q, want %q", got, c.wantLang)
			}
		})
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

// --- ParseAndValidate: token forms ---

// formsSpec declares the flags used by the token-form cases, including a
// spec-table bool so the mixed-forms case exercises generic bool parsing.
func formsSpec() *Spec {
	return &Spec{
		Name: "search",
		Flags: []Flag{
			{Name: "lang"},
			{Name: "format"},
			{Name: "title"},
			{Name: "limit", Type: "int"},
			{Name: "download", Type: "bool"},
		},
	}
}

// ParseAndValidate accepts both "--key value" and "--key=value"; the "="
// form is self-contained and must not consume the following token (pre-fix,
// "--limit=50 --lang fr" produced {"limit=50": "--lang"} and dropped fr).
func TestParseAndValidate_forms(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantParams   map[string]string
		wantDownload bool
	}{
		{
			name:       "space separated",
			args:       []string{"--lang", "fr"},
			wantParams: map[string]string{"lang": "fr"},
		},
		{
			name:       "equals form",
			args:       []string{"--format=json"},
			wantParams: map[string]string{"format": "json"},
		},
		{
			name:       "equals form does not swallow next flag",
			args:       []string{"--limit=50", "--lang", "fr"},
			wantParams: map[string]string{"limit": "50", "lang": "fr"},
		},
		{
			name:       "value containing equals splits on first",
			args:       []string{"--title=a=b"},
			wantParams: map[string]string{"title": "a=b"},
		},
		{
			name:       "equals with empty value",
			args:       []string{"--lang="},
			wantParams: map[string]string{"lang": ""},
		},
		{
			name:       "empty key is dropped",
			args:       []string{"--=foo"},
			wantParams: map[string]string{"": ""},
		},
		{
			name:         "download flag with mixed forms",
			args:         []string{"--download", "--lang=en"},
			wantParams:   map[string]string{"lang": "en"},
			wantDownload: true,
		},
		{
			name:       "trailing flag without value is ignored",
			args:       []string{"--lang"},
			wantParams: map[string]string{"lang": ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParseAndValidate(c.args, formsSpec())
			if err != nil {
				t.Fatalf("ParseAndValidate(%v) error = %v, want nil", c.args, err)
			}
			got := map[string]string{}
			for k := range c.wantParams {
				got[k] = p.String(k)
			}
			if !maps.Equal(got, c.wantParams) {
				t.Errorf("ParseAndValidate(%v) params = %v, want %v", c.args, got, c.wantParams)
			}
			if p.Bool("download") != c.wantDownload {
				t.Errorf("ParseAndValidate(%v) download = %t, want %t", c.args, p.Bool("download"), c.wantDownload)
			}
		})
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
