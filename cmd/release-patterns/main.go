// Command release-patterns imports the arr and TRaSH patterns
// internal/release parses with, from patterns.manifest.json at the commits
// patterns_source.go pins. generate writes patterns_gen.go and
// its testdata snapshots only when every check passes; otherwise it names
// each refusal and writes nothing. watch plans the upstream-watch issues
// for what changed upstream outside the imported entries, which ride the
// pin bump instead.
//
// Usage, from internal/release (go generate ./internal/release):
//
//	release-patterns generate [-dir .]
//
// and from the repository root (scripts/upstream-watch.sh):
//
//	release-patterns watch -dir internal/release -existing issues.jsonl -out actions.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/cplieger/go/filesafe"
)

const (
	manifestFile = "patterns.manifest.json"
	sourceFile   = "patterns_source.go"
	typesFile    = "release.go"
	overrideFile = "overrides.go"
	generated    = "patterns_gen.go"
	upstreamFile = "testdata/upstream.json"
	fixturesFile = "testdata/golden/fixtures.json"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: release-patterns generate [-dir dir]
       release-patterns watch [-dir dir] -existing issues.jsonl -out actions.json`

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "generate":
			return runGenerate(ctx, args[1:], stdout, stderr)
		case "watch":
			return runWatch(ctx, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

func runGenerate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", ".", "the internal/release directory")
	raw := fs.String("raw", defaultRawBase, "base URL of raw upstream files")
	api := fs.String("api", defaultAPIBase, "base URL of the GitHub REST API")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, refusals, err := build(ctx, *dir, newUpstream(*raw, *api, ""))
	if err != nil {
		fmt.Fprintln(stderr, "release-patterns:", err)
		return 1
	}
	if len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintln(stderr, "refused", r)
		}
		fmt.Fprintf(stderr, "release-patterns: %d refusals; nothing written\n", len(refusals))
		return 1
	}
	if err := out.write(ctx, *dir); err != nil {
		fmt.Fprintln(stderr, "release-patterns:", err)
		return 1
	}
	fmt.Fprintln(stdout, out.share)
	return 0
}

// outputs is what one successful run writes.
type outputs struct {
	share                   string
	gen, upstream, fixtures []byte
}

// write puts the generated table last, so a run failing between writes
// leaves the old table beside new snapshots; re-running go generate and
// diffing the package catches it.
func (o *outputs) write(ctx context.Context, dir string) error {
	for _, f := range []struct {
		name string
		data []byte
	}{{upstreamFile, o.upstream}, {fixturesFile, o.fixtures}, {generated, o.gen}} {
		target, err := filepath.Abs(filepath.Join(dir, f.name))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if _, err := filesafe.WriteFile(ctx, target, f.data, filesafe.WithMode(0o644)); err != nil {
			return err
		}
	}
	return nil
}

// manifest is patterns.manifest.json: the only declaration of an import.
type manifest struct {
	Upstreams     map[string]upstreamDecl `json:"upstreams"`
	Symbols       []symbolDecl            `json:"symbols"`
	CustomFormats []formatDecl            `json:"custom_formats"`
	Fixtures      []fixtureDecl           `json:"fixtures"`
	Watch         watchDecl               `json:"watch"`
	Version       int                     `json:"version"`
}

// watchDecl routes unimported TRaSH stems: one matching a class's stems
// goes to that class's digest issue, any other is a candidate of its own.
type watchDecl struct {
	Classes []classDecl `json:"classes"`
}

// classDecl is one out-of-model class. Stems are path.Match patterns; the
// first class matching a stem takes it.
type classDecl struct {
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Reason string   `json:"reason"`
	Stems  []string `json:"stems"`
}

type upstreamDecl struct {
	Repo string `json:"repo"`
	Pin  string `json:"pin"`
}

// symbolDecl is one imported C# symbol. Groups maps a source pattern's named
// groups to Source names, Captures lists other groups the parser reads, and
// Members maps an enum's members to the Sources they stand for.
type symbolDecl struct {
	Groups   map[string]string   `json:"groups,omitempty"`
	Members  map[string][]string `json:"members,omitempty"`
	ID       string              `json:"id"`
	Upstream string              `json:"upstream"`
	Path     string              `json:"path"`
	Symbol   string              `json:"symbol"`
	Kind     string              `json:"kind"`
	Override string              `json:"override,omitempty"`
	Options  []string            `json:"options,omitempty"`
	Captures []string            `json:"captures,omitempty"`
}

type formatDecl struct {
	Files map[string]formatFile `json:"files"`
	ID    string                `json:"id"`
	Label string                `json:"label"`
}

// formatFile is one arr's file of a custom format.
type formatFile struct {
	Path    string   `json:"path"`
	TrashID string   `json:"trash_id"`
	Specs   []string `json:"specs"`
}

func loadManifest(name string) (*manifest, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("%s: version %d, want 1", name, m.Version)
	}
	return &m, m.validate()
}

func (m *manifest) validate() error {
	seen := map[string]bool{}
	for i := range m.Symbols {
		if err := m.validateSymbol(&m.Symbols[i], seen); err != nil {
			return err
		}
	}
	for i := range m.CustomFormats {
		f := &m.CustomFormats[i]
		if seen[f.ID] {
			return fmt.Errorf("duplicate id %s", f.ID)
		}
		seen[f.ID] = true
		for arr := range f.Files {
			if m.sourceEnum(arr) == nil {
				return fmt.Errorf("%s: no source enum symbol for %q", f.ID, arr)
			}
		}
	}
	if err := m.validateFixtures(); err != nil {
		return err
	}
	return m.validateWatch()
}

func (m *manifest) validateWatch() error {
	seen := map[string]bool{}
	for i := range m.Watch.Classes {
		c := &m.Watch.Classes[i]
		if seen[c.Key] {
			return fmt.Errorf("watch class %s: duplicate key", c.Key)
		}
		seen[c.Key] = true
		if err := c.validate(); err != nil {
			return err
		}
	}
	files := map[string]string{}
	for _, f := range m.logicFiles() {
		k := logicKey(f.upstream, f.path)
		if prev, ok := files[k]; ok && prev != f.path {
			return fmt.Errorf("watch: %s and %s share the issue key %s", prev, f.path, k)
		}
		files[k] = f.path
	}
	return nil
}

func (m *manifest) validateFixtures() error {
	seen := map[string]bool{}
	for _, f := range m.Fixtures {
		if seen[f.ID] {
			return fmt.Errorf("duplicate fixture %s", f.ID)
		}
		seen[f.ID] = true
		if _, ok := m.Upstreams[f.Upstream]; !ok {
			return fmt.Errorf("fixture %s: unknown upstream %q", f.ID, f.Upstream)
		}
	}
	return nil
}

func (c *classDecl) validate() error {
	if !watchKeyRE.MatchString(c.Key) {
		return fmt.Errorf("watch class %q: a key is of [a-z0-9-]", c.Key)
	}
	if c.Title == "" || c.Reason == "" || len(c.Stems) == 0 {
		return fmt.Errorf("watch class %s: needs a title, a reason and stems", c.Key)
	}
	for _, p := range c.Stems {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("watch class %s: stem %q: %w", c.Key, p, err)
		}
	}
	return nil
}

func (m *manifest) validateSymbol(s *symbolDecl, seen map[string]bool) error {
	if seen[s.ID] {
		return fmt.Errorf("duplicate id %s", s.ID)
	}
	seen[s.ID] = true
	if _, ok := m.Upstreams[s.Upstream]; !ok {
		return fmt.Errorf("%s: unknown upstream %q", s.ID, s.Upstream)
	}
	if _, ok := declTypes[s.Kind]; !ok {
		return fmt.Errorf("%s: unknown kind %q", s.ID, s.Kind)
	}
	if s.Override != "" && len(s.Groups)+len(s.Captures) > 0 {
		return fmt.Errorf("%s: an override's groups are pinned by its hash, not declared", s.ID)
	}
	return nil
}

func (m *manifest) sourceEnum(arr string) *symbolDecl {
	for i := range m.Symbols {
		if s := &m.Symbols[i]; s.Kind == kindEnum && s.Upstream == arr {
			return s
		}
	}
	return nil
}
