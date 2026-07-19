// Package cliparse implements subflux's flat CLI grammar: a single
// ParseAndValidate pass that splits "--key value" / "--key=value" tokens,
// rejects unknown flags (with typo suggestions) and stray positionals,
// parses bool flags from the spec table, validates typed values, enforces
// required flags, and applies spec defaults. It also renders per-command
// and root help.
//
// The design choice is deliberate: the CLI surface is auxiliary to
// subflux's HTTP/JSON API and web UI, so a heavyweight CLI framework
// (kong, cobra) would be over-engineered for ~15 flat subcommands. This
// package closes the user-visible gaps (missing --help, silent typos,
// silent type errors) in roughly one file.
package cliparse

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Spec describes one subcommand: help metadata, the declared flag surface,
// and the runner dispatch invokes with the single parse's result.
type Spec struct {
	// Run executes the subcommand with the parsed, validated params and
	// returns the process exit code. Receiving Params keeps parsing to
	// exactly one pass per invocation (a bare func() int would force
	// runners to reparse os.Args).
	Run      func(Params) int
	Name     string // e.g. "search"
	Synopsis string // one-line purpose, shown in root help
	Help     string // multi-line description for `<command> --help`
	Args     string // optional usage suffix, e.g. "<imdb-id>"
	Flags    []Flag
	// Hidden excludes the spec from root help. Internal vehicles (the
	// sync-worker child process) stay dispatchable without being
	// advertised as user commands.
	Hidden bool
}

// Params is the validated result of one ParseAndValidate pass: typed access
// to flag values with spec defaults applied. Bool flags live in their own
// set; every other flag is stored in its string form (typed flags were
// already validated as parseable).
type Params struct {
	strs  map[string]string
	bools map[string]bool
}

// String returns the value of a string-ish flag, or "" when the flag is
// absent and its spec declares no default.
func (p Params) String(name string) string { return p.strs[name] }

// Int returns the value of an int-typed flag, or 0 when the flag is absent
// and its spec declares no default. Explicit values were validated during
// the parse, so a malformed value cannot reach this accessor.
func (p Params) Int(name string) int {
	n, _ := strconv.Atoi(p.strs[name])
	return n
}

// Bool reports whether a bool-typed flag was set.
func (p Params) Bool(name string) bool { return p.bools[name] }

// Flag type vocabulary for Flag.Type. An empty Type means TypeString.
const (
	TypeString   = "string"
	TypeInt      = "int"
	TypeBool     = "bool"
	TypeDuration = "duration"
)

// Flag describes one CLI flag for a subcommand.
type Flag struct {
	Name     string
	Help     string
	Type     string // TypeString (default), TypeInt, TypeBool, TypeDuration
	Default  string
	Required bool
}

// HelpRequested returns true if args contains --help or -h. Call before
// invoking the parser so help short-circuits before validation runs.
func HelpRequested(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// PrintHelp writes formatted help text for s to w.
func PrintHelp(w io.Writer, s *Spec) {
	fmt.Fprintf(w, "Usage: subflux %s [flags]", s.Name)
	if s.Args != "" {
		fmt.Fprintf(w, " %s", s.Args)
	}
	fmt.Fprintln(w)
	if s.Help != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(s.Help, "\n"))
	}
	if len(s.Flags) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	maxSig := 0
	for _, f := range s.Flags {
		maxSig = max(maxSig, len(flagSignature(f)))
	}
	for _, f := range s.Flags {
		desc := f.Help
		if f.Default != "" {
			desc += fmt.Sprintf(" (default: %s)", f.Default)
		}
		if f.Required {
			desc += " [required]"
		}
		fmt.Fprintf(w, "  %-*s  %s\n", maxSig, flagSignature(f), desc)
	}
}

func flagSignature(f Flag) string {
	if f.Type == "" || f.Type == TypeBool {
		return "--" + f.Name
	}
	return fmt.Sprintf("--%s <%s>", f.Name, f.Type)
}

// PrintRootHelp writes a list of all subcommands to w. specs are listed
// in the order provided; the caller may sort or filter (e.g. drop
// hidden subcommands like "health").
func PrintRootHelp(w io.Writer, specs []Spec) {
	fmt.Fprintln(w, "Usage: subflux <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "subflux runs as a daemon when invoked with no arguments.")
	fmt.Fprintln(w, "Subcommands operate on the running server or perform local maintenance.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	maxName := 0
	for _, s := range specs {
		if n := len(s.Name); n > maxName {
			maxName = n
		}
	}
	for _, s := range specs {
		fmt.Fprintf(w, "  %-*s  %s\n", maxName, s.Name, s.Synopsis)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment:")
	fmt.Fprintln(w, "  SUBFLUX_URL      Server base URL for remote commands (default http://127.0.0.1:8374)")
	fmt.Fprintln(w, "  SUBFLUX_API_KEY  API key sent as X-API-Key (required when the server has auth enabled;")
	fmt.Fprintln(w, "                   create one with 'subflux generate-api-key' or in the web UI)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Use 'subflux <command> --help' for command-specific flags.")
}

// ParseAndValidate parses args (the argv slice after the subcommand name,
// e.g. os.Args[2:]) against spec in a single pass: "--key value" and
// "--key=value" forms are split (the "=" form on the FIRST "=", so values
// may themselves contain "="), unknown flags are rejected with a "did you
// mean" suggestion, stray positional tokens are rejected, bool flags are
// parsed from the spec table (bare "--flag", or "--flag=true|false"),
// typed values (int, duration) are validated, required flags are enforced,
// and spec defaults are applied for absent flags.
//
// Preserved edge semantics (pinned by tests): a trailing non-bool flag
// without a value is treated as unset (a required flag then errors), a
// non-bool flag whose next token starts with "--" is a missing-value error
// (the "--name=--literal" form is the escape hatch for intentional
// flag-like values), a duplicated flag keeps the last value, and "--help" /
// "-h" / bare "--" / empty-name "--=v" tokens are skipped (help
// short-circuits in the dispatch preamble before this parse runs).
func ParseAndValidate(args []string, spec *Spec) (Params, error) {
	p := Params{strs: make(map[string]string), bools: make(map[string]bool)}
	known := knownFlags(spec)
	for i := 0; i < len(args); i++ {
		next, err := p.consumeToken(args, i, known, spec)
		if err != nil {
			return Params{}, err
		}
		i = next
	}
	if err := finishParams(&p, spec); err != nil {
		return Params{}, err
	}
	return p, nil
}

// consumeToken processes args[i] and returns the index of the last token
// it consumed: i, or i+1 when a space-separated value was taken. Skipped
// tokens (help, bare "--", empty flag names) consume themselves.
func (p Params) consumeToken(args []string, i int, known map[string]Flag, spec *Spec) (int, error) {
	arg := args[i]
	if arg == "--help" || arg == "-h" {
		return i, nil
	}
	name, isFlag := strings.CutPrefix(arg, "--")
	if !isFlag {
		return i, fmt.Errorf("unexpected argument %q (subcommands take --flag arguments only)", arg)
	}
	if name == "" {
		return i, nil // bare "--"
	}
	value, hasValue := "", false
	if k, v, found := strings.Cut(name, "="); found {
		if k == "" {
			return i, nil // "--=value": empty flag name, dropped
		}
		name, value, hasValue = k, v, true
	}
	f, ok := known[name]
	if !ok {
		return i, fmt.Errorf("unknown flag --%s%s", name, suggestion(name, spec.Flags))
	}
	if f.Type == TypeBool {
		return i, p.setBool(name, value, hasValue)
	}
	if !hasValue {
		if i+1 >= len(args) {
			// Trailing flag without a value: treated as unset, matching
			// the pre-consolidation parser. Required flags error below.
			return i, nil
		}
		if strings.HasPrefix(args[i+1], "--") {
			// The next token is another flag: report the missing value
			// instead of silently consuming the flag as the value
			// ("--lang --download" must not set lang to "--download" and
			// suppress the download flag).
			return i, fmt.Errorf("--%s requires a value (to pass a value beginning with --, use --%s=<value>)", name, name)
		}
		p.strs[name] = args[i+1]
		return i + 1, nil
	}
	p.strs[name] = value
	return i, nil
}

// setBool parses a spec-table bool flag: the bare form sets true; the
// "=value" form goes through strconv.ParseBool.
func (p Params) setBool(name, value string, hasValue bool) error {
	if !hasValue {
		p.bools[name] = true
		return nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("--%s: %q is not a valid boolean", name, value)
	}
	p.bools[name] = b
	return nil
}

// finishParams enforces required flags, validates explicit typed values,
// and applies spec defaults for absent non-bool flags.
func finishParams(p *Params, spec *Spec) error {
	for _, f := range spec.Flags {
		if f.Type == TypeBool {
			continue
		}
		value, present := p.strs[f.Name]
		if !present {
			if f.Required {
				return fmt.Errorf("--%s is required", f.Name)
			}
			if f.Default != "" {
				p.strs[f.Name] = f.Default
			}
			continue
		}
		if err := checkType(f, value); err != nil {
			return err
		}
	}
	return nil
}

// knownFlags indexes a spec's flags by name for O(1) lookup.
func knownFlags(spec *Spec) map[string]Flag {
	known := make(map[string]Flag, len(spec.Flags))
	for _, f := range spec.Flags {
		known[f.Name] = f
	}
	return known
}

func checkType(f Flag, value string) error {
	switch f.Type {
	case TypeInt:
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("--%s: %q is not a valid integer", f.Name, value)
		}
	case TypeDuration:
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("--%s: %q is not a valid duration (e.g. 30s, 5m, 1h): %w", f.Name, value, err)
		}
	}
	return nil
}

// suggestion returns " (did you mean --x?)" if name is within edit
// distance 2 of any known flag; empty string otherwise.
func suggestion(name string, flags []Flag) string {
	type candidate struct {
		name string
		dist int
	}
	var best candidate
	best.dist = -1
	for _, f := range flags {
		d := editDistance(name, f.Name)
		if d > 2 {
			continue
		}
		if best.dist == -1 || d < best.dist {
			best = candidate{name: f.Name, dist: d}
		}
	}
	if best.dist == -1 {
		return ""
	}
	return fmt.Sprintf(" (did you mean --%s?)", best.name)
}

// editDistance computes the Levenshtein distance between a and b.
// Sized for short flag names (< 32 chars); allocation cost is trivial.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func minInt(values ...int) int {
	m := values[0]
	for _, v := range values[1:] {
		m = min(m, v)
	}
	return m
}

// SortByName returns specs sorted alphabetically by Name. Useful before
// passing to PrintRootHelp.
func SortByName(specs []Spec) []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SuggestName finds the closest match in candidates (by Levenshtein
// distance, max 2) to input. Returns the matched name and true; or
// empty string and false when nothing within distance 2.
//
// Used by main.go to suggest "did you mean 'search'?" when an unknown
// subcommand is invoked.
func SuggestName(input string, candidates []string) (string, bool) {
	bestDist := -1
	bestName := ""
	for _, c := range candidates {
		d := editDistance(input, c)
		if d > 2 {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			bestName = c
		}
	}
	return bestName, bestDist != -1
}
