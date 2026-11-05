package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// report is what changed under one key between two commits, rendered.
type report struct {
	key      watchKey
	title    string
	labels   []string
	intro    string
	file     string // the watched file's link, for a logic key
	why      string
	footer   string
	sections []string
}

func (r *report) empty() bool { return len(r.sections) == 0 }

// maxBodyRunes stays under GitHub's 65,536-character issue body.
const maxBodyRunes = 60000

// body renders the issue over since..head; the marker is its last line.
func (r *report) body(w *watcher, since, head string) string {
	var b strings.Builder
	b.WriteString(r.intro + "\n\n")
	if r.file != "" {
		b.WriteString("- File: " + r.file + "\n")
	}
	b.WriteString("- Changes: " + w.compareLink(r.key.upstream, since, head) + "\n")
	if r.why != "" {
		b.WriteString("- Why it is watched: " + r.why + "\n")
	}
	b.WriteString("\n")
	if r.empty() {
		b.WriteString("Net of these commits, nothing watched differs from " + since[:7] + ".")
	} else {
		b.WriteString(strings.Join(r.sections, "\n\n"))
	}
	b.WriteString("\n\n" + r.footer)
	text := b.String()
	if runes := []rune(text); len(runes) > maxBodyRunes {
		text = string(runes[:maxBodyRunes]) + "\n\n(Cut here; the compare link lists every change.)"
	}
	return text + "\n\n" + marker(r.key.key, since, head) + "\n"
}

func (w *watcher) report(ctx context.Context, k watchKey, from, to string) (*report, error) {
	if k.kind == keyLogic {
		return w.logicReport(ctx, k, from, to)
	}
	return w.cfReport(ctx, k, from, to)
}

func (w *watcher) compareLink(upstream, from, to string) string {
	return "[" + from[:7] + "..." + to[:7] + "](https://github.com/" + w.repos[upstream] + "/compare/" + from + "..." + to + ")"
}

func (w *watcher) fileLink(upstream, ref, p string) string {
	return "[" + p + "](https://github.com/" + w.repos[upstream] + "/blob/" + ref + "/" + p + ")"
}

func display(upstream string) string {
	if upstream == upstreamTrash {
		return "TRaSH"
	}
	return strings.ToUpper(upstream[:1]) + upstream[1:]
}

func (w *watcher) cfReport(ctx context.Context, k watchKey, from, to string) (*report, error) {
	r := w.cfHeader(k)
	stems, lists, err := w.keyStems(ctx, k, from, to)
	if err != nil {
		return nil, err
	}
	for _, stem := range stems {
		for _, arr := range slices.Sorted(maps.Keys(w.dirs)) {
			if w.imported[arr][stem] {
				continue
			}
			sec, err := w.fileSection(ctx, arr, stem, from, to, lists[arr])
			if err != nil {
				return nil, err
			}
			if sec != "" {
				r.sections = append(r.sections, sec)
			}
		}
	}
	return r, nil
}

func (w *watcher) cfHeader(k watchKey) *report {
	r := &report{
		key:    k,
		labels: []string{labelWatch, "upstream: " + upstreamTrash, labelCandidate},
		footer: "Score-only changes are ignored. Close this issue after review; a later change reopens it.",
	}
	switch k.kind {
	case keyNew:
		r.title = "TRaSH custom format " + k.name + " has a file the import manifest does not list"
		r.intro = "TRaSH Guides has a file for a custom format subflux imports, at a path the import manifest does not list."
		r.why = "the manifest lists each arr's file of an imported format, so this one needs an entry or a decision to leave it out."
	case keyClass:
		r.labels[2] = labelOutOfModel
		r.title, r.intro = "TRaSH "+k.name+" formats changed (digest)", "TRaSH Guides changed custom formats that subflux leaves out of its model."
		if c := w.class(k.name); c != nil {
			r.title = "TRaSH " + c.Title + " formats changed (digest)"
			r.intro = "TRaSH Guides changed custom formats that subflux leaves out of its model: " + c.Title + "."
			r.why = c.Reason
		}
	default:
		r.title = "TRaSH custom format " + k.name + " changed (not imported)"
		r.intro = "TRaSH Guides changed a custom format that subflux does not import."
		r.why = "a release variant or label that could affect subtitle timing."
	}
	return r
}

// keyStems lists the stems k routes at from or to, sorted, and each arr's
// listings at from and at to.
func (w *watcher) keyStems(ctx context.Context, k watchKey, from, to string) (stems []string, lists map[string][2]map[string]string, err error) {
	lists = map[string][2]map[string]string{}
	routed := map[string]bool{}
	for arr := range w.dirs {
		var pair [2]map[string]string
		for i, ref := range []string{from, to} {
			if pair[i], err = w.listing(ctx, arr, ref); err != nil {
				return nil, nil, err
			}
			for stem := range pair[i] {
				if w.stemKey(stem).key == k.key {
					routed[stem] = true
				}
			}
		}
		lists[arr] = pair
	}
	return slices.Sorted(maps.Keys(routed)), lists, nil
}

func (w *watcher) class(key string) *classDecl {
	for i := range w.m.Watch.Classes {
		if w.m.Watch.Classes[i].Key == key {
			return &w.m.Watch.Classes[i]
		}
	}
	return nil
}

// listing maps each CF stem of arr's directory at ref to its blob hash.
func (w *watcher) listing(ctx context.Context, arr, ref string) (map[string]string, error) {
	id := arr + "@" + ref
	if l, ok := w.lists[id]; ok {
		return l, nil
	}
	entries, err := w.up.entries(ctx, w.repos[upstreamTrash], ref, w.dirs[arr])
	if err != nil {
		return nil, err
	}
	l := map[string]string{}
	for _, e := range entries {
		stem, ok := strings.CutSuffix(path.Base(e.Path), ".json")
		if !ok {
			continue
		}
		if !stemRE.MatchString(stem) {
			return nil, fmt.Errorf("%s at %s: file name %q is outside the watched stem form", w.dirs[arr], ref, runesafeName(e.Path))
		}
		l[stem] = e.SHA
	}
	w.lists[id] = l
	return l, nil
}

// fileSection renders one arr's file of a stem between from and to, or ""
// when nothing but scores and descriptions changed. lists holds arr's
// listings at from and at to.
func (w *watcher) fileSection(ctx context.Context, arr, stem, from, to string, lists [2]map[string]string) (string, error) {
	shaFrom, inFrom := lists[0][stem]
	shaTo, inTo := lists[1][stem]
	if inFrom == inTo && shaFrom == shaTo {
		return "", nil
	}
	mv, err := w.moves(ctx, arr, from, to, lists)
	if err != nil {
		return "", err
	}
	p, at := w.cfPath(arr, stem), to
	var lines []string
	switch {
	case !inFrom:
		lines, err = w.addedLines(ctx, arr, stem, from, to, mv.from[stem])
	case !inTo:
		at, lines = from, w.removedLines(arr, to, mv.to[stem])
	default:
		lines, err = w.changedLines(ctx, p, from, to)
	}
	if err != nil || len(lines) == 0 {
		return "", err
	}
	return "**" + display(arr) + "**, " + w.fileLink(upstreamTrash, at, p) + ":\n\n- " + strings.Join(lines, "\n- "), nil
}

// addedLines describes a file new at to; old is the stem it moved from, or "".
func (w *watcher) addedLines(ctx context.Context, arr, stem, from, to, old string) ([]string, error) {
	cur, err := w.readCF(ctx, w.cfPath(arr, stem), to)
	if err != nil {
		return nil, err
	}
	if old == "" {
		return append([]string{"New file."}, specLines(&cfDoc{}, cur)...), nil
	}
	prev, err := w.readCF(ctx, w.cfPath(arr, old), from)
	if err != nil {
		return nil, err
	}
	return append([]string{"Moved from " + w.fileLink(upstreamTrash, from, w.cfPath(arr, old)) + "."}, specLines(prev, cur)...), nil
}

// removedLines describes a file gone at to; dest is the stem it moved to,
// or "". A move to another watched stem is reported there, so it is nil.
func (w *watcher) removedLines(arr, to, dest string) []string {
	switch {
	case dest == "":
		return []string{"Removed."}
	case w.imported[arr][dest]:
		return []string{"Moved to " + w.fileLink(upstreamTrash, to, w.cfPath(arr, dest)) + ", a path the import reads."}
	}
	return nil
}

func (w *watcher) changedLines(ctx context.Context, p, from, to string) ([]string, error) {
	prev, err := w.readCF(ctx, p, from)
	if err != nil {
		return nil, err
	}
	cur, err := w.readCF(ctx, p, to)
	if err != nil {
		return nil, err
	}
	return specLines(prev, cur), nil
}

func (w *watcher) cfPath(arr, stem string) string { return w.dirs[arr] + "/" + stem + ".json" }

// moves pairs the files removed and added in arr's directory between from
// and to by trash_id, so a rename reads as one move.
type moves struct {
	to   map[string]string // removed stem -> added stem
	from map[string]string // added stem -> removed stem
}

func (w *watcher) moves(ctx context.Context, arr, from, to string, lists [2]map[string]string) (moves, error) {
	id := arr + "@" + from + ".." + to
	if mv, ok := w.moved[id]; ok {
		return mv, nil
	}
	removed, err := w.trashIDs(ctx, arr, from, lists[0], lists[1])
	if err != nil {
		return moves{}, err
	}
	added, err := w.trashIDs(ctx, arr, to, lists[1], lists[0])
	if err != nil {
		return moves{}, err
	}
	mv := moves{to: map[string]string{}, from: map[string]string{}}
	for tid, old := range removed {
		if dest, ok := added[tid]; ok && old != "" && dest != "" {
			mv.to[old], mv.from[dest] = dest, old
		}
	}
	w.moved[id] = mv
	return mv, nil
}

// trashIDs maps the trash_id of each stem in list but not in other to its
// stem; an id two stems share maps to "".
func (w *watcher) trashIDs(ctx context.Context, arr, ref string, list, other map[string]string) (map[string]string, error) {
	ids := map[string]string{}
	for stem := range list {
		if _, ok := other[stem]; ok {
			continue
		}
		b, err := w.up.file(ctx, w.repos[upstreamTrash], ref, w.cfPath(arr, stem))
		if err != nil {
			return nil, err
		}
		var cf struct {
			TrashID string `json:"trash_id"`
		}
		if json.Unmarshal(b, &cf) != nil || cf.TrashID == "" {
			continue
		}
		if _, dup := ids[cf.TrashID]; dup {
			stem = ""
		}
		ids[cf.TrashID] = stem
	}
	return ids, nil
}

// cfDoc is a CF as the watch compares it: its name and specifications,
// without scores, descriptions or key order.
type cfDoc struct {
	err   error
	name  string
	specs []cfView
}

type cfView struct {
	value          *json.RawMessage // fields.value, when it is the only field
	name, impl     string
	fields         string // canonical JSON
	negate, needed bool
}

func (v *cfView) canonical() string {
	return fmt.Sprintf("%q %q %t %t %s", v.name, v.impl, v.negate, v.needed, v.fields)
}

func (w *watcher) readCF(ctx context.Context, p, ref string) (*cfDoc, error) {
	b, err := w.up.file(ctx, w.repos[upstreamTrash], ref, p)
	if err != nil {
		return nil, err
	}
	return parseCF(b), nil
}

func parseCF(b []byte) *cfDoc {
	var raw struct {
		Name           string `json:"name"`
		Specifications []struct {
			Name           string          `json:"name"`
			Implementation string          `json:"implementation"`
			Fields         json.RawMessage `json:"fields"`
			Negate         bool            `json:"negate"`
			Required       bool            `json:"required"`
		} `json:"specifications"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return &cfDoc{err: err}
	}
	doc := &cfDoc{name: raw.Name}
	for _, s := range raw.Specifications {
		v := cfView{name: s.Name, impl: s.Implementation, negate: s.Negate, needed: s.Required}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(s.Fields, &fields); err != nil {
			return &cfDoc{err: fmt.Errorf("spec %q: fields: %w", s.Name, err)}
		}
		canon, err := json.Marshal(fields)
		if err != nil {
			return &cfDoc{err: err}
		}
		v.fields = string(canon)
		if val, ok := fields["value"]; ok && len(fields) == 1 {
			v.value = &val
		}
		doc.specs = append(doc.specs, v)
	}
	return doc
}

// specLines lists how cur's name and specifications differ from prev's, nil
// when they agree; a zero prev lists each of cur's as added.
func specLines(prev, cur *cfDoc) []string {
	if cur.err != nil {
		return []string{"The file is not a readable custom format: " + codeSpan(cur.err.Error()) + "."}
	}
	var lines []string
	if prev.err == nil && prev.name != "" && prev.name != cur.name {
		lines = append(lines, "Name changed from "+codeSpan(prev.name)+" to "+codeSpan(cur.name)+".")
	}
	before, after := prev.byName(), cur.byName()
	keys := slices.Collect(maps.Keys(before))
	for _, key := range slices.Sorted(maps.Keys(after)) {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		o, inPrev := before[key]
		n, inCur := after[key]
		switch {
		case !inPrev:
			lines = append(lines, specLabel(key, n)+": added, "+fieldText(n)+".")
		case !inCur:
			lines = append(lines, specLabel(key, o)+": removed.")
		case o.canonical() != n.canonical():
			lines = append(lines, specLabel(key, n)+": "+strings.Join(specChanges(o, n), "; ")+".")
		}
	}
	return lines
}

// byName keys specifications by name, a repeated name by its occurrence.
func (d *cfDoc) byName() map[string]*cfView {
	out := map[string]*cfView{}
	if d.err != nil {
		return out
	}
	count := map[string]int{}
	for i := range d.specs {
		v := &d.specs[i]
		count[v.name]++
		key := v.name
		if count[v.name] > 1 {
			key = fmt.Sprintf("%s\x00%d", v.name, count[v.name])
		}
		out[key] = v
	}
	return out
}

func specLabel(key string, v *cfView) string {
	label := codeSpan(v.name)
	if _, n, ok := strings.Cut(key, "\x00"); ok {
		label += " (occurrence " + n + ")"
	}
	attrs := []string{codeSpan(v.impl)}
	if v.needed {
		attrs = append(attrs, "required")
	}
	if v.negate {
		attrs = append(attrs, "negated")
	}
	return label + " (" + strings.Join(attrs, ", ") + ")"
}

func fieldText(v *cfView) string {
	if v.value == nil {
		return "fields " + codeSpan(v.fields)
	}
	return "value " + valueText(*v.value)
}

func valueText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return codeSpan(s)
	}
	return codeSpan(string(raw))
}

func specChanges(o, n *cfView) []string {
	var out []string
	if o.impl != n.impl {
		out = append(out, "implementation changed from "+codeSpan(o.impl)+" to "+codeSpan(n.impl))
	}
	if o.needed != n.needed {
		out = append(out, flip(n.needed, "required"))
	}
	if o.negate != n.negate {
		out = append(out, flip(n.negate, "negated"))
	}
	switch {
	case o.fields == n.fields:
	case o.value != nil && n.value != nil:
		out = append(out, "value changed from "+valueText(*o.value)+" to "+valueText(*n.value))
	default:
		out = append(out, "fields changed from "+codeSpan(o.fields)+" to "+codeSpan(n.fields))
	}
	return out
}

func flip(now bool, what string) string {
	if now {
		return "now " + what
	}
	return "no longer " + what
}

func (w *watcher) logicReport(ctx context.Context, k watchKey, from, to string) (*report, error) {
	arr := k.upstream
	r := &report{
		key:    k,
		title:  display(arr) + " " + path.Base(k.name) + " logic changed outside the imported patterns",
		labels: []string{labelWatch, "upstream: " + arr, labelCandidate},
		intro:  display(arr) + " changed parsing logic outside the patterns subflux imports from this file.",
		file:   w.fileLink(arr, to, k.name),
		why:    "a parsing change outside the imported patterns can change how releases are read.",
		footer: "Comment and whitespace changes are ignored. Close this issue after review; a later change reopens it.",
	}
	prev, prevErr := w.up.file(ctx, w.repos[arr], from, k.name)
	cur, curErr := w.up.file(ctx, w.repos[arr], to, k.name)
	for _, err := range []error{prevErr, curErr} {
		if err != nil && !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	switch {
	case prevErr != nil && curErr != nil:
		return r, nil
	case curErr != nil:
		r.sections = []string{"The file is gone at " + to[:7] + ": removed or moved."}
		return r, nil
	case prevErr != nil:
		r.sections = []string{"The file is new since " + from[:7] + "."}
		return r, nil
	}
	a, aErr := w.codeLines(prev, k)
	b, bErr := w.codeLines(cur, k)
	if err := errors.Join(aErr, bErr); err != nil {
		if !bytes.Equal(prev, cur) {
			r.sections = []string{"The file could not be read as C#, so the watch compared its bytes: " + codeSpan(err.Error()) + "."}
		}
		return r, nil
	}
	if d := lineDiff(a, b); len(d) > 0 {
		r.sections = []string{"Changed lines, leaving out comments, blank lines and the imported declarations:\n\n" + codeBlock("diff", d, maxDiffLines)}
	}
	return r, nil
}

// codeLines is src's code with comments and the manifest's declarations
// from this file blanked, one whitespace-collapsed line per non-blank line.
// A declaration renamed upstream is not found, so it shows as a change.
func (w *watcher) codeLines(src []byte, k watchKey) ([]string, error) {
	toks, spans, err := lexSpans(string(src))
	if err != nil {
		return nil, err
	}
	text := []byte(string(src))
	for _, sp := range append(spans, w.declSpans(toks, k)...) {
		for i := sp[0]; i < sp[1]; i++ {
			if text[i] != '\n' {
				text[i] = ' '
			}
		}
	}
	var lines []string
	for line := range strings.SplitSeq(string(text), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			lines = append(lines, strings.Join(f, " "))
		}
	}
	return lines, nil
}

// declSpans are the byte spans of the manifest's declarations from k's file.
func (w *watcher) declSpans(toks []csToken, k watchKey) [][2]int {
	var spans [][2]int
	for i := range w.m.Symbols {
		s := &w.m.Symbols[i]
		if s.Upstream != k.upstream || s.Path != k.name {
			continue
		}
		if at := findDecl(toks, s.Symbol, declTypes[s.Kind]); at >= 2 {
			spans = append(spans, [2]int{toks[at-2].start, declEnd(toks, at, s.Kind == kindEnum)})
		}
	}
	return spans
}

// declEnd is the byte offset after the declaration starting at toks[at]:
// its closing brace for an enum, else its semicolon, outside brackets.
func declEnd(toks []csToken, at int, enum bool) int {
	depth := 0
	for _, t := range toks[at:] {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "(", "{", "[":
			depth++
		case ")", "}", "]":
			depth--
			if enum && depth == 0 && t.text == "}" {
				return t.end
			}
		case ";":
			if depth == 0 {
				return t.end
			}
		}
	}
	return toks[len(toks)-1].end
}
