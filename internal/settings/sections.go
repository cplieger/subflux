package settings

import (
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

// section is one row of the ordered section table: parse reads the section's
// node, nil when absent, into its Active field with the registered adapter
// kinds and returns absolute paths. A landing adds its row beside its Active
// field. A row whose parse reads another section's result follows that row
// and names it in needs.
type section struct {
	parse func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError
	key   string
	needs string
}

var sections = []section{
	{key: "server", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Server, errs = parseServer(n)
		return errs
	}},
	{key: "sources", parse: func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Sources, errs = mediasource.ParseConfigs(n, r.Sources)
		return errs
	}},
	{key: "scan", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Scan, errs = mediasource.ParseSchedule(n)
		return errs
	}},
	{key: "languages", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Languages, errs = language.ParsePolicy(n)
		return errs
	}},
	{key: "providers", parse: func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Providers, errs = provider.ParseConfigs(n, r.Providers)
		return errs
	}},
	{key: "search", needs: "languages", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Search, errs = search.ParseConfig(n, a.Languages)
		return errs
	}},
	{key: "post_processing", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Media, errs = mediawrite.ParseConfig(n)
		return errs
	}},
	{key: "timing", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Timing, errs = timing.ParseConfig(n)
		return errs
	}},
	{key: "embedded_subtitles", parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Embedded, errs = embedded.ParseConfig(n)
		return errs
	}},
}

// crossChecks run over a configuration whose every section parsed, each
// reading two or more sections.
var crossChecks []func(Active) []formschema.FieldError

// run parses the row's one node, nil when absent, unless the row it needs
// failed: then a present section reports one error and is not parsed.
func (s *section) run(r Registries, nodes []*yaml.Node, failed map[string]bool, a *Active) ([]formschema.FieldError, bool) {
	var n *yaml.Node
	if len(nodes) > 0 {
		n = nodes[0]
	}
	if s.needs != "" && failed[s.needs] {
		if n == nil {
			return nil, false
		}
		return []formschema.FieldError{{Path: s.key, Message: "depends on " + s.needs + ", which did not parse"}}, false
	}
	errs := s.parse(n, r, a)
	return errs, len(errs) == 0
}

// parseSections runs each table row over its section's one node, failing a
// tainted row without running it, and a present row whose needed row failed
// with one error, refuses each name in names no row holds, then runs the
// cross checks when nothing failed. failed names each row that did not
// parse.
func parseSections(r Registries, names []string, values map[string][]*yaml.Node, tainted map[string]bool) (Active, map[string]bool, []formschema.FieldError) {
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
		if serrs, ok := s.run(r, values[s.key], failed, &a); !ok {
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
