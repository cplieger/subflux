package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/release"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// pair is one configured target and variant.
type pair struct {
	target  language.Tag
	variant language.Variant
}

// run is one work row's run as planned: the instance's facts read once, the
// pairs it wants and the providers it asks.
type run struct {
	now     time.Time
	inst    statedb.Instance
	root    mediasource.Root
	video   release.Parsed
	subject subject
	items   []runItem
	wants   []*want
	asks    []*ask
	// fetched holds the attempt key of every candidate this run fetched,
	// so no other pair of the run fetches it again.
	fetched map[[32]byte]bool
	held    heldEvidence
	query   provider.Query
	fps     float64
	cause   statedb.WorkCause
}

// runItem is one item of the instance.
type runItem struct {
	history itemHistory
	// noResults are its no-result rows, by provider and target.
	noResults map[noResultKey]statedb.NoResult
	// seasonStopped holds each provider and target for which seasonStop
	// other episodes of its season hold a live row.
	seasonStopped map[noResultKey]bool
	// pairs are its configured pairs, in Policy.TargetsFor order.
	pairs []pair
	item  mediasource.Item
	key   media.Key
}

type noResultKey struct {
	provider string
	tag      language.Tag
}

// want is one pair the run searches for: missing, or upgradable over cur.
type want struct {
	cur *standing
	// lastRefusal is why an earlier run of the pair delivered nothing.
	lastRefusal string
	pair
	// anyUntried: over an Unverified file any untried candidate qualifies.
	anyUntried bool
	// gainOrClose: over a no-reference Unverified file under a user's run,
	// a strict gain or a candidate fitting the release qualifies.
	gainOrClose bool
}

// qualifies reports whether a candidate of standing st and fit would
// replace what w holds, if anything.
func (w *want) qualifies(st standing, fit rank.Fit) bool {
	switch {
	case w.cur == nil || w.anyUntried:
		return true
	case w.gainOrClose && fit >= rank.SameRelease:
		return true
	}
	return compareStanding(st, *w.cur) < 0
}

// toPick runs w up to the pick: it plans the run, asks every planned
// provider once, books the no-result evidence and ranks each wanted pair's
// candidates, in the wants' order. A nil run plans nothing.
func (e *Engine) toPick(ctx context.Context, w statedb.Work) (*run, []pick, error) {
	r, err := e.plan(ctx, w)
	if err != nil || r == nil || len(r.asks) == 0 {
		return r, e.picks(r, nil), err
	}
	answers, err := r.sweep(ctx)
	if err != nil {
		return nil, nil, err
	}
	picks := e.picks(r, r.offers(answers))
	if err := e.bookEvidence(ctx, r, answers, picks); err != nil {
		return nil, nil, err
	}
	return r, picks, nil
}

// picks is every want's pick over offers.
func (e *Engine) picks(r *run, offers []*offer) []pick {
	if r == nil {
		return nil
	}
	out := make([]pick, 0, len(r.wants))
	for _, w := range r.wants {
		out = append(out, e.pickOf(w, offers))
	}
	return out
}

// errInstanceGone is plan's answer for an instance the store no longer
// holds, or one with no item left: the run acknowledges its row.
var errInstanceGone = errors.New("search: instance gone")

// plan reads the work row's instance and plans its run: errInstanceGone
// when the instance is gone or has no item, nil when it plans nothing for
// this cause. A store error or an unavailable root is returned, the run
// then deferring or waiting (8.1).
func (e *Engine) plan(ctx context.Context, w statedb.Work) (*run, error) {
	inst, found, err := e.deps.Store.Instance(ctx, w.Inst)
	if err != nil {
		return nil, fmt.Errorf("search: read instance %d: %w", w.Inst, err)
	}
	if !found || len(inst.Items) == 0 {
		return nil, errInstanceGone
	}
	r := &run{now: time.Now(), inst: inst, cause: w.Cause, fetched: map[[32]byte]bool{}}
	if rerr := r.readItems(ctx, e); rerr != nil {
		return nil, rerr
	}
	if len(r.items) == 0 {
		return nil, errInstanceGone
	}
	if heldBack(w.Cause, r.items, false) {
		return nil, nil
	}
	video, err := e.deps.Writer.OpenVideo(ctx, inst.ID)
	if err != nil {
		return nil, e.unopened(&inst, err)
	}
	defer func() { _ = video.Close() }()
	probe, err := e.probe(ctx, inst.ID, video)
	if err != nil {
		return nil, err
	}
	inv, err := e.inventory(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	if heldBack(w.Cause, r.items, inv.Unwritable) {
		return nil, nil
	}
	if err := r.planWants(ctx, e, inv, probe); err != nil {
		return nil, err
	}
	if err := r.planAsks(ctx, e); err != nil {
		return nil, err
	}
	r.planQuery(probe)
	if len(r.asks) > 0 {
		if err := r.readSeries(ctx, e); err != nil {
			return nil, err
		}
		r.query.OSHash = e.hash(inst.ID, video)
	}
	return r, nil
}

// readSeries reads, for an episode, what 8.11's steps check names against:
// the bound reading of the instance's root under its owning source, the
// root's order and its items as the source lists them. A reading's store
// error fails the run.
func (r *run) readSeries(ctx context.Context, e *Engine) error {
	if r.root.Native.Kind != media.Episode {
		return nil
	}
	ev, found, err := e.deps.Evidence.Evidence(ctx, r.inst.Source, r.root.Native)
	if err != nil {
		return fmt.Errorf("search: read the series evidence of instance %d: %w", r.inst.ID, err)
	}
	ser := episodematch.Series{Order: r.root.Order}
	if found {
		ser.Reading = &ev
	}
	root := r.items[0].key.Root()
	var cursor statedb.ListCursor
	at := map[media.Key]int{}
	for {
		rows, next, err := e.deps.Store.Items(ctx, root, cursor, rootItemsPage)
		if err != nil {
			return fmt.Errorf("search: read the items of instance %d's root: %w", r.inst.ID, err)
		}
		for i := range rows {
			at[rows[i].Key] = len(ser.Items)
			ser.Items = append(ser.Items, rows[i].Item)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	r.subject.index = make([]int, len(r.items))
	for i := range r.items {
		j, ok := at[r.items[i].key]
		if !ok {
			j = len(ser.Items)
			ser.Items = append(ser.Items, r.items[i].item)
		}
		r.subject.index[i] = j
	}
	r.subject.series = ser
	return nil
}

// rootItemsPage is how many of a root's items one Items read takes.
const rootItemsPage = 500

// heldBack reports whether a run of cause plans nothing for the instance:
// an automatic one while an item is excluded (CR-MS-5) or the video's
// directory refuses writes (CR-TS-14); a user's run ignores both.
func heldBack(cause statedb.WorkCause, items []runItem, unwritable bool) bool {
	return cause != statedb.Item && (unwritable || slices.ContainsFunc(items, func(it runItem) bool { return it.item.Excluded }))
}

// unopened is plan's answer to OpenVideo's error: an unavailable root is
// returned for the drainer to defer; an instance retired meanwhile plans
// nothing; a video outside every root or that cannot be opened plans
// nothing until the next scan re-enqueues it.
func (e *Engine) unopened(inst *statedb.Instance, err error) error {
	switch {
	case errors.Is(err, mediawrite.ErrRootUnavailable):
		return err
	case errors.Is(err, mediawrite.ErrInvalidRef):
		return nil
	}
	e.log.Warn("search: video cannot be opened", "instance", inst.ID, "video", inst.VideoPath, "err", err)
	return nil
}

// probe is video's probe, or after a failed probe the one stored.
func (e *Engine) probe(ctx context.Context, inst statedb.InstanceID, video *os.File) (statedb.VideoProbe, error) {
	p, err := e.deps.Prober.Probe(ctx, inst, video)
	if err == nil {
		return p, nil
	}
	e.log.Warn("search: probe failed, planning with what is stored", "instance", inst, "err", err)
	p, _, err = e.deps.Store.Embedded(ctx, inst)
	if err != nil {
		return statedb.VideoProbe{}, fmt.Errorf("search: read instance %d's probe: %w", inst, err)
	}
	return p, nil
}

// inventory lists the instance's subtitle files and stores the unmanaged
// ones, which recomputes its items' coverage.
func (e *Engine) inventory(ctx context.Context, inst statedb.InstanceID) (mediawrite.Inventory, error) {
	inv, err := e.deps.Writer.List(ctx, inst, mediawrite.ListOptions{})
	if err != nil {
		return mediawrite.Inventory{}, fmt.Errorf("search: list instance %d: %w", inst, err)
	}
	local := []statedb.LocalFile{}
	for i := range inv.Files {
		if !inv.Files[i].Managed {
			local = append(local, inv.Files[i].LocalFile)
		}
	}
	if err := e.deps.Store.PutExternal(ctx, inst, local); err != nil {
		return mediawrite.Inventory{}, fmt.Errorf("search: store instance %d's local files: %w", inst, err)
	}
	return inv, nil
}

// hash is the video's OpenSubtitles hash, zero when it cannot be read.
func (e *Engine) hash(inst statedb.InstanceID, video *os.File) uint64 {
	h, err := videoHash(video)
	if err != nil {
		e.log.Warn("search: video not hashed", "instance", inst, "err", err)
	}
	return h
}

// readItems reads each item's facts; an item whose record went since the
// instance was read is left out.
func (r *run) readItems(ctx context.Context, e *Engine) error {
	for _, k := range r.inst.Items {
		item, root, found, err := e.deps.Store.Item(ctx, k)
		if err != nil {
			return fmt.Errorf("search: read item %s: %w", k, err)
		}
		if found {
			r.root = root
			r.items = append(r.items, runItem{key: k, item: item})
		}
	}
	return nil
}

// planWants reads each item's coverage and history and lists the pairs the
// run wants: every configured pair no item has locked that some item misses
// or that is upgradable (8.2 step 3).
func (r *run) planWants(ctx context.Context, e *Engine, inv mediawrite.Inventory, probe statedb.VideoProbe) error {
	records, err := e.deps.Store.Files(ctx, r.inst.ID)
	if err != nil {
		return fmt.Errorf("search: read instance %d's files: %w", r.inst.ID, err)
	}
	horizon := max(attemptMemory, time.Duration(e.cfg.UpgradeWindowDays)*24*time.Hour)
	var cov coverage
	for i := range r.items {
		it := &r.items[i]
		rows, err := e.deps.Store.Coverage(ctx, it.key)
		if err != nil {
			return fmt.Errorf("search: read %s's coverage: %w", it.key, err)
		}
		it.pairs = cov.add(rows)
		if it.history, err = e.readHistory(ctx, it.key, r.now, horizon); err != nil {
			return err
		}
	}
	r.held = heldEvidence{policy: e.policy, records: records, inv: inv, tracks: probe.Tracks}
	// No text track to align against and no audio fallback: an Unverified
	// file timed by nothing stays unverified whatever a scan fetches.
	noReference := !e.timing.AudioFallback && !slices.ContainsFunc(probe.Tracks, func(t statedb.EmbeddedTrack) bool {
		return slices.Contains(embedded.TextCodecs, t.Codec)
	})
	for _, p := range cov.order {
		if cov.locked[p] {
			continue
		}
		if w, ok := r.wantOf(e, p, cov.worst[p], &r.held, noReference); ok {
			w.lastRefusal = cov.refusal[p]
			r.wants = append(r.wants, w)
		}
	}
	return nil
}

// coverage is the instance's configured pairs over its items: in the order
// first seen, each at its worst coverage, locked when any item locked it.
type coverage struct {
	worst  map[pair]statedb.Coverage
	locked map[pair]bool
	// refusal is the first refusal an item notes on the pair.
	refusal map[pair]string
	order   []pair
}

// add merges one item's rows and returns its pairs.
func (c *coverage) add(rows []statedb.CoverageRow) []pair {
	if c.worst == nil {
		c.worst, c.locked, c.refusal = map[pair]statedb.Coverage{}, map[pair]bool{}, map[pair]string{}
	}
	pairs := make([]pair, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		p := pair{target: row.Target, variant: row.Variant}
		pairs = append(pairs, p)
		if worst, seen := c.worst[p]; !seen || row.Coverage < worst {
			if !seen {
				c.order = append(c.order, p)
			}
			c.worst[p] = row.Coverage
		}
		c.locked[p] = c.locked[p] || row.Locked
		if c.refusal[p] == "" {
			c.refusal[p] = row.LastRefusal
		}
	}
	return pairs
}

// wantOf judges one unlocked pair whose worst coverage over the items is
// c; false when the run does not search for it.
func (r *run) wantOf(e *Engine, p pair, c statedb.Coverage, held *heldEvidence, noReference bool) (*want, bool) {
	if c == statedb.Missing {
		return &want{pair: p}, true
	}
	if c == statedb.Exact && held.settled(p) {
		return nil, false
	}
	cur := held.best(p)
	w := &want{pair: p, cur: &cur}
	upgradable := c == statedb.Substitute
	if rec := held.supersedable(p); rec != nil {
		prov := rec.Provenance
		unverified := prov.Timing.Verdict == timing.Unverified
		unreferenced := unverified && noReference && prov.Timing.Method == timing.MethodNone
		switch {
		case unreferenced && r.cause != statedb.Item:
			return nil, false
		case unreferenced:
			w.gainOrClose, upgradable = true, true
		case unverified:
			w.anyUntried, upgradable = true, true
		case prov.MachineTranslated || prov.Fit < rank.Exact && r.withinWindow(e, p):
			upgradable = true
		}
	}
	return w, upgradable
}

// withinWindow reports whether p was last delivered on any item within
// upgrade_window_days.
func (r *run) withinWindow(e *Engine, p pair) bool {
	window := time.Duration(e.cfg.UpgradeWindowDays) * 24 * time.Hour
	for i := range r.items {
		if at, ok := r.items[i].history.delivered[p]; ok && r.now.Sub(at) < window {
			return true
		}
	}
	return false
}

// heldEvidence is what the instance holds that may serve a pair: its file
// records, its unmanaged local files and its embedded tracks.
type heldEvidence struct {
	policy  language.Policy
	records []statedb.FileRecord
	tracks  []statedb.EmbeddedTrack
	inv     mediawrite.Inventory
}

// supersedableRecord reports whether rec is a file a run may replace:
// managed, delivered automatically and not locked.
func supersedableRecord(rec *statedb.FileRecord) bool {
	return rec.Provenance != nil && !rec.Provenance.Manual && !rec.Locked
}

// serves grades tag for p's target when flags serve p's variant, and
// reports whether it serves p automatically or as a substitute.
func (h *heldEvidence) serves(p pair, tag language.Tag, f language.Flags) (language.Grade, bool) {
	g := h.policy.Grade(p.target, tag)
	return g, f.Serves(p.variant) && (g.Class == language.Exact || g.Class == language.Substitute)
}

// best is the best standing among the evidence serving p, which an
// upgrade must beat; a file of unknown fit stands below every fit.
func (h *heldEvidence) best(p pair) standing {
	var top standing
	found := false
	consider := func(tag language.Tag, f language.Flags, machine bool, fit rank.Fit) {
		g, ok := h.serves(p, tag, f)
		if st := standingOf(g, machine, f, p.variant, fit); ok && (!found || compareStanding(st, top) < 0) {
			top, found = st, true
		}
	}
	for i := range h.records {
		r := &h.records[i]
		if r.Provenance == nil {
			consider(r.Tag, r.Flags, false, 0)
		} else {
			consider(r.Tag, r.Flags, r.Provenance.MachineTranslated, r.Provenance.Fit)
		}
	}
	for i := range h.inv.Files {
		if f := &h.inv.Files[i]; !f.Managed {
			consider(f.Tag, f.Flags, false, 0)
		}
	}
	for _, t := range h.tracks {
		consider(t.Tag, t.Flags, false, 0)
	}
	return top
}

// supersedable is the best supersedable record serving p, nil for none.
func (h *heldEvidence) supersedable(p pair) *statedb.FileRecord {
	var (
		rec *statedb.FileRecord
		top standing
	)
	for i := range h.records {
		r := &h.records[i]
		g, ok := h.serves(p, r.Tag, r.Flags)
		if !ok || !supersedableRecord(r) {
			continue
		}
		if st := standingOf(g, r.Provenance.MachineTranslated, r.Flags, p.variant, r.Provenance.Fit); rec == nil || compareStanding(st, top) < 0 {
			rec, top = r, st
		}
	}
	return rec
}

// settled reports whether a file no run replaces serves p as Exact: a
// user's file, an adopted one, or a manual or locked delivery. Such a file
// covers the pair for good (C9).
func (h *heldEvidence) settled(p pair) bool {
	exact := func(tag language.Tag, f language.Flags) bool {
		g, ok := h.serves(p, tag, f)
		return ok && g.Class == language.Exact
	}
	for i := range h.records {
		if r := &h.records[i]; !supersedableRecord(r) && exact(r.Tag, r.Flags) {
			return true
		}
	}
	for i := range h.inv.Files {
		if f := &h.inv.Files[i]; !f.Managed && exact(f.Tag, f.Flags) {
			return true
		}
	}
	return false
}

// planQuery fills the query every provider is asked with, less its tags
// and the hash: the instance's media, its first item's numbering, the
// root's year and titles and the video string, which it also parses.
func (r *run) planQuery(probe statedb.VideoProbe) {
	first := r.items[0].item
	kind := r.root.Native.Kind
	name := r.inst.SceneName
	if name == "" {
		name = filepath.Base(r.inst.VideoPath)
	}
	r.video, r.fps = release.Parse(name, kind), probe.FPS
	titles := append([]string{r.root.Title}, r.root.AltTitles...)
	ext := r.root.External
	if kind == media.Movie && first.External != (media.ExternalIDs{}) {
		ext = first.External
	}
	video := r.inst.SceneName
	if video == "" && r.inst.VideoPath != "" {
		base := filepath.Base(r.inst.VideoPath)
		video = strings.TrimSuffix(base, filepath.Ext(base))
	}
	r.query = provider.Query{
		Kind: kind, Year: r.root.Year, External: ext, Numbering: first.Numbering, Absolute: first.Absolute,
		Titles: titles, Video: video,
	}
	r.subject = subject{titles: titles, year: r.root.Year, kind: kind}
	for i := range r.items {
		r.subject.numbers = append(r.subject.numbers, r.items[i].item.Numbering)
	}
}
