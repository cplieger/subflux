// Package cliparse adds help generation, unknown-flag detection (with
// typo suggestions), and required/type validation on top of subflux's
// existing flag-parsing helpers. It does not replace the parser; it
// validates after the fact, so existing call sites remain backward
// compatible.
//
// The design choice is deliberate: the CLI surface is auxiliary to
// subflux's HTTP/JSON API and web UI, so a heavyweight CLI framework
// (kong, cobra) would be over-engineered for ~13 flat subcommands. This
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

// Spec describes one subcommand. Used by PrintHelp and Validate.
type Spec struct {
	Name     string // e.g. "search"
	Synopsis string // one-line purpose, shown in root help
	Help     string // multi-line description for `<command> --help`
	Args     string // optional usage suffix, e.g. "<imdb-id>"
	Flags    []Flag
}

// Flag describes one CLI flag for a subcommand.
type Flag struct {
	Name     string
	Help     string
	Type     string // "string" (default), "int", "bool", "duration"
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
		if n := len(flagSignature(f)); n > maxSig {
			maxSig = n
		}
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
	if f.Type == "" || f.Type == "bool" {
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
	fmt.Fprintln(w, "Use 'subflux <command> --help' for command-specific flags.")
}

// Validate checks that args contains only flags declared in spec, that
// all required flags are present, and that typed flags (int, duration)
// parse cleanly. Returns nil on success; any error is human-readable
// and includes a "did you mean" suggestion for unknown flags whose
// edit distance to a known flag is at most 2.
//
// args is the raw argv slice (after the subcommand name, e.g.
// os.Args[2:]); params is the result of clisearch.ParseArgs.
func Validate(args []string, params map[string]string, spec *Spec) error {
	known := make(map[string]Flag, len(spec.Flags))
	for _, f := range spec.Flags {
		known[f.Name] = f
	}
	for _, a := range args {
		if a == "--help" || a == "-h" {
			continue
		}
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			continue
		}
		// Strip --flag=value form for the unknown-flag check
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if name == "" {
			continue
		}
		if _, ok := known[name]; ok {
			continue
		}
		// Skip values that follow a known flag: ParseArgs treats arg[i+1]
		// as the value for arg[i] when arg[i] is `--flag value`. We can't
		// reconstruct that pairing without re-parsing, so we apply a
		// heuristic: if the previous arg is a known --flag and this arg
		// is not a --flag itself, it is probably a value. Skip below by
		// only flagging args that start with --.
		if msg := suggestion(name, spec.Flags); msg != "" {
			return fmt.Errorf("unknown flag --%s%s", name, msg)
		}
		return fmt.Errorf("unknown flag --%s", name)
	}
	for _, f := range spec.Flags {
		_, present := params[f.Name]
		if f.Required && !present {
			return fmt.Errorf("--%s is required", f.Name)
		}
		if !present {
			continue
		}
		if err := checkType(f, params[f.Name]); err != nil {
			return err
		}
	}
	return nil
}

func checkType(f Flag, value string) error {
	switch f.Type {
	case "int":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("--%s: %q is not a valid integer", f.Name, value)
		}
	case "duration":
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
		if v < m {
			m = v
		}
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

// ParseArgs parses --key value pairs from the given argument slice.
// Returns the params map and whether --download was specified.
func ParseArgs(args []string) (params map[string]string, download bool) {
	params = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if args[i] == "--download" {
			download = true
			continue
		}
		if name, ok := strings.CutPrefix(args[i], "--"); ok && name != "" && i+1 < len(args) {
			params[name] = args[i+1]
			i++
		}
	}
	return params, download
}
