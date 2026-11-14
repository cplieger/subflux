package settings

import (
	"slices"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/backup"
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
// and names it in needs. schema is the form parse decodes the section's flat
// keys with, nil for a hand-walked section.
type section struct {
	parse  func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError
	schema *formschema.Schema
	key    string
	needs  string
}

var sections = []section{
	{key: "server", schema: &serverSchema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Server, errs = parseServer(n)
		return errs
	}},
	{key: accounts.SectionKey, schema: &accounts.Schema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Auth, errs = accounts.ParseConfig(n)
		return errs
	}},
	{key: "sources", parse: func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Sources, errs = mediasource.ParseConfigs(n, r.Sources)
		return errs
	}},
	{key: "scan", schema: &mediasource.ScheduleSchema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
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
	{key: "search", needs: "languages", schema: &search.Schema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Search, errs = search.ParseConfig(n, a.Languages)
		return errs
	}},
	{key: "post_processing", schema: &mediawrite.Schema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Media, errs = mediawrite.ParseConfig(n)
		return errs
	}},
	{key: "timing", schema: &timing.Schema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Timing, errs = timing.ParseConfig(n)
		return errs
	}},
	{key: "embedded_subtitles", schema: &embedded.Schema, parse: func(n *yaml.Node, _ Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Embedded, errs = embedded.ParseConfig(n)
		return errs
	}},
	{key: backup.SectionKey, schema: &backup.Schema, parse: func(n *yaml.Node, r Registries, a *Active) []formschema.FieldError {
		var errs []formschema.FieldError
		a.Backup, errs = backup.ParseConfig(n, r.StoreDir)
		return errs
	}},
}

// SectionSchema is one section whose flat keys its owner decodes with
// Schema.
type SectionSchema struct {
	Key    string
	Schema formschema.Schema
}

// SectionSchemas lists every section the table decodes with a schema, in
// table order; a hand-walked section has none.
func SectionSchemas() []SectionSchema {
	var out []SectionSchema
	for _, s := range sections {
		if s.schema != nil {
			out = append(out, SectionSchema{Key: s.key, Schema: *s.schema})
		}
	}
	return out
}

// bootOnlyChanges refuses each BootOnly field whose value in next differs
// from stored's, both documents read section by section with ${NAME}
// references expanded.
func bootOnlyChanges(stored, next *yaml.Node) []formschema.FieldError {
	var errs []formschema.FieldError
	for _, s := range sections {
		if s.schema != nil {
			errs = append(errs, formschema.Prefix(s.key, s.schema.BootOnlyChanges(storedSection(stored, s.key), storedSection(next, s.key)))...)
		}
	}
	return errs
}

// crossCheck reads two or more sections: it runs once every section in reads
// parsed, whatever else failed, so a section Failed keeps was checked, and
// its errors fail owner.
type crossCheck struct {
	check func(Active) []formschema.FieldError
	owner string
	reads []string
}

var crossChecks = []crossCheck{{
	reads: []string{"server", accounts.SectionKey}, owner: accounts.SectionKey,
	check: func(a Active) []formschema.FieldError { return accounts.CheckOrigins(a.Auth, a.Server.AllowedHosts()) },
}}

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
// with one error, refuses each name in names no row holds, then runs each
// cross check whose sections parsed. failed names each row that did not parse
// or whose cross check refused it.
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
	for _, c := range crossChecks {
		if slices.ContainsFunc(c.reads, func(k string) bool { return failed[k] }) {
			continue
		}
		if cerrs := c.check(a); len(cerrs) > 0 {
			failed[c.owner] = true
			errs = append(errs, cerrs...)
		}
	}
	return a, failed, errs
}
