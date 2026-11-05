package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/atomicfile/v4"
)

// maxOpens bounds the issues one run creates or reopens; the rest are
// listed in the bulk issue and opened by the next runs.
const maxOpens = 10

const (
	labelWatch      = "upstream-watch"
	labelCandidate  = "candidate"
	labelOutOfModel = "out-of-model"
	bulkKey         = "bulk"
	upstreamTrash   = "trash"
	stateOpen       = "open"
	stateClosed     = "closed"
)

var (
	watchKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	stemRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]*$`)
	branchRE   = regexp.MustCompile(`renovate: datasource=git-refs depName=\S+ packageName=https://github\.com/(\S+) branch=(\S+)`)
)

func runWatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "internal/release", "the internal/release directory")
	existing := fs.String("existing", "", "the upstream-watch issues, one JSON object per line")
	out := fs.String("out", "", "the action plan to write")
	raw := fs.String("raw", defaultRawBase, "base URL of raw upstream files")
	api := fs.String("api", defaultAPIBase, "base URL of the GitHub REST API")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *existing == "" || *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if err := watch(ctx, *dir, *existing, *out, newUpstream(*raw, *api, os.Getenv("GH_TOKEN")), stdout); err != nil {
		fmt.Fprintln(stderr, "release-patterns watch:", err)
		return 1
	}
	return 0
}

func watch(ctx context.Context, dir, existing, out string, up *upstream, stdout io.Writer) error {
	issues, err := readIssues(existing)
	if err != nil {
		return err
	}
	w, err := newWatcher(ctx, dir, up)
	if err != nil {
		return err
	}
	actions, err := w.plan(ctx, issues)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(actions, "", "  ")
	if err != nil {
		return err
	}
	target, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if _, err := atomicfile.WriteFile(ctx, target, append(b, '\n'), atomicfile.WithMode(0o644)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d actions planned\n", len(actions))
	return nil
}

// watchIssue is one existing issue, as scripts/upstream-watch.sh lists it.
type watchIssue struct {
	State  string `json:"state"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Number int    `json:"number"`
}

func readIssues(name string) ([]watchIssue, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReader(f))
	dec.DisallowUnknownFields()
	var issues []watchIssue
	for {
		var is watchIssue
		err := dec.Decode(&is)
		if errors.Is(err, io.EOF) {
			return issues, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if is.Number <= 0 || is.State != stateOpen && is.State != stateClosed {
			return nil, fmt.Errorf("%s: issue %d with state %q", name, is.Number, is.State)
		}
		issues = append(issues, is)
	}
}

// action is one planned issue change; scripts/apply-issues.sh has one case
// per kind and stops on any other. Number is 0 for a create.
type action struct {
	Kind   string   `json:"kind"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	Number int      `json:"number,omitempty"`
}

const (
	actCreate = "create"
	actReopen = "reopen"
	actEdit   = "edit"
)

// marked is an issue with its marker read: since and through bound the
// upstream commits its body covers, empty on the bulk issue.
type marked struct {
	since, through string
	watchIssue
}

var markerRE = regexp.MustCompile(`^<!-- subflux:upstream key=([A-Za-z0-9._:+-]+)(?: since=([0-9a-f]{40}) through=([0-9a-f]{40}))? -->$`)

func marker(key, since, through string) string {
	if since == "" {
		return "<!-- subflux:upstream key=" + key + " -->"
	}
	return "<!-- subflux:upstream key=" + key + " since=" + since + " through=" + through + " -->"
}

// indexIssues keys the marked issues, an open one before a closed one and
// the newest first. Only a body's last line is read as its marker, so text
// quoted from upstream above it cannot claim a key.
func indexIssues(issues []watchIssue) map[string]marked {
	byKey := map[string]marked{}
	for _, is := range issues {
		body := strings.TrimRight(is.Body, " \t\r\n")
		m := markerRE.FindStringSubmatch(body[strings.LastIndexByte(body, '\n')+1:])
		if m == nil {
			continue
		}
		if cur, ok := byKey[m[1]]; ok && !outranks(&is, &cur.watchIssue) {
			continue
		}
		byKey[m[1]] = marked{since: m[2], through: m[3], watchIssue: is}
	}
	return byKey
}

func outranks(a, b *watchIssue) bool {
	if a.State != b.State {
		return a.State == stateOpen
	}
	return a.Number > b.Number
}

// watcher compares upstream heads against the pins and the commits each
// issue last covered.
type watcher struct {
	m        *manifest
	up       *upstream
	repos    map[string]string            // upstream -> owner/name
	pins     map[string]string            // upstream -> pinned commit
	heads    map[string]string            // upstream -> branch head
	dirs     map[string]string            // arr -> its TRaSH CF directory
	imported map[string]map[string]bool   // arr -> imported CF stems
	lists    map[string]map[string]string // arr@ref -> stem -> blob hash
	moved    map[string]moves             // arr@from..to
	logic    []logicFile
}

func newWatcher(ctx context.Context, dir string, up *upstream) (*watcher, error) {
	m, err := loadManifest(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, err
	}
	src := filepath.Join(dir, sourceFile)
	consts, err := readConsts(src)
	if err != nil {
		return nil, err
	}
	branches, err := readBranches(src)
	if err != nil {
		return nil, err
	}
	w := &watcher{
		m: m, up: up, repos: map[string]string{}, pins: map[string]string{}, heads: map[string]string{},
		dirs: map[string]string{}, imported: map[string]map[string]bool{}, logic: m.logicFiles(),
		lists: map[string]map[string]string{}, moved: map[string]moves{},
	}
	for _, name := range slices.Sorted(maps.Keys(m.Upstreams)) {
		if err := w.addUpstream(ctx, name, consts, branches[m.Upstreams[name].Pin]); err != nil {
			return nil, fmt.Errorf("%s: %w", src, err)
		}
	}
	if _, ok := w.repos[upstreamTrash]; !ok {
		return nil, errors.New("the manifest declares no trash upstream")
	}
	for i := range m.CustomFormats {
		if err := w.addFormat(&m.CustomFormats[i]); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// addUpstream resolves one upstream's head; branch is its pin's renovate
// comment as [repo, branch].
func (w *watcher) addUpstream(ctx context.Context, name string, consts map[string]string, branch [2]string) error {
	decl := w.m.Upstreams[name]
	if r := checkPin(decl.Pin, consts[decl.Pin]); r != nil {
		return errors.New(r.String())
	}
	if branch[0] != decl.Repo {
		return fmt.Errorf("%s has no renovate comment naming https://github.com/%s", decl.Pin, decl.Repo)
	}
	head, err := w.up.head(ctx, decl.Repo, branch[1])
	if err != nil {
		return err
	}
	w.heads[name], w.repos[name], w.pins[name] = head, decl.Repo, consts[decl.Pin]
	return nil
}

func (w *watcher) addFormat(f *formatDecl) error {
	for arr, file := range f.Files {
		d, stem := path.Dir(file.Path), strings.TrimSuffix(path.Base(file.Path), ".json")
		if prev, ok := w.dirs[arr]; ok && prev != d {
			return fmt.Errorf("%s: %s CFs sit in %s and %s; the watch lists one directory per arr", f.ID, arr, prev, d)
		}
		w.dirs[arr] = d
		if w.imported[arr] == nil {
			w.imported[arr] = map[string]bool{}
		}
		w.imported[arr][stem] = true
	}
	return nil
}

// readBranches reads each pin's renovate comment: const name -> [repo, branch].
func readBranches(name string) (map[string][2]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	out := map[string][2]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST || gd.Doc == nil || len(gd.Specs) != 1 {
			continue
		}
		m := branchRE.FindStringSubmatch(gd.Doc.Text())
		if vs, ok := gd.Specs[0].(*ast.ValueSpec); ok && m != nil && len(vs.Names) == 1 {
			out[vs.Names[0].Name] = [2]string{m[1], m[2]}
		}
	}
	return out, nil
}

// logicFile is a C# file the manifest imports symbols from; its code
// outside them is watched.
type logicFile struct {
	upstream, path string
}

func (m *manifest) logicFiles() []logicFile {
	seen := map[logicFile]bool{}
	var files []logicFile
	for i := range m.Symbols {
		f := logicFile{m.Symbols[i].Upstream, m.Symbols[i].Path}
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	slices.SortFunc(files, func(a, b logicFile) int {
		return strings.Compare(a.upstream+"/"+a.path, b.upstream+"/"+b.path)
	})
	return files
}

func logicKey(upstream, file string) string { return upstream + ":" + path.Base(file) + ":logic" }

// watchKey is one issue identity and what it covers.
type watchKey struct {
	key      string
	upstream string
	name     string // the stem, the class key or the logic file's path
	kind     keyKind
}

type keyKind uint8

const (
	keyLogic keyKind = iota
	keyNew
	keyCandidate
	keyClass
)

// stemKey routes an unimported stem: a stem another arr imports needs a
// manifest entry, a class stem joins its digest, any other is a candidate.
func (w *watcher) stemKey(stem string) watchKey {
	for _, set := range w.imported {
		if set[stem] {
			return watchKey{key: "trash:new:" + stem, upstream: upstreamTrash, kind: keyNew, name: stem}
		}
	}
	for _, c := range w.m.Watch.Classes {
		for _, p := range c.Stems {
			if ok, _ := path.Match(p, stem); ok {
				return watchKey{key: "trash:class:" + c.Key, upstream: upstreamTrash, kind: keyClass, name: c.Key}
			}
		}
	}
	return watchKey{key: "trash:cf:" + stem, upstream: upstreamTrash, kind: keyCandidate, name: stem}
}

// keys lists every key with something to compare: each logic file, each
// stem at the pin or the head, and each TRaSH key an issue holds, whose
// stems may exist only at the commit it covered through. An
// imported stem's key compares only the arrs that do not import it.
func (w *watcher) keys(ctx context.Context, issues map[string]marked) ([]watchKey, error) {
	byKey := map[string]watchKey{}
	for _, f := range w.logic {
		k := watchKey{key: logicKey(f.upstream, f.path), upstream: f.upstream, kind: keyLogic, name: f.path}
		byKey[k.key] = k
	}
	if err := w.stemKeys(ctx, byKey); err != nil {
		return nil, err
	}
	for key, is := range issues {
		if _, ok := byKey[key]; ok || is.since == "" {
			continue
		}
		if k, ok := w.issueKey(key); ok {
			byKey[key] = k
		}
	}
	keys := slices.Collect(maps.Values(byKey))
	slices.SortFunc(keys, func(a, b watchKey) int {
		if a.kind != b.kind {
			return int(a.kind) - int(b.kind)
		}
		return strings.Compare(a.key, b.key)
	})
	return keys, nil
}

// stemKeys adds the key of every stem at the TRaSH pin and head.
func (w *watcher) stemKeys(ctx context.Context, byKey map[string]watchKey) error {
	for _, ref := range []string{w.pins[upstreamTrash], w.heads[upstreamTrash]} {
		for _, arr := range slices.Sorted(maps.Keys(w.dirs)) {
			list, err := w.listing(ctx, arr, ref)
			if err != nil {
				return fmt.Errorf("%s %s: %w", w.repos[upstreamTrash], w.dirs[arr], err)
			}
			for stem := range list {
				k := w.stemKey(stem)
				byKey[k.key] = k
			}
		}
	}
	return nil
}

// issueKey reads a TRaSH key an issue holds, if the manifest still routes
// it.
func (w *watcher) issueKey(key string) (watchKey, bool) {
	for _, prefix := range []string{"trash:cf:", "trash:new:"} {
		if stem, ok := strings.CutPrefix(key, prefix); ok && stemRE.MatchString(stem) {
			k := w.stemKey(stem)
			return k, k.key == key
		}
	}
	if c, ok := strings.CutPrefix(key, "trash:class:"); ok && w.class(c) != nil {
		return watchKey{key: key, upstream: upstreamTrash, kind: keyClass, name: c}, true
	}
	return watchKey{}, false
}

// plan compares each key from the commit its issue last covered, or the
// pin, to the head. A change edits an open issue (keeping its since),
// reopens a closed one from its through, or creates one; nothing closes an
// issue, since only a person can tell a never-imported change was handled.
func (w *watcher) plan(ctx context.Context, issues []watchIssue) ([]action, error) {
	byKey := indexIssues(issues)
	keys, err := w.keys(ctx, byKey)
	if err != nil {
		return nil, err
	}
	var actions []action
	var deferred []string
	opens := 0
	for _, k := range keys {
		var is *marked
		if found, ok := byKey[k.key]; ok {
			is = &found
		}
		a, from, err := w.planKey(ctx, k, is)
		switch {
		case err != nil:
			return nil, err
		case a == nil:
		case a.Kind == actEdit:
			actions = append(actions, *a)
		case opens >= maxOpens:
			deferred = append(deferred, "- "+a.Title+": "+w.compareLink(k.upstream, from, w.heads[k.upstream]))
		default:
			opens++
			actions = append(actions, *a)
		}
	}
	if len(deferred) > 0 {
		bulk := byKey[bulkKey]
		actions = append(actions, bulkAction(&bulk, deferred))
	}
	return actions, nil
}

// planKey is k's action, nil when nothing changed, and the commit its
// window starts from; is is k's issue, nil when it has none.
func (w *watcher) planKey(ctx context.Context, k watchKey, is *marked) (*action, string, error) {
	head, from := w.heads[k.upstream], w.pins[k.upstream]
	if is != nil && is.through != "" {
		from = is.through
	}
	if from == head {
		return nil, from, nil
	}
	r, err := w.report(ctx, k, from, head)
	if err != nil || r.empty() {
		return nil, from, err
	}
	switch {
	case is == nil:
		return &action{Kind: actCreate, Title: r.title, Body: r.body(w, from, head), Labels: r.labels}, from, nil
	case is.State != stateOpen:
		return &action{Kind: actReopen, Number: is.Number, Title: r.title, Body: r.body(w, from, head)}, from, nil
	}
	since := w.pins[k.upstream]
	if is.since != "" {
		since = is.since
	}
	if since != from {
		if r, err = w.report(ctx, k, since, head); err != nil {
			return nil, from, err
		}
	}
	return &action{Kind: actEdit, Number: is.Number, Title: r.title, Body: r.body(w, since, head)}, from, nil
}

// bulkAction lists the changes over this run's limit in the one bulk
// issue, which no limit holds back.
func bulkAction(is *marked, deferred []string) action {
	a := action{
		Kind:  actCreate,
		Title: "Upstream changes over one run's issue limit",
		Body: fmt.Sprintf("The upstream watch found more changes than it opens issues for in one run (%d). Later runs open these, at most %d a run:\n\n%s\n\nClose this issue once each has its own.\n\n%s\n",
			maxOpens, maxOpens, strings.Join(deferred, "\n"), marker(bulkKey, "", "")),
		Labels: []string{labelWatch},
	}
	switch {
	case is.Number == 0:
	case is.State == stateOpen:
		a.Kind, a.Number, a.Labels = actEdit, is.Number, nil
	default:
		a.Kind, a.Number, a.Labels = actReopen, is.Number, nil
	}
	return a
}
