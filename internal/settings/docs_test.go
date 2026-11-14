package settings

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/radarr"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/provider/providers"
	"go.yaml.in/yaml/v4"
)

const (
	configurationDoc = "../../docs/configuration.md"
	hardeningDoc     = "../../docs/hardening.md"
	readmeDoc        = "../../README.md"
)

// docSourceKinds are the source adapters the binary registers (app's
// sourceKinds, which settings cannot import).
var docSourceKinds = []mediasource.AdapterKind{sonarr.Kind, radarr.Kind}

// probeBase is a configuration that loads Active; each probe adds one
// documented key to it, so every level above that key is one the loader walks
// into, a source entry of a registered type included.
const probeBase = `
sources:
  - {id: tv, type: sonarr, url: "http://sonarr.lan:8989", api_key: test-key}
languages:
  default: [{tag: en}]
providers:
  gestdown: {}
`

// probeNames stands in for a placeholder segment of a documented key.
var probeNames = map[string]string{"<name>": "gestdown"}

// docRow is one row of a settings table: its key, its Default cell and where
// it was read.
type docRow struct{ key, def, at string }

// example is one fenced yaml block of a doc and where it starts.
type example struct{ text, at string }

func TestConfigurationDocMatchesSchemas(t *testing.T) {
	r := Registries{StoreDir: "/config", Sources: docSourceKinds, Providers: providers.New().Entries}
	config := readDoc(t, configurationDoc)
	sectionRows := settingTables(t, config)
	readme := referenceTable(t, readDoc(t, readmeDoc))

	t.Run("the probe base loads", func(t *testing.T) {
		if errs, failed := fromBytes([]byte(probeBase), r).Failure(); failed {
			t.Fatalf("the probe base fails: %v", errs)
		}
	})
	t.Run("every section has its table", func(t *testing.T) {
		for _, s := range sections {
			if _, ok := sectionRows[s.key]; !ok {
				t.Errorf("no ### `%s` table under ## Every setting", s.key)
			}
		}
		for key := range sectionRows {
			if !slices.ContainsFunc(sections, func(s section) bool { return s.key == key }) {
				t.Errorf("### `%s` names no section the loader reads", key)
			}
		}
	})
	t.Run("every row sits in its section's table", func(t *testing.T) {
		seen := map[string]string{}
		for key, rows := range sectionRows {
			for _, row := range rows {
				if row.key != key && !strings.HasPrefix(row.key, key+".") && !strings.HasPrefix(row.key, key+"[]") {
					t.Errorf("%s: %s is under ### `%s`", row.at, row.key, key)
				}
				if at, dup := seen[row.key]; dup {
					t.Errorf("%s: %s is also at %s", row.at, row.key, at)
				}
				seen[row.key] = row.at
			}
		}
	})
	want := schemaRows(r)
	t.Run("every schema field is a row with its default", func(t *testing.T) {
		for _, w := range want {
			section := w.key[:strings.IndexAny(w.key, ".[")]
			i := slices.IndexFunc(sectionRows[section], func(row docRow) bool { return row.key == w.key })
			if i < 0 {
				t.Errorf("%s has no row under ### `%s`", w.key, section)
				continue
			}
			if got := sectionRows[section][i]; got.def != w.def {
				t.Errorf("%s: %s's default reads %s, want %s", got.at, w.key, got.def, w.def)
			}
		}
	})
	t.Run("the readme's defaults match", func(t *testing.T) {
		for _, row := range readme {
			if i := slices.IndexFunc(want, func(w docRow) bool { return w.key == row.key }); i >= 0 && want[i].def != row.def {
				t.Errorf("%s: %s's default reads %s, want %s", row.at, row.key, row.def, want[i].def)
			}
		}
	})
	t.Run("every documented key loads", func(t *testing.T) {
		var rows []docRow
		for _, s := range sectionRows {
			rows = append(rows, s...)
		}
		for _, row := range append(rows, readme...) {
			doc := probeDoc(t)
			overlay(t, doc, strings.Split(row.key, "."))
			if unknown := unknownErrors(t, doc, r); len(unknown) > 0 {
				t.Errorf("%s: %s is refused: %v", row.at, row.key, unknown)
			}
		}
	})
	t.Run("every example loads", func(t *testing.T) {
		for _, path := range []string{configurationDoc, hardeningDoc} {
			for _, ex := range yamlExamples(t, path) {
				var n yaml.Node
				if err := yaml.Unmarshal([]byte(ex.text), &n); err != nil || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
					t.Errorf("%s: the example is not one YAML mapping: %v", ex.at, err)
					continue
				}
				doc := probeDoc(t)
				mergeInto(doc, n.Content[0])
				if unknown := unknownErrors(t, doc, r); len(unknown) > 0 {
					t.Errorf("%s: the example is refused: %v", ex.at, unknown)
				}
			}
		}
	})
}

// schemaRows is the row every registered schema field needs, with the Default
// cell docDefault writes: each section schema's fields, each provider's
// settings and each source kind's adapter keys.
func schemaRows(r Registries) []docRow {
	var out []docRow
	add := func(prefix string, s formschema.Schema) {
		for _, f := range s.Fields {
			key := prefix + f.Key
			if !slices.ContainsFunc(out, func(row docRow) bool { return row.key == key }) {
				out = append(out, docRow{key: key, def: docDefault(f)})
			}
		}
	}
	for _, s := range SectionSchemas() {
		add(s.Key+".", s.Schema)
	}
	for _, e := range r.Providers {
		add("providers."+string(e.ID)+".settings.", e.Schema)
	}
	for _, k := range r.Sources {
		add("sources[].", k.Schema)
	}
	return out
}

// docDefault is the Default cell of f's row: the default in code format, else
// required, an empty list or unset.
func docDefault(f formschema.Field) string {
	switch {
	case f.Default != "":
		return "`" + f.Default + "`"
	case f.Required:
		return "required"
	case f.Kind == formschema.StringList:
		return "`[]`"
	default:
		return "_(unset)_"
	}
}

func readDoc(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

// settingTables reads the tables under "## Every setting", each under its
// "### `<section>`" heading.
func settingTables(t *testing.T, lines []string) map[string][]docRow {
	t.Helper()
	start := slices.Index(lines, "## Every setting")
	if start < 0 {
		t.Fatal("configuration.md has no ## Every setting")
	}
	out := map[string][]docRow{}
	section := ""
	for i := start + 1; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		line := lines[i]
		if h, ok := strings.CutPrefix(line, "### "); ok {
			section = strings.Trim(h, "`")
			if h != "`"+section+"`" {
				t.Fatalf("configuration.md:%d: a section heading is one key in code format", i+1)
			}
			out[section] = nil
			continue
		}
		if row, ok := tableRow(t, line, "configuration.md", i); ok {
			if section == "" {
				t.Fatalf("configuration.md:%d: a row before the first section heading", i+1)
			}
			out[section] = append(out[section], row)
		}
	}
	return out
}

// referenceTable reads the first table under the README's "## Configuration
// reference".
func referenceTable(t *testing.T, lines []string) []docRow {
	t.Helper()
	start := slices.Index(lines, "## Configuration reference")
	if start < 0 {
		t.Fatal("README.md has no ## Configuration reference")
	}
	var out []docRow
	for i := start + 1; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		if out != nil && !strings.HasPrefix(lines[i], "|") {
			break
		}
		if row, ok := tableRow(t, lines[i], "README.md", i); ok {
			out = append(out, row)
		}
	}
	if len(out) == 0 {
		t.Fatal("README.md's configuration reference has no table")
	}
	return out
}

// tableRow reads a "| `key` | default | ..." line; a header, a separator or
// any other line is no row.
func tableRow(t *testing.T, line, file string, i int) (docRow, bool) {
	t.Helper()
	if !strings.HasPrefix(line, "| `") {
		return docRow{}, false
	}
	cells := strings.Split(strings.Trim(line, "|"), " | ")
	key := strings.TrimSpace(cells[0])
	if len(cells) < 3 || strings.Count(key, "`") != 2 || !strings.HasSuffix(key, "`") {
		t.Fatalf("%s:%d: a settings row is | `key` | default | description |", file, i+1)
	}
	return docRow{key: strings.Trim(key, "`"), def: strings.TrimSpace(cells[1]), at: file + ":" + strconv.Itoa(i+1)}, true
}

// yamlExamples are the doc's fenced yaml blocks, each a configuration
// fragment.
func yamlExamples(t *testing.T, path string) []example {
	t.Helper()
	lines := readDoc(t, path)
	name := filepath.Base(path)
	var out []example
	for i := 0; i < len(lines); i++ {
		if lines[i] != "```yaml" {
			continue
		}
		end := slices.Index(lines[i+1:], "```")
		if end < 0 {
			t.Fatalf("%s:%d: an unclosed fence", name, i+1)
		}
		out = append(out, example{text: strings.Join(lines[i+1:i+1+end], "\n"), at: name + ":" + strconv.Itoa(i+1)})
		i += end + 1
	}
	return out
}

func probeDoc(t *testing.T) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(probeBase), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

// overlay sets the key segs name in m, a "[]" segment reaching into the
// list's first element, to a scalar, adding each level m lacks.
func overlay(t *testing.T, m *yaml.Node, segs []string) {
	t.Helper()
	name, list := strings.CutSuffix(segs[0], "[]")
	if strings.HasPrefix(name, "<") {
		sub, ok := probeNames[name]
		if !ok {
			t.Fatalf("no probe name for %s", name)
		}
		name = sub
	}
	leaf := len(segs) == 1
	v := mappingValue(m, name)
	switch {
	case list && leaf:
		setValue(m, name, &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{probeScalar()}})
	case list:
		if v == nil || v.Kind != yaml.SequenceNode || len(v.Content) == 0 || v.Content[0].Kind != yaml.MappingNode {
			v = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
			setValue(m, name, v)
		}
		overlay(t, v.Content[0], segs[1:])
	case leaf:
		setValue(m, name, probeScalar())
	default:
		if v == nil || v.Kind != yaml.MappingNode {
			v = &yaml.Node{Kind: yaml.MappingNode}
			setValue(m, name, v)
		}
		overlay(t, v, segs[1:])
	}
}

func probeScalar() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: "probe"} }

// mergeInto sets each key of src in dst, merging two mappings key by key.
func mergeInto(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i].Value, src.Content[i+1]
		if d := mappingValue(dst, k); d != nil && d.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode {
			mergeInto(d, v)
			continue
		}
		setValue(dst, k, v)
	}
}

func setValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

// unknownErrors loads doc and answers each refusal of a key or section the
// loader does not know; the probe base holds none, so each comes from the
// probe.
func unknownErrors(t *testing.T, doc *yaml.Node, r Registries) []formschema.FieldError {
	t.Helper()
	b, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := fromBytes(b, r).Failure()
	var out []formschema.FieldError
	for _, e := range errs {
		if strings.HasPrefix(e.Message, "unknown") {
			out = append(out, e)
		}
	}
	return out
}
