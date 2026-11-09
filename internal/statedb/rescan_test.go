package statedb

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

var rescanSeed = flag.Uint64("rescan.seed", 0, "run TestTablesAgainstRescan with this seed only")

// rescanSeeds are the fixed sequences every run checks.
var rescanSeeds = []uint64{1, 2, 3, 5, 8, 13, 21, 34}

// TestTablesAgainstRescan runs random observe, finish, activate,
// write-protocol, lock, download and account sequences, and after every
// operation checks the store against a model of the sightings the sources
// reported and the accounts the operations left, an independent coverage
// recomputation, the change feed, the append-only log and every parent a
// row depends on.
func TestTablesAgainstRescan(t *testing.T) {
	seeds := rescanSeeds
	if *rescanSeed != 0 {
		seeds = []uint64{*rescanSeed}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			r := newRescan(t, seed)
			for step := range 60 {
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
	log  map[uint64]string
	acct rescanAccounts
	n    int
}

func newRescan(t *testing.T, seed uint64) *rescan {
	s, clock := newLibrary(t, rescanSources...)
	r := &rescan{
		t: t, s: s, clock: clock, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		policy: map[string]language.Policy{"en": pol(t, "default:\n  - tag: en\n  - tag: pt-BR\n"), "audio": pol(t, audioRules)},
		seen:   map[itemRef]bool{}, log: map[uint64]string{}, acct: newRescanAccounts(),
	}
	r.act = Activation{Sources: rescanSources, Languages: r.policy["en"]}
	activate(t, s, r.act)
	return r
}

func (r *rescan) pick(n int) int { return r.rng.IntN(n) }

func (r *rescan) chance(p float64) bool { return r.rng.Float64() < p }

// step runs one random operation and answers its name. Before it runs, it
// snapshots what the feed checks compare.
func (r *rescan) step() string {
	before, beforeAuth := r.snapshot(), r.authRows()
	head := headRev(r.t, r.s)
	var name string
	var accountStep bool
	switch n := r.pick(100); {
	case n < 26:
		name = r.pass()
	case n < 31:
		name = r.activate()
	case n < 40:
		name = r.inventory()
	case n < 48:
		name = r.lock()
	case n < 56:
		name = r.writeProtocol()
	case n < 59:
		name = r.noteRefusal()
	case n < 64:
		name = r.download()
	case n < 92:
		name, accountStep = r.account(), true
	default:
		days := []int{1, 29, 31}[r.pick(3)]
		r.clock.advance(time.Duration(days) * 24 * time.Hour)
		name = fmt.Sprintf("advance %d days", days)
	}
	r.checkFeed(name, head, before)
	r.checkAuthFeed(name, accountStep, head, beforeAuth)
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
	r.checkAccounts(step)
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

// rescanAccounts is the account model: the rows the account operations
// left, by the IDs the store assigned.
type rescanAccounts struct {
	users    map[int64]string
	sessions map[access.SessionRef]rescanSession
	passkeys map[access.PasskeyRef]rescanPasskey
	keys     map[int64]rescanKey
	tokens   map[string]rescanToken
	attrs    map[int64]int64
	// last is the highest user, key and attribution ID the store assigned:
	// rule 17 never assigns one again.
	last map[string]int64
}

type rescanSession struct {
	created, last time.Time
	hash          string
	idleCap       time.Duration
}

type rescanPasskey struct {
	cred      string
	signCount uint32
}

type rescanKey struct {
	used  time.Time
	owner int64
}

type rescanToken struct {
	expires time.Time
	subject int64
}

func newRescanAccounts() rescanAccounts {
	return rescanAccounts{
		users: map[int64]string{}, sessions: map[access.SessionRef]rescanSession{}, passkeys: map[access.PasskeyRef]rescanPasskey{},
		keys: map[int64]rescanKey{}, tokens: map[string]rescanToken{}, attrs: map[int64]int64{}, last: map[string]int64{},
	}
}

func cmpSessionRef(a, b access.SessionRef) int {
	return cmp.Or(cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.ID, b.ID))
}

func cmpPasskeyRef(a, b access.PasskeyRef) int {
	return cmp.Or(cmp.Compare(a.UserID, b.UserID), cmp.Compare(a.ID, b.ID))
}

// pickOf answers a random key of the non-empty m, drawn in sorted order so
// a seed replays.
func pickOf[K comparable, V any](r *rescan, m map[K]V, order func(a, b K) int) K {
	ks := slices.SortedFunc(maps.Keys(m), order)
	return ks[r.pick(len(ks))]
}

// at is a stamp within an hour either side of the store's clock, so later
// stamps can come before earlier ones and rule 21 decides what stays.
func (r *rescan) at() time.Time {
	return r.clock.Now().Add(time.Duration(r.pick(121)-60) * time.Minute)
}

// account runs one random account operation among those the model holds a
// row for, creates weighted so the others find rows to act on.
func (r *rescan) account() string {
	m := &r.acct
	always := func() bool { return true }
	users := func() bool { return len(m.users) > 0 }
	sessions := func() bool { return len(m.sessions) > 0 }
	passkeys := func() bool { return len(m.passkeys) > 0 }
	keys := func() bool { return len(m.keys) > 0 }
	var ops []func(*AccountStore) string
	for _, op := range []struct {
		run    func(*AccountStore) string
		ready  func() bool
		weight int
	}{
		{r.createUser, always, 3},
		{r.deleteUser, users, 1},
		{r.createSession, users, 3},
		{r.rotateSession, sessions, 3},
		{r.touchSession, sessions, 3},
		{r.expireSessions, sessions, 2},
		{r.deleteSession, sessions, 1},
		{r.createPasskey, users, 2},
		{r.passkeySignIn, passkeys, 2},
		{r.deletePasskey, passkeys, 1},
		{r.createKey, users, 2},
		{r.useKey, keys, 2},
		{r.deleteKey, keys, 1},
		{r.createToken, always, 2},
		{r.consumeToken, always, 2},
		{r.createAttribution, users, 1},
	} {
		if op.ready() {
			for range op.weight {
				ops = append(ops, op.run)
			}
		}
	}
	r.n++
	return ops[r.pick(len(ops))](r.s.Accounts())
}

// assigned checks a create's new ID against rule 17 and records it.
func (r *rescan) assigned(kind string, id int64) {
	if id <= r.acct.last[kind] {
		r.t.Errorf("%s ID %d assigned after %d", kind, id, r.acct.last[kind])
	}
	r.acct.last[kind] = max(r.acct.last[kind], id)
}

func (r *rescan) createUser(a *AccountStore) string {
	name := fmt.Sprintf("user%d", r.pick(6))
	u := &access.User{Username: name, Role: access.RoleUser, Enabled: true, CreatedAt: r.clock.Now(), WebAuthnHandle: access.GenerateWebAuthnHandle()}
	err := a.CreateUser(r.t.Context(), u)
	if taken := slices.Contains(slices.Collect(maps.Values(r.acct.users)), name); taken {
		if !errors.Is(err, account.ErrUsernameTaken) {
			r.t.Fatalf("CreateUser(%s), a taken name: %v, want ErrUsernameTaken", name, err)
		}
		return fmt.Sprintf("CreateUser %s: taken", name)
	}
	if err != nil {
		r.t.Fatalf("CreateUser(%s): %v", name, err)
	}
	r.assigned("user", u.ID)
	r.acct.users[u.ID] = name
	return fmt.Sprintf("CreateUser %s: %d", name, u.ID)
}

// deleteUser deletes a user and, in the model, everything its cascades
// reach: sessions, passkeys and keys with their stamps, the tokens acting
// on it and its key attributions.
func (r *rescan) deleteUser(a *AccountStore) string {
	id := pickOf(r, r.acct.users, cmp.Compare[int64])
	if err := a.DeleteUser(r.t.Context(), id); err != nil {
		r.t.Fatalf("DeleteUser(%d): %v", id, err)
	}
	delete(r.acct.users, id)
	maps.DeleteFunc(r.acct.sessions, func(k access.SessionRef, _ rescanSession) bool { return k.UserID == id })
	maps.DeleteFunc(r.acct.passkeys, func(k access.PasskeyRef, _ rescanPasskey) bool { return k.UserID == id })
	maps.DeleteFunc(r.acct.keys, func(_ int64, k rescanKey) bool { return k.owner == id })
	maps.DeleteFunc(r.acct.tokens, func(_ string, tok rescanToken) bool { return tok.subject == id })
	maps.DeleteFunc(r.acct.attrs, func(_, owner int64) bool { return owner == id })
	return fmt.Sprintf("DeleteUser %d", id)
}

func (r *rescan) createSession(a *AccountStore) string {
	user := pickOf(r, r.acct.users, cmp.Compare[int64])
	now := r.clock.Now()
	sess := &access.Session{
		CreatedAt: now, LastActivity: now, AuthTime: now, ID: fmt.Sprintf("s%d", r.n), TokenHash: access.NewHash(fmt.Sprintf("session-%d", r.n)),
		AuthMethod: access.MethodPassword, UserID: user,
	}
	if r.chance(0.3) {
		sess.IdleCap = 30 * time.Minute
	}
	if err := a.CreateSession(r.t.Context(), sess); err != nil {
		r.t.Fatalf("CreateSession(%d, %s): %v", user, sess.ID, err)
	}
	r.acct.sessions[access.SessionRef{UserID: user, ID: sess.ID}] = rescanSession{created: now, last: now, hash: sess.TokenHash.Reveal(), idleCap: sess.IdleCap}
	return fmt.Sprintf("CreateSession %d %s", user, sess.ID)
}

// rotateSession rotates a session to a new hash, through ExpectHash; a
// stale ExpectHash is refused and changes nothing.
func (r *rescan) rotateSession(a *AccountStore) string {
	ref := pickOf(r, r.acct.sessions, cmpSessionRef)
	m := r.acct.sessions[ref]
	stale := r.chance(0.2)
	expect := access.NewHash(m.hash)
	if stale {
		expect = access.NewHash("stale")
	}
	next := access.Session{
		CreatedAt: m.created, LastActivity: r.at(), AuthTime: m.created, ID: ref.ID, TokenHash: access.NewHash(fmt.Sprintf("session-%d", r.n)),
		AuthMethod: access.MethodPassword, UserID: ref.UserID, IdleCap: m.idleCap,
	}
	err := a.RotateSession(r.t.Context(), account.Rotation{ID: ref.ID, UserID: ref.UserID, ExpectHash: expect, Next: next})
	if stale {
		if !errors.Is(err, account.ErrNotFound) {
			r.t.Fatalf("RotateSession(%v) with a stale hash: %v, want ErrNotFound", ref, err)
		}
		return fmt.Sprintf("RotateSession %v: stale", ref)
	}
	if err != nil {
		r.t.Fatalf("RotateSession(%v): %v", ref, err)
	}
	m.hash, m.last = next.TokenHash.Reveal(), later(m.last, next.LastActivity)
	r.acct.sessions[ref] = m
	return fmt.Sprintf("RotateSession %v", ref)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (r *rescan) touchSession(a *AccountStore) string {
	ref := pickOf(r, r.acct.sessions, cmpSessionRef)
	m := r.acct.sessions[ref]
	at := r.at()
	if err := a.UpdateSessionActivity(r.t.Context(), access.NewHash(m.hash), access.SessionActivity{At: at, IP: "192.0.2.1"}); err != nil {
		r.t.Fatalf("UpdateSessionActivity(%v): %v", ref, err)
	}
	m.last = later(m.last, at)
	r.acct.sessions[ref] = m
	return fmt.Sprintf("UpdateSessionActivity %v", ref)
}

// expireSessions sweeps at most limit of the sessions access.ValidateSession
// refuses at now, and requires the refs returned to be that many of them.
func (r *rescan) expireSessions(a *AccountStore) string {
	now := r.clock.Now().Add([]time.Duration{0, 45 * time.Minute, 2 * time.Hour}[r.pick(3)])
	limit := 1 + r.pick(3)
	expired := map[access.SessionRef]bool{}
	for ref, m := range r.acct.sessions {
		sess := access.Session{CreatedAt: m.created, LastActivity: m.last, AuthTime: m.created, IdleCap: m.idleCap}
		if access.ValidateSession(&sess, cleanupTimeouts, now) != nil {
			expired[ref] = true
		}
	}
	refs, err := a.CleanupExpiredSessions(r.t.Context(), now, cleanupTimeouts, limit)
	if err != nil {
		r.t.Fatalf("CleanupExpiredSessions: %v", err)
	}
	if want := min(limit, len(expired)); len(refs) != want {
		r.t.Errorf("CleanupExpiredSessions(limit %d) deleted %v, want %d of the %d expired", limit, refs, want, len(expired))
	}
	for _, ref := range refs {
		if !expired[ref] {
			r.t.Errorf("CleanupExpiredSessions deleted %v, which is live or gone", ref)
		}
		delete(expired, ref)
		delete(r.acct.sessions, ref)
	}
	return fmt.Sprintf("CleanupExpiredSessions limit %d: %d", limit, len(refs))
}

func (r *rescan) deleteSession(a *AccountStore) string {
	ref := pickOf(r, r.acct.sessions, cmpSessionRef)
	if err := a.DeleteSession(r.t.Context(), ref); err != nil {
		r.t.Fatalf("DeleteSession(%v): %v", ref, err)
	}
	delete(r.acct.sessions, ref)
	return fmt.Sprintf("DeleteSession %v", ref)
}

func (r *rescan) createPasskey(a *AccountStore) string {
	user := pickOf(r, r.acct.users, cmp.Compare[int64])
	cred := &access.PasskeyCredential{CreatedAt: r.clock.Now(), UserID: user, CredentialID: fmt.Appendf(nil, "cred-%d", r.n), RawFlags: 1}
	if err := a.CreatePasskey(r.t.Context(), cred); err != nil {
		r.t.Fatalf("CreatePasskey(%d): %v", user, err)
	}
	r.acct.passkeys[access.PasskeyRef{UserID: user, ID: cred.ID}] = rescanPasskey{cred: string(cred.CredentialID)}
	return fmt.Sprintf("CreatePasskey %d: %d", user, cred.ID)
}

func (r *rescan) passkeySignIn(a *AccountStore) string {
	ref := pickOf(r, r.acct.passkeys, cmpPasskeyRef)
	m := r.acct.passkeys[ref]
	m.signCount++
	if err := a.UpdatePasskeyAfterLogin(r.t.Context(), ref.UserID, []byte(m.cred), access.PasskeyLoginUpdate{At: r.at(), SignCount: m.signCount, RawFlags: 5}); err != nil {
		r.t.Fatalf("UpdatePasskeyAfterLogin(%v): %v", ref, err)
	}
	r.acct.passkeys[ref] = m
	return fmt.Sprintf("UpdatePasskeyAfterLogin %v", ref)
}

func (r *rescan) deletePasskey(a *AccountStore) string {
	ref := pickOf(r, r.acct.passkeys, cmpPasskeyRef)
	if err := a.DeletePasskey(r.t.Context(), ref); err != nil {
		r.t.Fatalf("DeletePasskey(%v): %v", ref, err)
	}
	delete(r.acct.passkeys, ref)
	return fmt.Sprintf("DeletePasskey %v", ref)
}

func (r *rescan) createKey(a *AccountStore) string {
	user := pickOf(r, r.acct.users, cmp.Compare[int64])
	key := &access.Key{CreatedAt: r.clock.Now(), KeyHash: access.NewHash(fmt.Sprintf("key-%d", r.n)), UserID: user}
	if err := a.CreateAPIKey(r.t.Context(), key); err != nil {
		r.t.Fatalf("CreateAPIKey(%d): %v", user, err)
	}
	r.assigned("key", key.ID)
	r.acct.keys[key.ID] = rescanKey{owner: user}
	return fmt.Sprintf("CreateAPIKey %d: %d", user, key.ID)
}

func (r *rescan) useKey(a *AccountStore) string {
	id := pickOf(r, r.acct.keys, cmp.Compare[int64])
	at := r.at()
	if err := a.TouchAPIKey(r.t.Context(), id, at); err != nil {
		r.t.Fatalf("TouchAPIKey(%d): %v", id, err)
	}
	k := r.acct.keys[id]
	k.used = later(k.used, at)
	r.acct.keys[id] = k
	return fmt.Sprintf("TouchAPIKey %d", id)
}

func (r *rescan) deleteKey(a *AccountStore) string {
	id := pickOf(r, r.acct.keys, cmp.Compare[int64])
	if err := a.DeleteAPIKey(r.t.Context(), access.KeyRef{ID: id, UserID: r.acct.keys[id].owner}); err != nil {
		r.t.Fatalf("DeleteAPIKey(%d): %v", id, err)
	}
	delete(r.acct.keys, id)
	return fmt.Sprintf("DeleteAPIKey %d", id)
}

// createToken creates an invite, or a recovery token for a user, expiring
// up to an hour either side of the store's clock.
func (r *rescan) createToken(a *AccountStore) string {
	tok := access.OneTimeToken{
		CreatedAt: r.clock.Now(), ExpiresAt: r.clock.Now().Add(time.Duration(r.pick(121)-60) * time.Minute),
		Hash: access.NewHash(fmt.Sprintf("token-%d", r.n)), Purpose: access.PurposeInvite, Role: access.RoleUser,
	}
	if len(r.acct.users) > 0 && r.chance(0.6) {
		tok.Purpose, tok.Role, tok.Subject = access.PurposeRecovery, "", pickOf(r, r.acct.users, cmp.Compare[int64])
	}
	if err := a.CreateOneTimeToken(r.t.Context(), tok); err != nil {
		r.t.Fatalf("CreateOneTimeToken(%s, %d): %v", tok.Purpose, tok.Subject, err)
	}
	r.acct.tokens[tok.Hash.Reveal()] = rescanToken{expires: tok.ExpiresAt, subject: tok.Subject}
	return fmt.Sprintf("CreateOneTimeToken %s %d", tok.Purpose, tok.Subject)
}

// consumeToken consumes a token, or one no token has: found exactly when
// it expires after the store's clock, and gone either way.
func (r *rescan) consumeToken(a *AccountStore) string {
	hash := "token-none"
	if len(r.acct.tokens) > 0 && r.chance(0.85) {
		hash = pickOf(r, r.acct.tokens, cmp.Compare[string])
	}
	now := r.clock.Now()
	_, found, err := a.ConsumeOneTimeToken(r.t.Context(), access.NewHash(hash), now)
	if err != nil {
		r.t.Fatalf("ConsumeOneTimeToken(%s): %v", hash, err)
	}
	m, held := r.acct.tokens[hash]
	if want := held && m.expires.After(now); found != want {
		r.t.Errorf("ConsumeOneTimeToken(%s) found %v, want %v", hash, found, want)
	}
	delete(r.acct.tokens, hash)
	return fmt.Sprintf("ConsumeOneTimeToken %s: %v", hash, found)
}

func (r *rescan) createAttribution(a *AccountStore) string {
	user := pickOf(r, r.acct.users, cmp.Compare[int64])
	rec := &access.KeyAttribution{At: r.clock.Now(), Action: access.AttributionCreated, KeyLineage: []int64{1 + int64(r.pick(4))}, UserID: user}
	if err := a.CreateKeyAttribution(r.t.Context(), rec); err != nil {
		r.t.Fatalf("CreateKeyAttribution(%d): %v", user, err)
	}
	r.assigned("attribution", rec.ID)
	r.acct.attrs[rec.ID] = user
	return fmt.Sprintf("CreateKeyAttribution %d: %d", user, rec.ID)
}

// authState is every auth row the account invariants read, from one View.
type authState struct {
	users      map[int64]userRow
	sessions   map[access.SessionRef]sessionRow
	activity   map[access.SessionRef]activityRow
	passkeys   map[access.PasskeyRef]passkeyRow
	passkeyUse map[access.PasskeyRef]passkeyUseRow
	keys       map[int64]keyRow
	keyUse     map[int64]keyUseRow
	tokens     map[string]tokenRow
	attrs      map[int64]attributionRow
	userCount  int64
}

func (r *rescan) loadAuth() authState {
	st := authState{
		users: map[int64]userRow{}, sessions: map[access.SessionRef]sessionRow{}, activity: map[access.SessionRef]activityRow{},
		passkeys: map[access.PasskeyRef]passkeyRow{}, passkeyUse: map[access.PasskeyRef]passkeyUseRow{}, keys: map[int64]keyRow{},
		keyUse: map[int64]keyUseRow{}, tokens: map[string]tokenRow{}, attrs: map[int64]attributionRow{},
	}
	a := r.s.a
	if err := r.s.db.View(r.t.Context(), func(rtx *bolttable.ReadTx) error {
		n, err := a.userCount.Value(rtx)
		st.userCount = n
		return errors.Join(err,
			scanInto(rtx, a.users, st.users), scanInto(rtx, a.sessions, st.sessions), scanInto(rtx, a.activity, st.activity),
			scanInto(rtx, a.passkeys, st.passkeys), scanInto(rtx, a.passkeyUse, st.passkeyUse), scanInto(rtx, a.keys, st.keys),
			scanInto(rtx, a.keyUse, st.keyUse), scanInto(rtx, a.tokens, st.tokens), scanInto(rtx, a.attributions, st.attrs),
		)
	}); err != nil {
		r.t.Fatal(err)
	}
	return st
}

// checkAccounts requires the auth rows to be the model's, every companion
// stamp row beside its parent and every parent beside its stamp row, every
// row naming a user to name one that exists, and the users counter to
// equal a count of the users rows.
func (r *rescan) checkAccounts(step string) {
	st, m, t := r.loadAuth(), &r.acct, r.t
	users := map[int64]string{}
	for id, u := range st.users {
		users[id] = u.Username
	}
	if !maps.Equal(users, m.users) {
		t.Errorf("%s: users %v, the model %v", step, users, m.users)
	}
	if st.userCount != int64(len(st.users)) {
		t.Errorf("%s: the users counter is %d over %d users rows", step, st.userCount, len(st.users))
	}
	sessions := map[access.SessionRef]rescanSession{}
	for ref, row := range st.sessions {
		sessions[ref] = rescanSession{created: row.CreatedAt, last: st.activity[ref].LastActivity, hash: row.TokenHash, idleCap: time.Duration(row.IdleCap)}
	}
	if !maps.EqualFunc(sessions, m.sessions, sameSession) {
		t.Errorf("%s: sessions %+v, the model %+v", step, sessions, m.sessions)
	}
	passkeys := map[access.PasskeyRef]rescanPasskey{}
	for ref, row := range st.passkeys {
		passkeys[ref] = rescanPasskey{cred: string(row.CredentialID), signCount: st.passkeyUse[ref].SignCount}
	}
	if !maps.Equal(passkeys, m.passkeys) {
		t.Errorf("%s: passkeys %+v, the model %+v", step, passkeys, m.passkeys)
	}
	keys := map[int64]rescanKey{}
	for id, row := range st.keys {
		keys[id] = rescanKey{used: st.keyUse[id].LastUsedAt, owner: row.UserID}
	}
	if !maps.EqualFunc(keys, m.keys, func(a, b rescanKey) bool { return a.owner == b.owner && a.used.Equal(b.used) }) {
		t.Errorf("%s: keys %+v, the model %+v", step, keys, m.keys)
	}
	tokens := map[string]rescanToken{}
	for hash, row := range st.tokens {
		tokens[hash] = rescanToken{expires: row.ExpiresAt, subject: row.Subject}
	}
	if !maps.EqualFunc(tokens, m.tokens, func(a, b rescanToken) bool { return a.subject == b.subject && a.expires.Equal(b.expires) }) {
		t.Errorf("%s: tokens %+v, the model %+v", step, tokens, m.tokens)
	}
	attrs := map[int64]int64{}
	for id, row := range st.attrs {
		attrs[id] = row.UserID
	}
	if !maps.Equal(attrs, m.attrs) {
		t.Errorf("%s: key attributions %v, the model %v", step, attrs, m.attrs)
	}
	st.checkCompanions(t, step)
}

func sameSession(a, b rescanSession) bool {
	return a.hash == b.hash && a.idleCap == b.idleCap && a.created.Equal(b.created) && a.last.Equal(b.last)
}

// companions requires parent and stamp rows to come and go together.
func companions[K comparable, P, S any](t *testing.T, step, what string, parents map[K]P, stamps map[K]S) {
	t.Helper()
	for k := range stamps {
		if _, ok := parents[k]; !ok {
			t.Errorf("%s: a %s stamp row for %v has no parent row", step, what, k)
		}
	}
	for k := range parents {
		if _, ok := stamps[k]; !ok {
			t.Errorf("%s: %s %v has no stamp row", step, what, k)
		}
	}
}

func (st *authState) checkCompanions(t *testing.T, step string) {
	t.Helper()
	companions(t, step, "session", st.sessions, st.activity)
	companions(t, step, "passkey", st.passkeys, st.passkeyUse)
	companions(t, step, "key", st.keys, st.keyUse)
	owned := func(what string, k any, user int64) {
		if _, ok := st.users[user]; !ok {
			t.Errorf("%s: %s %v names user %d, which is gone", step, what, k, user)
		}
	}
	for ref := range st.sessions {
		owned("session", ref, ref.UserID)
	}
	for ref := range st.passkeys {
		owned("passkey", ref, ref.UserID)
	}
	for id, row := range st.keys {
		owned("key", id, row.UserID)
	}
	for id, row := range st.attrs {
		owned("key attribution", id, row.UserID)
	}
	for hash, row := range st.tokens {
		if row.Subject != 0 {
			owned("token", hash, row.Subject)
		}
	}
}

// authRows renders every auth row as its stored JSON by table, for the
// account feed check.
func (r *rescan) authRows() map[string]map[string]string {
	out := map[string]map[string]string{}
	add := func(table string, k, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			r.t.Fatal(err)
		}
		if out[table] == nil {
			out[table] = map[string]string{}
		}
		out[table][fmt.Sprint(k)] = string(b)
	}
	st := r.loadAuth()
	for k, v := range st.users {
		add("users", k, v)
	}
	for k, v := range st.sessions {
		add("sessions", k, v)
	}
	for k, v := range st.activity {
		add("session_activity", k, v)
	}
	for k, v := range st.passkeys {
		add("passkeys", k, v)
	}
	for k, v := range st.passkeyUse {
		add("passkey_use", k, v)
	}
	for k, v := range st.keys {
		add("keys", k, v)
	}
	for k, v := range st.keyUse {
		add("key_use", k, v)
	}
	for k, v := range st.tokens {
		add("tokens", k, v)
	}
	for k, v := range st.attrs {
		add("key_attributions", k, v)
	}
	return out
}

// stampTables are the auth tables a step may change without moving the head
// (AC1).
var stampTables = []string{"session_activity", "passkey_use", "key_use"}

// checkAuthFeed requires an account step that changed no auth row, and any
// step that changed only stamp rows, to leave the head revision where it
// was, and a step that changed any other auth row to mark accounts.
func (r *rescan) checkAuthFeed(step string, accountStep bool, head uint64, before map[string]map[string]string) {
	after := r.authRows()
	var changed []string
	for table := range unionKeys(before, after) {
		if !maps.Equal(before[table], after[table]) {
			changed = append(changed, table)
		}
	}
	slices.Sort(changed)
	switch {
	case slices.ContainsFunc(changed, func(table string) bool { return !slices.Contains(stampTables, table) }):
		if d := since(r.t, r.s, head); !d.Full && !slices.Contains(d.Marks, MarkAccounts) {
			r.t.Errorf("%s: %v changed and Since lists Marks %v", step, changed, d.Marks)
		}
	case accountStep || len(changed) > 0:
		if rev := headRev(r.t, r.s); rev != head {
			r.t.Errorf("%s: auth tables changed %v and the head moved from %d to %d", step, changed, head, rev)
		}
	}
}
