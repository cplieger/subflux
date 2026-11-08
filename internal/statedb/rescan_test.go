package statedb

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

var rescanSeed = flag.Uint64("rescan.seed", 0, "run TestTablesAgainstRescan with this seed only")

// rescanSeeds are the fixed sequences every run checks.
var rescanSeeds = []uint64{1, 2, 3, 5, 8, 13, 21, 34}

// TestTablesAgainstRescan runs random observe, finish, activate,
// write-protocol, lock and download sequences, and after every operation
// checks the store against a model of the sightings the sources reported,
// an independent coverage recomputation, the change feed, the
// append-only log and every parent a row depends on.
func TestTablesAgainstRescan(t *testing.T) {
	seeds := rescanSeeds
	if *rescanSeed != 0 {
		seeds = []uint64{*rescanSeed}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			r := newRescan(t, seed)
			for step := range 40 {
				name := r.step()
				t.Log(step, name)
				r.check(fmt.Sprintf("step %d (%s)", step, name))
				if t.Failed() {
					t.Fatalf("seed %d stopped at step %d; rerun with -rescan.seed=%d", seed, step, seed)
				}
			}
		})
	}
}

// rescanRoot is one series every source may report, under its own native
// id per source.
type rescanRoot struct {
	tvdb uint64
	name string
}

var (
	rescanSources = []media.SourceID{"sonarr", "sonarr-b"}
	rescanRoots   = []rescanRoot{{name: "ff", tvdb: fireflyTVDB}, {name: "a", tvdb: 100}}
	rescanTags    = []string{"en", "fr", "pt-BR", "pt-AO", "ja"}
)

type rescan struct {
	t      *testing.T
	s      *Store
	clock  *testClock
	rng    *rand.Rand
	act    Activation
	policy map[string]language.Policy
	// seen is the model: every sighting the sources reported and the
	// store must hold.
	seen map[itemRef]bool
	// log is the download log after the previous step, by sequence.
	log map[uint64]string
	n   int
}

func newRescan(t *testing.T, seed uint64) *rescan {
	s, clock := newLibrary(t, rescanSources...)
	r := &rescan{
		t: t, s: s, clock: clock, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		policy: map[string]language.Policy{"en": pol(t, "default:\n  - tag: en\n  - tag: pt-BR\n"), "audio": pol(t, audioRules)},
		seen:   map[itemRef]bool{}, log: map[uint64]string{},
	}
	r.act = Activation{Sources: rescanSources, Languages: r.policy["en"]}
	activate(t, s, r.act)
	return r
}

func (r *rescan) pick(n int) int { return r.rng.IntN(n) }

func (r *rescan) chance(p float64) bool { return r.rng.Float64() < p }

// step runs one random operation and answers its name. Before it runs, it
// snapshots what the feed check compares.
func (r *rescan) step() string {
	before := r.snapshot()
	head := headRev(r.t, r.s)
	var name string
	switch n := r.pick(100); {
	case n < 35:
		name = r.pass()
	case n < 42:
		name = r.activate()
	case n < 55:
		name = r.inventory()
	case n < 67:
		name = r.lock()
	case n < 78:
		name = r.writeProtocol()
	case n < 82:
		name = r.noteRefusal()
	case n < 90:
		name = r.download()
	default:
		days := []int{1, 29, 31}[r.pick(3)]
		r.clock.advance(time.Duration(days) * 24 * time.Hour)
		name = fmt.Sprintf("advance %d days", days)
	}
	r.checkFeed(name, head, before)
	return name
}

// pass runs a whole pass or a change batch of src. A complete one
// retires every sighting in its scope that it did not observe; an
// incomplete one retires nothing (design 5.3).
func (r *rescan) pass() string {
	if len(r.act.Sources) == 0 {
		return "pass: no active source"
	}
	src := r.act.Sources[r.pick(len(r.act.Sources))]
	var scope PassScope
	inScope := func(rescanRoot) bool { return true }
	if r.chance(0.3) {
		batch := map[string]bool{}
		for _, root := range rescanRoots {
			if r.chance(0.6) {
				batch[root.name] = true
				scope.Roots = append(scope.Roots, mediasource.NativeRef{ID: mediasource.NativeID(string(src) + "-" + root.name), Kind: media.Episode})
			}
		}
		if len(scope.Roots) == 0 {
			batch[rescanRoots[0].name] = true
			scope.Roots = append(scope.Roots, mediasource.NativeRef{ID: mediasource.NativeID(string(src) + "-" + rescanRoots[0].name), Kind: media.Episode})
		}
		inScope = func(root rescanRoot) bool { return batch[root.name] }
	}
	p, err := r.s.BeginPass(r.t.Context(), src, scope)
	if err != nil {
		r.t.Fatalf("BeginPass(%s, %v): %v", src, scope, err)
	}
	observed := map[itemRef]bool{}
	scoped := map[rootRef]bool{}
	for _, root := range rescanRoots {
		if !inScope(root) {
			continue
		}
		mr, items := r.report(src, root)
		ref := rootRef{Source: src, Root: mr.Native}
		scoped[ref] = true
		if !r.chance(0.7) {
			continue
		}
		if err := r.s.ObserveItems(r.t.Context(), src, p, mr, items); err != nil {
			r.t.Fatalf("ObserveItems(%s, %s): %v", src, mr.Native.ID, err)
		}
		for _, it := range items {
			k := itemRef{rootRef: ref, Item: it.Native}
			observed[k], r.seen[k] = true, true
		}
	}
	complete := r.chance(0.75)
	if _, err := r.s.FinishPass(r.t.Context(), src, p, complete, mediasource.Cursor{}, nil); err != nil {
		r.t.Fatalf("FinishPass(%s, %v): %v", src, complete, err)
	}
	if complete {
		maps.DeleteFunc(r.seen, func(k itemRef, _ bool) bool {
			return k.Source == src && (scope.Roots == nil || scoped[k.rootRef]) && !observed[k]
		})
	}
	return fmt.Sprintf("%s pass, scope %d roots, complete %v, %d items", src, len(scope.Roots), complete, len(observed))
}

// report is src's random report of root: up to three episodes, each with
// no video, a file of its own or the double-episode file.
func (r *rescan) report(src media.SourceID, root rescanRoot) (mediasource.Root, []mediasource.Item) {
	mr := series(string(src)+"-"+root.name, root.tvdb)
	if root.tvdb == fireflyTVDB && r.chance(0.5) {
		mr.Evidence = fireflyEvidence()
	}
	var items []mediasource.Item
	for ep := 1; ep <= 3; ep++ {
		if !r.chance(0.8) {
			continue
		}
		var path string
		switch r.pick(4) {
		case 1:
			path = fmt.Sprintf("/tv/%s/%d.mkv", root.name, ep)
		case 2:
			path = fmt.Sprintf("/tv/%s/%d.v2.mkv", root.name, ep)
		case 3:
			path = fmt.Sprintf("/tv/%s/1-2.mkv", root.name)
		}
		it := episode(fmt.Sprintf("%s-e%d", root.name, ep), 1, ep, path)
		if r.chance(0.4) {
			it.AudioLangs = []language.Tag{mustTag(r.t, "ja")}
		}
		it.Excluded = r.chance(0.15)
		items = append(items, it)
	}
	return mr, items
}

func (r *rescan) activate() string {
	var sources []media.SourceID
	for _, src := range rescanSources {
		if r.chance(0.7) {
			sources = append(sources, src)
		}
	}
	policy := []string{"en", "audio"}[r.pick(2)]
	var codecs []string
	if r.chance(0.5) {
		codecs = []string{pgs}
	}
	r.act = Activation{Sources: sources, Languages: r.policy[policy], IgnoredCodecs: codecs}
	activate(r.t, r.s, r.act)
	maps.DeleteFunc(r.seen, func(k itemRef, _ bool) bool { return !slices.Contains(sources, k.Source) })
	return fmt.Sprintf("activate %v, policy %s, codecs %v", sources, policy, codecs)
}

// owned answers a random owned instance and its item keys; false when
// none exists.
func (r *rescan) owned() (InstanceID, []media.Key, bool) {
	var ids []InstanceID
	rows := map[InstanceID]instanceRow{}
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		return r.s.t.instances.Scan(rtx, bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
			if v.Owner != "" {
				ids = append(ids, id)
				rows[id] = *v
			}
			return true, nil
		})
	}); err != nil {
		r.t.Fatal(err)
	}
	if len(ids) == 0 {
		return 0, nil, false
	}
	id := ids[r.pick(len(ids))]
	keys := make([]media.Key, 0, len(rows[id].Items))
	for _, x := range rows[id].Items {
		keys = append(keys, x.k)
	}
	return id, keys, true
}

func (r *rescan) tag() language.Tag { return mustTag(r.t, rescanTags[r.pick(len(rescanTags))]) }

func (r *rescan) inventory() string {
	id, _, ok := r.owned()
	if !ok {
		return "inventory: no owned instance"
	}
	if r.chance(0.5) {
		var tracks []EmbeddedTrack
		for i := range r.pick(3) {
			tracks = append(tracks, EmbeddedTrack{Tag: r.tag(), Codec: []string{"subrip", pgs}[r.pick(2)], Index: i + 2})
		}
		putEmbedded(r.t, r.s, id, tracks...)
		return fmt.Sprintf("PutEmbedded %d, %d tracks", id, len(tracks))
	}
	var files []LocalFile
	for i := range r.pick(3) {
		files = append(files, LocalFile{Name: fmt.Sprintf("local%d.srt", i), Tag: r.tag()})
	}
	putExternal(r.t, r.s, id, files...)
	return fmt.Sprintf("PutExternal %d, %d files", id, len(files))
}

// lock locks a pair some evidence of a random owned instance serves, or
// clears one of its locks, falling back to a random pair, which SetLock may
// refuse.
func (r *rescan) lock() string {
	id, keys, ok := r.owned()
	if !ok {
		return "lock: no owned instance"
	}
	locking := r.chance(0.6)
	var pairs []lockKey
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		for _, k := range keys {
			if !locking {
				if err := r.s.t.locks.Scan(rtx, bolttable.Range{Prefix: k.Bytes()}, func(lk lockKey, _ *lockRow) (bool, error) {
					pairs = append(pairs, lk)
					return true, nil
				}); err != nil {
					return err
				}
				continue
			}
			cov, _, err := r.s.t.coverage.Get(rtx, k)
			if err != nil {
				return err
			}
			for _, p := range cov.Pairs {
				if Coverage(p.Coverage) != Missing {
					pairs = append(pairs, lockKey{Key: k, Tag: p.Tag.t, Variant: language.Variant(p.Variant)})
				}
			}
		}
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
	tag, v := r.tag(), language.Standard
	if len(pairs) > 0 && r.chance(0.85) {
		p := pairs[r.pick(len(pairs))]
		tag, v = p.Tag, p.Variant
	}
	err := r.s.SetLock(r.t.Context(), id, tag, v, locking)
	if err != nil && !errors.Is(err, ErrNotLockable) {
		r.t.Fatalf("SetLock(%d, %s, %v): %v", id, tag, locking, err)
	}
	return fmt.Sprintf("SetLock %d %s %d %v: %v", id, tag, v, locking, err)
}

// writeProtocol delivers, retimes or deletes one file record through its
// Begin and End, landed or not.
func (r *rescan) writeProtocol() string {
	id, keys, ok := r.owned()
	if !ok {
		return "write: no owned instance"
	}
	landed := r.chance(0.8)
	names := r.fileNames(id)
	ctx := r.t.Context()
	switch {
	case len(names) > 0 && r.chance(0.4):
		name := names[r.pick(len(names))]
		if err := r.s.BeginDelete(ctx, id, name, [32]byte{1}); err != nil {
			r.t.Fatalf("BeginDelete(%d, %s): %v", id, name, err)
		}
		if err := r.s.EndDelete(ctx, id, name, landed); err != nil {
			r.t.Fatalf("EndDelete(%d, %s): %v", id, name, err)
		}
		return fmt.Sprintf("delete %d %s, landed %v", id, name, landed)
	default:
		r.n++
		d := pendingDeliver(r.t, id, fmt.Sprintf("d%d.en.srt", r.n), byte(r.n), keys[:1+r.pick(len(keys))]...)
		d.Record.Tag = r.tag()
		d.Record.Locked = r.chance(0.5)
		d.Attempts[0].Target = d.Record.Tag
		if err := r.s.BeginDeliver(ctx, d); err != nil {
			r.t.Fatalf("BeginDeliver(%d): %v", id, err)
		}
		if err := r.s.EndDeliver(ctx, id, d.Record.Name, landed); err != nil {
			r.t.Fatalf("EndDeliver(%d): %v", id, err)
		}
		return fmt.Sprintf("deliver %d %s %s, landed %v", id, d.Record.Name, d.Record.Tag, landed)
	}
}

func (r *rescan) noteRefusal() string {
	id, _, ok := r.owned()
	if !ok {
		return "refusal: no owned instance"
	}
	tag := r.tag()
	if err := r.s.NoteRefusal(r.t.Context(), id, tag, language.Standard, "different_cut"); err != nil {
		r.t.Fatalf("NoteRefusal(%d, %s): %v", id, tag, err)
	}
	return fmt.Sprintf("NoteRefusal %d %s", id, tag)
}

func (r *rescan) fileNames(id InstanceID) []string {
	var names []string
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		return r.s.t.files.Scan(rtx, bolttable.Range{Prefix: appendUint64(nil, uint64(id))}, func(k fileKey, _ *fileRow) (bool, error) {
			names = append(names, k.Name)
			return true, nil
		})
	}); err != nil {
		r.t.Fatal(err)
	}
	return names
}

func (r *rescan) download() string {
	id, keys, ok := r.owned()
	if !ok {
		return "download: no owned instance"
	}
	a := attemptOf(id, false, keys...)
	a.Target = r.tag()
	if err := r.s.RecordAttempts(r.t.Context(), []Download{a}); err != nil {
		r.t.Fatalf("RecordAttempts(%d): %v", id, err)
	}
	return fmt.Sprintf("RecordAttempts %d", id)
}

// rescanSnapshot is the rows the feed check compares across one step: the
// item, coverage and lock rows by key, the file rows by instance, and each
// instance's keys.
type rescanSnapshot struct {
	keyed map[media.Key]string
	files map[InstanceID]string
	held  map[InstanceID][]media.Key
}

func (r *rescan) snapshot() rescanSnapshot {
	sn := rescanSnapshot{keyed: map[media.Key]string{}, files: map[InstanceID]string{}, held: map[InstanceID][]media.Key{}}
	add := func(k media.Key, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			r.t.Fatal(err)
		}
		sn.keyed[k] += string(b)
	}
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		if err := r.s.t.items.Scan(rtx, bolttable.Range{}, func(k media.Key, v *itemRow) (bool, error) {
			add(k, v)
			return true, nil
		}); err != nil {
			return err
		}
		if err := r.s.t.coverage.Scan(rtx, bolttable.Range{}, func(k media.Key, v *coverageRow) (bool, error) {
			add(k, v)
			return true, nil
		}); err != nil {
			return err
		}
		if err := r.s.t.locks.Scan(rtx, bolttable.Range{}, func(k lockKey, _ *lockRow) (bool, error) {
			add(k.Key, k.Tag.String())
			return true, nil
		}); err != nil {
			return err
		}
		if err := r.s.t.instances.Scan(rtx, bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
			for _, x := range v.Items {
				sn.held[id] = append(sn.held[id], x.k)
			}
			return true, nil
		}); err != nil {
			return err
		}
		return r.s.t.files.Scan(rtx, bolttable.Range{}, func(k fileKey, v *fileRow) (bool, error) {
			b, err := json.Marshal(v)
			sn.files[k.Inst] += k.Name + string(b)
			return true, err
		})
	}); err != nil {
		r.t.Fatal(err)
	}
	return sn
}

// checkFeed requires every root whose item, coverage or lock rows changed
// in the step in Since's Roots, and every root of an instance whose file
// records changed in its Written.
func (r *rescan) checkFeed(step string, head uint64, before rescanSnapshot) {
	after := r.snapshot()
	d := since(r.t, r.s, head)
	if d.Full {
		return
	}
	for _, k := range slices.Collect(maps.Keys(unionKeys(before.keyed, after.keyed))) {
		if before.keyed[k] != after.keyed[k] && !slices.Contains(d.Roots, k.Root()) {
			r.t.Errorf("%s: %s's rows changed and Since lists Roots %v", step, k, d.Roots)
		}
	}
	for _, id := range slices.Collect(maps.Keys(unionKeys(before.files, after.files))) {
		if before.files[id] == after.files[id] {
			continue
		}
		for _, k := range slices.Concat(before.held[id], after.held[id]) {
			if !slices.Contains(d.Written, k.Root()) {
				r.t.Errorf("%s: instance %d's file records changed and Since lists Written %v, not %s", step, id, d.Written, k.Root())
			}
		}
	}
}

func unionKeys[K comparable, V any](a, b map[K]V) map[K]bool {
	out := map[K]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// check runs every invariant after a step.
func (r *rescan) check(step string) {
	if in := inspect(r.t, r.s, InspectOptions{Verify: true}); len(in.Mismatches) != 0 {
		r.t.Errorf("%s: Inspect(Verify) = %+v, want none", step, in.Mismatches)
	}
	st := r.load()
	if got := slices.Collect(maps.Keys(st.sightings)); !maps.Equal(r.seen, setOf(got)) {
		r.t.Errorf("%s: the store holds %d sightings, the model %d", step, len(got), len(r.seen))
	}
	st.checkParents(r.t, step)
	r.checkCounts(step, &st, r.checkCoverage(step, &st))
	r.checkLog(step, &st)
	checkEvidenceHasRoot(r.t, r.s, step)
}

func setOf[K comparable](ks []K) map[K]bool {
	out := make(map[K]bool, len(ks))
	for _, k := range ks {
		out[k] = true
	}
	return out
}

// rescanState is every core row the invariants read, from one View.
type rescanState struct {
	roots     map[rootRef]rootRow
	sightings map[itemRef]sightingRow
	items     map[media.Key]itemRow
	instances map[InstanceID]instanceRow
	coverage  map[media.Key]coverageRow
	files     map[fileKey]fileRow
	local     map[InstanceID]localRow
	embedded  map[InstanceID]embeddedRow
	downloads map[uint64]string
	counts    map[media.RootKey]countsRow
	children  []rescanChild
	locks     []lockKey
}

// rescanChild is a row that exists only beside its parent.
type rescanChild struct {
	table  string
	exists func(*rescanState) bool
}

func scanInto[K comparable, V any](rtx *bolttable.ReadTx, tb *bolttable.Table[K, V], dst map[K]V) error {
	return tb.Scan(rtx, bolttable.Range{}, func(k K, v *V) (bool, error) {
		dst[k] = *v
		return true, nil
	})
}

// child scans tb and records, per row, the check that its parent exists.
func child[K, V any](rtx *bolttable.ReadTx, st *rescanState, tb *bolttable.Table[K, V], table string, parent func(*rescanState, K) bool) error {
	return tb.Scan(rtx, bolttable.Range{}, func(k K, _ *V) (bool, error) {
		st.children = append(st.children, rescanChild{table: fmt.Sprintf("%s %v", table, k), exists: func(st *rescanState) bool { return parent(st, k) }})
		return true, nil
	})
}

func (r *rescan) load() rescanState {
	st := rescanState{
		roots: map[rootRef]rootRow{}, sightings: map[itemRef]sightingRow{}, items: map[media.Key]itemRow{},
		instances: map[InstanceID]instanceRow{}, coverage: map[media.Key]coverageRow{}, files: map[fileKey]fileRow{},
		local: map[InstanceID]localRow{}, embedded: map[InstanceID]embeddedRow{}, downloads: map[uint64]string{},
	}
	st.counts = map[media.RootKey]countsRow{}
	t := r.s.t
	hasSighting := func(st *rescanState, k itemRef) bool { _, ok := st.sightings[k]; return ok }
	hasInstance := func(st *rescanState, id InstanceID) bool { _, ok := st.instances[id]; return ok }
	hasItem := func(st *rescanState, k media.Key) bool { _, ok := st.items[k]; return ok }
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		return errors.Join(
			scanInto(rtx, t.roots, st.roots), scanInto(rtx, t.sightings, st.sightings), scanInto(rtx, t.items, st.items),
			scanInto(rtx, t.instances, st.instances), scanInto(rtx, t.coverage, st.coverage), scanInto(rtx, t.files, st.files),
			scanInto(rtx, t.local, st.local), scanInto(rtx, t.embedded, st.embedded), scanInto(rtx, t.counts, st.counts),
			t.downloads.Scan(rtx, bolttable.Range{}, func(seq uint64, v *downloadRow) (bool, error) {
				b, err := json.Marshal(v)
				st.downloads[seq] = string(b)
				return true, err
			}),
			t.locks.Scan(rtx, bolttable.Range{}, func(k lockKey, _ *lockRow) (bool, error) {
				st.locks = append(st.locks, k)
				return true, nil
			}),
			child(rtx, &st, t.stamps, "stamp", func(st *rescanState, k itemRef) bool {
				if k.Item == "" {
					_, ok := st.roots[k.rootRef]
					return ok
				}
				return hasSighting(st, k)
			}),
			child(rtx, &st, t.overlaps, "overlap", hasSighting),
			child(rtx, &st, t.sightings, "sighting", func(st *rescanState, k itemRef) bool { _, ok := st.roots[k.rootRef]; return ok }),
			child(rtx, &st, t.coverage, "coverage", hasItem),
			child(rtx, &st, t.noResults, "no_results", func(st *rescanState, k noResultKey) bool { return hasItem(st, k.Key) }),
			child(rtx, &st, t.files, "file", func(st *rescanState, k fileKey) bool { return hasInstance(st, k.Inst) }),
			child(rtx, &st, t.baselines, "baseline", func(st *rescanState, k fileKey) bool { return hasInstance(st, k.Inst) }),
			child(rtx, &st, t.ordinals, "ordinal", func(st *rescanState, k OrdinalKey) bool { return hasInstance(st, k.Inst) }),
			child(rtx, &st, t.local, "local", hasInstance),
			child(rtx, &st, t.embedded, "embedded", hasInstance),
			child(rtx, &st, t.work, "work", hasInstance),
		)
	}); err != nil {
		r.t.Fatal(err)
	}
	return st
}

// checkParents fails for a row whose parent is gone: a companion row
// without its sighting, root, instance or item record, an owned or
// no-video item record naming a gone sighting or an instance not holding
// it, an instance whose item names another, and a lock with no item
// record. An ownerless instance's records name their removed source's
// sightings by design (5.4).
func (st *rescanState) checkParents(t *testing.T, step string) {
	t.Helper()
	for _, c := range st.children {
		if !c.exists(st) {
			t.Errorf("%s: %s has no parent row", step, c.table)
		}
	}
	for k, rec := range st.items {
		if inst, ok := st.instances[InstanceID(rec.Inst)]; ok && inst.Owner == "" {
			continue
		}
		if _, ok := st.sightings[rec.ref()]; !ok {
			t.Errorf("%s: item %s names sighting %v, which is gone", step, k, rec.ref())
		}
		if inst, ok := st.instances[InstanceID(rec.Inst)]; rec.Inst != 0 && (!ok || !slices.ContainsFunc(inst.Items, func(x rowKey) bool { return x.k == k })) {
			t.Errorf("%s: item %s names instance %d, which does not hold it", step, k, rec.Inst)
		}
	}
	for id, inst := range st.instances {
		for _, x := range inst.Items {
			if rec, ok := st.items[x.k]; !ok || InstanceID(rec.Inst) != id {
				t.Errorf("%s: instance %d (owner %q) holds %s, whose record (found %v) names instance %d of %s", step, id, inst.Owner, x.k, ok, rec.Inst, rec.Source)
			}
		}
	}
	for _, lk := range st.locks {
		if _, ok := st.items[lk.Key]; !ok {
			t.Errorf("%s: a lock on %s has no item record", step, lk.Key)
		}
	}
}

// checkCoverage recomputes every owned item's coverage from design section
// 6 alone and compares it with the stored row, refusals aside, requires
// every lock of an owned item to be on a configured pair, and answers the
// recomputed pairs by key.
func (r *rescan) checkCoverage(step string, st *rescanState) map[media.Key][]coveragePair {
	wants := map[media.Key][]coveragePair{}
	policy := r.act.Languages
	for k, rec := range st.items {
		inst, held := st.instances[InstanceID(rec.Inst)]
		if held && inst.Owner == "" {
			continue
		}
		sg := st.sightings[rec.ref()]
		var audio []language.Tag
		for _, a := range sg.AudioLangs {
			audio = append(audio, a.t)
		}
		ev := st.evidence(InstanceID(rec.Inst), r.act.IgnoredCodecs)
		var want []coveragePair
		for _, target := range policy.TargetsFor(sg.OriginalLang.t, audio) {
			for _, v := range target.Variants {
				c := Missing
				for _, e := range ev {
					if !e.flags.Serves(v) {
						continue
					}
					switch policy.Grade(target.Tag, e.tag).Class {
					case language.Exact:
						c = Exact
					case language.Substitute:
						c = max(c, Substitute)
					case language.ManualOnly, language.ClassUnrelated:
					}
				}
				want = append(want, coveragePair{Tag: rowTag{t: target.Tag}, Variant: uint8(v), Coverage: uint8(c)})
			}
		}
		wants[k] = want
		got, stored := st.coverage[k]
		plain := make([]coveragePair, 0, len(got.Pairs))
		for _, p := range got.Pairs {
			p.LastRefusal = ""
			plain = append(plain, p)
		}
		if stored != (len(want) > 0) || !slices.Equal(plain, want) {
			r.t.Errorf("%s: %s coverage %+v (stored %v), want %+v", step, k, got.Pairs, stored, want)
		}
		for _, lk := range st.locks {
			if lk.Key == k && !slices.ContainsFunc(want, func(p coveragePair) bool { return p.Tag.t == lk.Tag && p.Variant == uint8(lk.Variant) }) {
				r.t.Errorf("%s: %s keeps a lock on %s %d, which it does not configure", step, k, lk.Tag, lk.Variant)
			}
		}
	}
	return wants
}

// checkCounts recounts every root from the recomputed coverage and the
// locks of its items that name an owned instance and a sighting not
// excluded, per (tag, variant), and compares it with the stored counts.
func (r *rescan) checkCounts(step string, st *rescanState, wants map[media.Key][]coveragePair) {
	type pairAt struct {
		root media.RootKey
		tag  string
		v    uint8
	}
	want := map[pairAt]countPair{}
	for k, rec := range st.items {
		inst, held := st.instances[InstanceID(rec.Inst)]
		if !held || inst.Owner == "" || st.sightings[rec.ref()].Excluded {
			continue
		}
		for _, p := range wants[k] {
			at := pairAt{k.Root(), p.Tag.t.String(), p.Variant}
			c := want[at]
			c.Tag, c.Variant = p.Tag, p.Variant
			c.Items++
			switch Coverage(p.Coverage) {
			case Exact:
				c.Exact++
			case Substitute:
				c.Substitute++
			case Missing:
				c.Missing++
			}
			if slices.Contains(st.locks, lockKey{Key: k, Tag: p.Tag.t, Variant: language.Variant(p.Variant)}) {
				c.Locked++
			}
			want[at] = c
		}
	}
	got := map[pairAt]countPair{}
	for root, row := range st.counts {
		for _, p := range row.Pairs {
			got[pairAt{root, p.Tag.t.String(), p.Variant}] = p
		}
	}
	if !maps.Equal(got, want) {
		r.t.Errorf("%s: stored counts %+v, recounted %+v", step, got, want)
	}
}

// evidence is instance id's file records, local files and embedded tracks
// with a tag and a codec outside ignored.
func (st *rescanState) evidence(id InstanceID, ignored []string) []evidence {
	if id == 0 {
		return nil
	}
	var ev []evidence
	for k, f := range st.files {
		if k.Inst == id {
			ev = append(ev, evidence{tag: f.Tag.t, flags: flagsOf(f.Flags)})
		}
	}
	for _, f := range st.local[id].Files {
		ev = append(ev, evidence{tag: f.Tag.t, flags: flagsOf(f.Flags)})
	}
	for _, tr := range st.embedded[id].Tracks {
		if tr.Tag.t != (language.Tag{}) && !slices.Contains(ignored, tr.Codec) {
			ev = append(ev, evidence{tag: tr.Tag.t, flags: flagsOf(tr.Flags)})
		}
	}
	return ev
}

// checkLog requires every download row of the previous step unchanged.
func (r *rescan) checkLog(step string, st *rescanState) {
	for seq, row := range r.log {
		if st.downloads[seq] != row {
			r.t.Errorf("%s: download %d changed or went", step, seq)
		}
	}
	r.log = st.downloads
}
