package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/release/netregex"
)

// generator holds one run's inputs and what it has produced so far.
type generator struct {
	m         *manifest
	up        *upstream
	pins      map[string]string
	sources   map[string]bool
	overrides map[string]string // override name -> upstream pattern sha256
	used      map[string]bool   // override names an entry uses
	enums     map[string]map[int]string
	share     map[string]string // unique upstream pattern -> how it is imported
	originals map[string]original
	fixtures  map[string]fixtureFile
	refusals  []refusal
	symbols   []genSymbol
	formats   []genFormat
}

// original is one upstream pattern as the arr compiles it, which
// internal/release's oracle test runs under regexp2.
type original struct {
	Pattern     string   `json:"pattern"`
	Replacement string   `json:"replacement,omitempty"`
	Spec        string   `json:"spec,omitempty"`
	Options     []string `json:"options"`
}

type genSymbol struct {
	decl        *symbolDecl
	rule        string
	pattern     string
	override    string
	replacement string
	values      []string
	members     []string
	options     netregex.Options
}

type genFormat struct {
	decl  *formatDecl
	file  *formatFile
	arr   string
	specs []genSpec
}

type genSpec struct {
	kind    string
	rule    string
	pattern string
	sources []string
	spec    cfSpec
}

func (g *generator) refuse(r *refusal) bool {
	if r == nil {
		return false
	}
	g.refusals = append(g.refusals, *r)
	return true
}

// build runs every check and returns the files to write, or the refusals.
func build(ctx context.Context, dir string, up *upstream) (outputs, []refusal, error) {
	m, err := loadManifest(filepath.Join(dir, manifestFile))
	if err != nil {
		return outputs{}, nil, err
	}
	g := &generator{m: m, up: up, enums: map[string]map[int]string{}, share: map[string]string{}, originals: map[string]original{}, fixtures: map[string]fixtureFile{}, used: map[string]bool{}}
	if g.pins, err = readConsts(filepath.Join(dir, sourceFile)); err != nil {
		return outputs{}, nil, err
	}
	if g.sources, err = readSourceNames(filepath.Join(dir, typesFile)); err != nil {
		return outputs{}, nil, err
	}
	if g.overrides, err = readOverrides(filepath.Join(dir, overrideFile)); err != nil {
		return outputs{}, nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(m.Upstreams)) {
		pin := m.Upstreams[name].Pin
		g.refuse(checkPin(pin, g.pins[pin]))
	}
	if len(g.refusals) > 0 {
		return outputs{}, g.refusals, nil
	}
	for i := range m.Symbols {
		g.symbol(ctx, &m.Symbols[i])
	}
	for i := range m.CustomFormats {
		g.format(ctx, &m.CustomFormats[i])
	}
	for i := range m.Fixtures {
		g.fixture(ctx, &m.Fixtures[i])
	}
	if len(g.refusals) == 0 {
		g.refusals = unusedOverrides(g.overrides, g.used)
	}
	if len(g.refusals) > 0 {
		return outputs{}, g.refusals, nil
	}
	return g.outputs()
}

func (g *generator) outputs() (outputs, []refusal, error) {
	gen, err := g.emit()
	if err != nil {
		return outputs{}, nil, err
	}
	snapshot, err := indentJSON(g.originals)
	if err != nil {
		return outputs{}, nil, err
	}
	fixtures, err := fixturesJSON(g.fixtures)
	if err != nil {
		return outputs{}, nil, err
	}
	return outputs{gen: gen, upstream: snapshot, fixtures: fixtures, share: g.shareLine()}, nil, nil
}

func indentJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return b.Bytes(), err
}

func (g *generator) ref(upstreamName string) (repo, pin string) {
	u := g.m.Upstreams[upstreamName]
	return u.Repo, g.pins[u.Pin]
}

func (g *generator) symbol(ctx context.Context, s *symbolDecl) {
	repo, pin := g.ref(s.Upstream)
	src, err := g.up.file(ctx, repo, pin, s.Path)
	if err != nil {
		g.refuse(&refusal{id: s.ID, reason: err.Error()})
		return
	}
	cs, err := extractSymbol(src, s.Symbol, s.Kind)
	if err != nil {
		g.refuse(&refusal{id: s.ID, reason: fmt.Sprintf("%s %s: %v", s.Path, s.Symbol, err)})
		return
	}
	gs := genSymbol{decl: s}
	switch s.Kind {
	case kindList:
		gs.rule, gs.values = "data", cs.values
	case kindEnum:
		if !g.enum(s, &cs, &gs) {
			return
		}
	default:
		if !g.regex(s, &cs, &gs) {
			return
		}
	}
	g.symbols = append(g.symbols, gs)
}

func (g *generator) enum(s *symbolDecl, cs *csSymbol, gs *genSymbol) bool {
	if g.refuse(checkMembers(s.ID, cs.members, s.Members)) {
		return false
	}
	byValue := map[int]string{}
	for _, mem := range cs.members {
		byValue[mem.value] = mem.name
		gs.members = append(gs.members, mem.name+"="+strconv.Itoa(mem.value))
		g.checkSources(s.ID, s.Members[mem.name])
	}
	g.enums[s.Upstream] = byValue
	gs.rule = "data"
	return true
}

func (g *generator) checkSources(id string, names []string) {
	for _, n := range names {
		if !g.sources[n] {
			g.refuse(&refusal{id: id, reason: fmt.Sprintf("%q is not a release.Source", n)})
		}
	}
}

func (g *generator) regex(s *symbolDecl, cs *csSymbol, gs *genSymbol) bool {
	opts, r := checkOptions(s.ID, s.Options, cs.options)
	if g.refuse(r) {
		return false
	}
	gs.options, gs.replacement = opts, cs.replacement
	g.originals[s.ID] = original{Pattern: cs.pattern, Options: slices.Sorted(slices.Values(s.Options)), Replacement: cs.replacement}
	if s.Override != "" {
		g.used[s.Override] = true
		if g.refuse(checkOverride(s.ID, s.Override, cs.pattern, g.overrides)) {
			return false
		}
		gs.override, gs.rule = s.Override, "override "+s.Override
		g.share[cs.pattern] = gs.rule
		return true
	}
	if strings.Contains(cs.replacement, "$") {
		g.refuse(&refusal{id: s.ID, reason: "a substituting replacement needs an override"})
		return false
	}
	re, err := netregex.Compile(cs.pattern, opts)
	if err != nil {
		g.refuse(unsupportedPattern(s.ID, cs.pattern, err))
		return false
	}
	if len(s.Groups) > 0 || len(s.Captures) > 0 {
		g.checkSources(s.ID, slices.Collect(maps.Values(s.Groups)))
		read := append(slices.Collect(maps.Keys(s.Groups)), s.Captures...)
		if g.refuse(checkGroups(s.ID, re, read)) {
			return false
		}
	}
	gs.pattern, gs.rule = cs.pattern, asPublished
	g.share[cs.pattern] = gs.rule
	return true
}

// asPublished is the rule of a pattern the executor runs as upstream wrote it.
const asPublished = "as published"

func unsupportedPattern(id, pattern string, err error) *refusal {
	if u, ok := errors.AsType[*netregex.UnsupportedError](err); ok {
		return &refusal{id: id, reason: fmt.Sprintf("pattern %q: the executor does not run %s: add an override", pattern, u.Construct)}
	}
	return &refusal{id: id, reason: fmt.Sprintf("pattern %q: %v: add an override", pattern, err)}
}

// specKinds are the TRaSH specification implementations the import reads.
var specKinds = map[string]string{
	"ReleaseTitleSpecification": "title",
	"ReleaseGroupSpecification": "group",
	"SourceSpecification":       "source",
}

func (g *generator) format(ctx context.Context, f *formatDecl) {
	for _, arr := range slices.Sorted(maps.Keys(f.Files)) {
		file := f.Files[arr]
		repo, pin := g.ref("trash")
		cf, r := fetchFormat(ctx, g.up, repo, pin, f.ID, &file)
		if g.refuse(r) {
			continue
		}
		gf := genFormat{decl: f, arr: arr, file: &file}
		ok := true
		for i := range cf.Specifications {
			gsp, good := g.spec(specKey(f.ID, arr, i), &cf.Specifications[i])
			ok = ok && good
			gf.specs = append(gf.specs, gsp)
		}
		if ok {
			g.formats = append(g.formats, gf)
		}
	}
}

// spec reads one specification; id is its specKey.
func (g *generator) spec(id string, spec *cfSpec) (genSpec, bool) {
	arr := strings.Fields(id)[1]
	gs := genSpec{spec: *spec, kind: specKinds[spec.Implementation]}
	switch gs.kind {
	case "":
		return gs, !g.refuse(&refusal{id: id, reason: fmt.Sprintf("spec %s: %s is not read", spec.Name, spec.Implementation)})
	case "source":
		member, r := sourceValue(id, spec, g.enums[arr])
		if g.refuse(r) {
			return gs, false
		}
		gs.rule, gs.sources = "data "+member, g.m.sourceEnum(arr).Members[member]
		return gs, true
	}
	var pattern string
	if err := json.Unmarshal(spec.Fields.Value, &pattern); err != nil {
		return gs, !g.refuse(&refusal{id: id, reason: fmt.Sprintf("spec %s: value is not a string", spec.Name)})
	}
	g.originals[id] = original{Pattern: pattern, Options: []string{"IgnoreCase"}, Spec: spec.Name}
	if _, err := netregex.Compile(pattern, netregex.Options{IgnoreCase: true}); err != nil {
		return gs, !g.refuse(unsupportedPattern(id+" ("+spec.Name+")", pattern, err))
	}
	gs.pattern, gs.rule = pattern, asPublished
	g.share[pattern] = gs.rule
	return gs, true
}

// specKey names a specification by its format, arr and upstream position,
// as testdata/upstream.json keys it.
func specKey(id, arr string, i int) string { return id + " " + arr + " " + strconv.Itoa(i) }

// shareLine reports how many unique upstream patterns import as published.
func (g *generator) shareLine() string {
	published := 0
	for _, rule := range g.share {
		if rule == asPublished {
			published++
		}
	}
	return fmt.Sprintf("release-patterns: %d of %d unique patterns import as published", published, len(g.share))
}
