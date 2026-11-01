package settings

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// scalars lists every scalar under n as path=text, unchanged ones as
// path=!unchanged, in document order.
func scalars(n *yaml.Node, path string) []string {
	switch n.Kind {
	case yaml.MappingNode:
		var out []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, scalars(n.Content[i+1], child(path, n.Content[i].Value))...)
		}
		return out
	case yaml.SequenceNode:
		var out []string
		for i, e := range n.Content {
			out = append(out, scalars(e, index(path, i))...)
		}
		return out
	}
	if n.Tag == formschema.UnchangedTag {
		return []string{path + "=" + formschema.UnchangedTag}
	}
	return []string{path + "=" + n.Value}
}

func unchangedPaths(n *yaml.Node) []string {
	var out []string
	for _, s := range scalars(n, "") {
		if p, ok := strings.CutSuffix(s, "="+formschema.UnchangedTag); ok {
			out = append(out, p)
		}
	}
	return out
}

func encoded(t *testing.T, n *yaml.Node) string {
	t.Helper()
	b, err := encode(n)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// filledExample is the 640a639 example with every empty value a distinct
// literal, then a misspelled secret key of a registered section, an entry of
// an unknown kind and a commented-out key.
func filledExample(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/example-640a639.yaml")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	text := regexp.MustCompile(`: ""`).ReplaceAllStringFunc(string(raw), func(string) string {
		n++
		return `: "lit-` + strconv.Itoa(n) + `"`
	})
	if n < 10 {
		t.Fatalf("filled %d values; the fixture changed", n)
	}
	return text +
		"t:\n  a: shown\n  s: lit-secret\n  sx: lit-misspelled\n" +
		"sources:\n  - {kind: bogus, id: x, api_key: lit-kind}\n" +
		"# api_key: 1f2e\n" // gitleaks:allow
}

func TestDocumentRedacts(t *testing.T) {
	t.Run("only marked scalars and references keep their text", func(t *testing.T) {
		testSection(t)
		t.Setenv("SUBFLUX_KEY", "lit-env")
		s := serve(fixture(t, filledExample(t)+"server: {log_level: debug}\nrefs: {api_key: '${SUBFLUX_KEY}'}\n"), nil, nil)
		doc := s.Snapshot().Document()
		text := encoded(t, doc)
		for _, leak := range []string{"lit-", "1f2e", "#"} {
			if strings.Contains(text, leak) {
				t.Errorf("Document holds %q:\n%s", leak, text)
			}
		}
		got := scalars(doc, "")
		for _, want := range []string{"t.a=shown", "t.s=!unchanged", "server.log_level=debug", "refs.api_key=${SUBFLUX_KEY}", "sonarr.url=!unchanged"} {
			if !slices.Contains(got, want) {
				t.Errorf("Document lacks %s", want)
			}
		}
	})
	t.Run("a save of it keeps every value", func(t *testing.T) {
		testSection(t)
		const text = "t: {a: shown, s: lit-secret, sx: lit-misspelled}\nserver: {log_level: debug}\n" +
			"sonarr: {url: 'http://sonarr:8989', api_key: lit-1}\n"
		snap := serve(fixture(t, text), nil, nil).Snapshot()
		merged, errs := mergeDocument(snap.p.doc, snap.Document())
		if errs != nil {
			t.Fatalf("merge errors %v", errs)
		}
		if got, want := scalars(merged, ""), scalars(snap.p.doc, ""); !slices.Equal(got, want) {
			t.Errorf("merged values\n%v\nwant the stored\n%v", got, want)
		}
	})
	t.Run("an unknown section's entry shows no id, so it cannot keep its secret", func(t *testing.T) {
		snap := serve(fixture(t, "sources: [{id: x, kind: bogus, api_key: lit-kind}]\n"), nil, nil).Snapshot()
		if _, errs := mergeDocument(snap.p.doc, snap.Document()); !slices.Equal(errPaths(errs), []string{"sources[0]"}) {
			t.Errorf("merge errors %v, want one at sources[0]", errs)
		}
	})
	t.Run("an absent file or one not a mapping has no document", func(t *testing.T) {
		for _, text := range []string{"", "[a, b]\n", "a: 1\n---\nb: 2\n"} {
			if d := serve(fixture(t, text), nil, nil).Snapshot().Document(); d != nil {
				t.Errorf("Document of %q = %+v, want nil", text, d)
			}
		}
	})
	t.Run("an anchored, tagged or forged-mark scalar is unchanged", func(t *testing.T) {
		s := serve(fixture(t, "server: {log_level: !!str debug, log_format: &f json}\nx: *f\nf: {s: !plain lit}\n"+validLanguages), nil, nil)
		want := []string{"server.log_level", "server.log_format", "x", "f.s"}
		if got := unchangedPaths(s.Snapshot().Document()); !slices.Equal(got, want) {
			t.Errorf("unchanged %v, want %v", got, want)
		}
	})
}

func TestSaveRefusesMovedSecret(t *testing.T) {
	const stored = "t2: [{id: a, s: k1}, {id: b, s: k2}]\nt3: [{s: k3}, {s: k4}]\nt4: [{id: d, s: k5}, {id: d, s: k6}]\n" + validLanguages
	snap := serve(fixture(t, stored), nil, nil).Snapshot()
	cases := []struct{ name, body, path string }{
		{"a reordered id-less element", "t3: [{s: !unchanged ''}, {s: k3}]\n", "t3[0]"},
		{"a deleted element shifting an id-less one", "t3: [{s: !unchanged ''}]\n", "t3[0]"},
		{"an id-less element in an id list", "t2: [{s: !unchanged ''}]\n", "t2[0]"},
		{"an id sent twice", "t2: [{id: a, s: k9}, {id: a, s: !unchanged ''}]\n", "t2[1]"},
		{"an id saved twice", "t4: [{id: d, s: !unchanged ''}]\n", "t4[0]"},
		{"a new id", "t2: [{id: c, s: !unchanged ''}]\n", "t2[0]"},
		{"a value the file lacks", "t2: [{id: a, s: k1, p: !unchanged ''}]\n", "t2[0].p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := mergeDocument(snap.p.doc, node(t, c.body))
			if !slices.Equal(errPaths(errs), []string{c.path}) {
				t.Errorf("errors %v, want one at %s", errs, c.path)
			}
		})
	}
	t.Run("a reordered id list keeps each secret with its id", func(t *testing.T) {
		merged, errs := mergeDocument(snap.p.doc, node(t, "t2: [{id: b, s: !unchanged ''}, {id: a, s: !unchanged ''}]\n"))
		want := []string{"t2[0].id=b", "t2[0].s=k2", "t2[1].id=a", "t2[1].s=k1"}
		if errs != nil || !slices.Equal(scalars(merged, ""), want) {
			t.Errorf("merged %v, %v; want %v", scalars(merged, ""), errs, want)
		}
	})
}

// sectionFixtures holds one valid section per registered row and the paths
// its Document leaves unchanged, its Secret fields'.
var sectionFixtures = map[string]struct {
	text    string
	secrets []string
}{
	"server": {
		text:    "server:\n  trusted_proxies: [10.0.0.0/8, 192.168.1.5]\n  allowed_hosts: [nas.lan, 'nas.lan:8374']\n  log_level: \"debug\"\n  log_format: text\n",
		secrets: nil,
	},
	"languages": {
		text:    "languages:\n  close_varieties: true\n  default:\n    - tag: pt-BR\n      variants: [standard, forced]\n    - tag: en\n      variants: [hi]\n  rules:\n    - audio: ja\n      targets:\n        - tag: en\n    - audio: es\n      targets: []\n",
		secrets: nil,
	},
}

// TestDocumentRoundTrip saves the Document of one file holding every
// section's fixture: each row needs one, so a section whose parse does not
// mark what it read fails here.
func TestDocumentRoundTrip(t *testing.T) {
	var text strings.Builder
	var secrets []string
	for _, sec := range sections {
		fx, ok := sectionFixtures[sec.key]
		if !ok {
			t.Fatalf("section %s has no round-trip fixture", sec.key)
		}
		text.WriteString(fx.text)
		secrets = append(secrets, fx.secrets...)
	}
	for key := range sectionFixtures {
		if !slices.ContainsFunc(sections, func(s section) bool { return s.key == key }) {
			t.Errorf("fixture %s names no registered section", key)
		}
	}
	path := fixture(t, text.String())
	s := serve(path, nil, nil)
	if m := s.Snapshot().Mode(); m != ModeActive {
		failure, _ := s.Snapshot().Failure()
		t.Fatalf("fixture mode %v (%v), want active", m, failure)
	}
	doc := s.Snapshot().Document()
	if got := unchangedPaths(doc); !slices.Equal(got, secrets) {
		t.Errorf("unchanged %v, want the Secret paths %v", got, secrets)
	}
	if _, errs, err := s.Save(t.Context(), doc, s.Snapshot().Revision()); err != nil || errs != nil {
		t.Fatalf("Save(Document()) = %v, %v", errs, err)
	}
	if got, want := scalars(Load(path, Registries{}).p.doc, ""), scalars(node(t, text.String()), ""); !slices.Equal(got, want) {
		t.Errorf("saved values %v, want %v", got, want)
	}
}

func TestDocumentFreshCopy(t *testing.T) {
	snap := serve(fixture(t, "server: {log_level: debug}\n"+validLanguages), nil, nil).Snapshot()
	first := snap.Document()
	first.Content[1].Content[1].Value = "error"
	first.Content = append(first.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "x"})
	if got := scalars(snap.Document(), ""); !slices.Equal(got, []string{"server.log_level=debug", "languages.default[0].tag=en"}) {
		t.Errorf("a later Document %v saw an earlier copy's edit", got)
	}
}
