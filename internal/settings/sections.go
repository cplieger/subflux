package settings

import (
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"go.yaml.in/yaml/v3"
)

// section is one row of the ordered section table: parse reads the section's
// node, nil when absent, into its Active field and returns absolute paths. A
// landing adds its row beside its Active field; a row whose parse reads
// another section's result follows that row.
type section struct {
	parse func(n *yaml.Node, a *Active) []formschema.FieldError
	key   string
}

var sections = []section{
	{key: "server", parse: func(n *yaml.Node, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Server, errs = parseServer(n)
		return errs
	}},
	{key: "languages", parse: func(n *yaml.Node, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Languages, errs = language.ParsePolicy(n)
		return errs
	}},
}

// crossChecks run over a configuration whose every section parsed, each
// reading two or more sections.
var crossChecks []func(Active) []formschema.FieldError

// parseSections runs each table row over its section's one node, failing a
// tainted row without running it, refuses each name in names no row holds,
// then runs the cross checks when nothing failed. failed names each row that
// did not parse.
func parseSections(names []string, values map[string][]*yaml.Node, tainted map[string]bool) (Active, map[string]bool, []formschema.FieldError) {
	var a Active
	var errs []formschema.FieldError
	failed := map[string]bool{}
	known := map[string]bool{}
	for _, s := range sections {
		known[s.key] = true
		if tainted[s.key] {
			failed[s.key] = true
			continue
		}
		var n *yaml.Node
		if v := values[s.key]; len(v) > 0 {
			n = v[0]
		}
		if serrs := s.parse(n, &a); len(serrs) > 0 {
			failed[s.key] = true
			errs = append(errs, serrs...)
		}
	}
	for _, name := range names {
		if !known[name] {
			errs = append(errs, formschema.FieldError{Path: name, Message: "unknown section"})
		}
	}
	if len(errs) > 0 || len(failed) > 0 {
		return a, failed, errs
	}
	for _, check := range crossChecks {
		errs = append(errs, check(a)...)
	}
	return a, failed, errs
}
