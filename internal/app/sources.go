package app

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediarefresh"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/radarr"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/seriesorder"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/skyhook"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// sourceKinds are the media source adapters the binary registers.
var sourceKinds = []mediasource.AdapterKind{sonarr.Kind, radarr.Kind}

// registries are what settings parses each section with: the adapter kinds,
// the process's provider table, whose entries every engine shares, and the
// directory of the store at storePath.
func registries(t *providers.Table, storePath string) settings.Registries {
	return settings.Registries{StoreDir: filepath.Dir(storePath), Sources: sourceKinds, Providers: t.Entries}
}

// sourceHTTPTimeout bounds a request on the client every source and SkyHook
// share; it sits above the longest per-request budget, Root's 60 s, so that
// budget's own error is the one a slow arr reports.
const sourceHTTPTimeout = 90 * time.Second

// sourceDeps are the one client and logger every source and SkyHook share.
func sourceDeps(log *slog.Logger) mediasource.Deps {
	return mediasource.Deps{HTTP: &http.Client{Timeout: sourceHTTPTimeout}, Logger: log}
}

// saveCheck is Save's check: media sources refuse added or reconnected
// entries that do not answer (mediasource.CheckSaved owns which), then
// methods, the method guard's advisory pre-check, refuses a sign-in change
// that would strand a user.
func saveCheck(deps mediasource.Deps, methods func(context.Context, accounts.Config) []formschema.FieldError) settings.SaveCheck {
	return func(ctx context.Context, prev, next settings.Active) []formschema.FieldError {
		errs := mediasource.CheckSaved(ctx, sourceKinds, deps, prev.Sources, next.Sources)
		return append(errs, methods(ctx, next.Auth)...)
	}
}

// sourceIDs are a's source ids, in its order.
func sourceIDs(a *settings.Active) []media.SourceID {
	ids := make([]media.SourceID, len(a.Sources))
	for i := range a.Sources {
		ids[i] = a.Sources[i].ID
	}
	return ids
}

// retainSources keeps the source and overlap alerts of ids and clears the
// rest, so a removed source's alert never outlives it.
func retainSources(st *status.Store, ids []media.SourceID) {
	srcs := make([]status.AlertSource, len(ids))
	overlaps := make([]status.AlertSource, len(ids))
	for i, id := range ids {
		srcs[i], overlaps[i] = status.SourceAlert(string(id)), status.SourceOverlapAlert(string(id))
	}
	st.RetainAlerts(status.SourceAlerts, srcs)
	st.RetainAlerts(status.SourceOverlapAlerts, overlaps)
}

// liveSources is the source Set in force, from boot: the writer's media
// roots and the refresher's sources. A build or rebuild publishes a Set
// whole, so a reader holding the old one keeps a consistent view until its
// next load. Before the first Active it holds none and lists no root.
type liveSources struct {
	refresh *mediarefresh.Refresher
	writer  *mediawrite.Writer
	set     atomic.Pointer[mediasource.Set]
}

// newLiveSources holds no Set yet and builds the writer over d with itself
// as Roots.
func newLiveSources(refresh *mediarefresh.Refresher, d mediawrite.Deps) *liveSources {
	l := &liveSources{refresh: refresh}
	d.Roots = l
	l.writer = mediawrite.New(d)
	return l
}

// publish makes set live, then hands it to the refresher and has the writer
// probe its roots.
func (l *liveSources) publish(set *mediasource.Set) {
	l.set.Store(set)
	l.refresh.ReloadSources(set)
	l.writer.RootsChanged()
}

func (l *liveSources) load() *mediasource.Set { return l.set.Load() }

// MediaRoots is the live Set's media roots; none before the first Set.
func (l *liveSources) MediaRoots(ctx context.Context) []string {
	set := l.load()
	if set == nil {
		return nil
	}
	return set.MediaRoots(ctx)
}

// mediaSources builds and rebuilds the Set it publishes to live, and runs
// the Scanner over it.
type mediaSources struct {
	orders mediasource.OrderJoin
	scan   *scan.Scanner
	log    *slog.Logger
	live   *liveSources
	deps   mediasource.Deps
	kinds  []mediasource.AdapterKind
}

// newMediaSources builds the Set over a's sources, publishes it to live and
// builds the Scanner over it with a's schedule; d's Schedule is ignored.
// The Scanner is not running.
func newMediaSources(a *settings.Active, kinds []mediasource.AdapterKind, deps mediasource.Deps, orders mediasource.OrderJoin, d scan.Deps, live *liveSources) (*mediaSources, error) {
	set, err := mediasource.NewSet(kinds, a.Sources, deps, orders)
	if err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	d.Schedule = a.Scan
	m := &mediaSources{orders: orders, scan: scan.New(d, set), log: log, live: live, deps: deps, kinds: kinds}
	live.publish(set)
	return m, nil
}

// reloadSources publishes a Set rebuilt from cur's sources when they differ
// from old's, inheriting each entry's caches CompareEntry allows before
// publishing, so an unchanged source keeps its roots while its arr is down,
// then hands it to the Scanner. A NewSet refusal, which a parsed
// configuration cannot reach, keeps the old Set and logs ERROR.
func (m *mediaSources) reloadSources(old, cur *settings.Active) {
	if reflect.DeepEqual(old.Sources, cur.Sources) {
		return
	}
	set, err := mediasource.NewSet(m.kinds, cur.Sources, m.deps, m.orders)
	if err != nil {
		m.log.Error("media sources: the rebuilt set was refused; the previous one stays live", "err", err)
		return
	}
	set.Inherit(m.live.load())
	m.live.publish(set)
	changed, rekeyed := changedSources(m.kinds, old.Sources, cur.Sources)
	m.scan.Reload(set, changed, rekeyed)
}

// reloadSchedule hands a changed scan section to the Scanner, which
// re-arms every loop and restarts none.
func (m *mediaSources) reloadSchedule(old, cur *settings.Active) {
	if cur.Scan != old.Scan {
		m.scan.SetSchedule(cur.Scan)
	}
}

// changedSources sorts cur's entries by their edit from old's entry with
// the same ID, in cur's order: changed holds the new and EntryChanged ones,
// rekeyed the EntryRekeyed ones. A removed entry is in neither, since
// Reload reads removals from the Set.
func changedSources(kinds []mediasource.AdapterKind, old, cur []mediasource.Config) (changed, rekeyed []media.SourceID) {
	for i := range cur {
		c := &cur[i]
		j := slices.IndexFunc(old, func(o mediasource.Config) bool { return o.ID == c.ID })
		if j < 0 {
			changed = append(changed, c.ID)
			continue
		}
		switch mediasource.CompareEntry(kinds, &old[j], c) {
		case mediasource.EntryChanged:
			changed = append(changed, c.ID)
		case mediasource.EntryRekeyed:
			rekeyed = append(rekeyed, c.ID)
		case mediasource.EntrySame:
		}
	}
	return changed, rekeyed
}

// sourcesLoop owns the media sources from the first Active snapshot on: it
// builds them then, runs their Scanner, and applies each later Active to
// them. Settings may first become Active after boot, from setup or a failed
// file, so the build waits for it.
type sourcesLoop struct {
	snapshot func() settings.Snapshot
	build    func(*settings.Active) (*mediaSources, error)
	// wake is signalled after each settings commit; wakes coalesce, since
	// apply reads the latest snapshot.
	wake        chan struct{}
	beforeApply func(context.Context)
	cur         *mediaSources
	prev        settings.Active
}

// airedOrders is the Set's aired order join: SkyHook's orders over deps'
// client, with store serving the evidence and the policy in force.
func airedOrders(deps mediasource.Deps, store *statedb.Store) *seriesorder.Service {
	return seriesorder.New(skyhook.New(deps.HTTP, deps.Logger), store, deps.Logger)
}

// sourcesWiring is what the sources loop builds over and feeds. built, when
// set, is handed each Scanner the loop builds; beforeApply, when set, runs
// before each apply of a wake.
type sourcesWiring struct {
	orders      mediasource.OrderJoin
	snapshot    func() settings.Snapshot
	store       *statedb.Store
	status      *status.Store
	metrics     *obs.Metrics
	live        *liveSources
	built       func(*scan.Scanner)
	beforeApply func(context.Context)
	deps        mediasource.Deps
}

// newSourcesLoop wires the loop's builds: the Set over w's deps and orders
// published to w.live, the Scanner over w's store and status with its
// series registered in w's metrics.
func newSourcesLoop(w *sourcesWiring) *sourcesLoop {
	return &sourcesLoop{
		snapshot:    w.snapshot,
		wake:        make(chan struct{}, 1),
		beforeApply: w.beforeApply,
		build: func(a *settings.Active) (*mediaSources, error) {
			ms, err := newMediaSources(a, sourceKinds, w.deps, w.orders, scan.Deps{Store: w.store, Status: w.status, Logger: w.deps.Logger}, w.live)
			if err != nil {
				return nil, err
			}
			if err := w.metrics.Register(ms.scan.Collectors()...); err != nil {
				return nil, err
			}
			if w.built != nil {
				w.built(ms.scan)
			}
			return ms, nil
		},
	}
}

// signal is the change loop's applied: it never blocks.
func (l *sourcesLoop) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// apply builds the sources on the first Active snapshot and reloads them
// from each later one; a snapshot that is not Active changes nothing. Only
// a build fails, and only at a programming fault.
func (l *sourcesLoop) apply() error {
	a, ok := l.snapshot().Active()
	if !ok {
		return nil
	}
	if l.cur == nil {
		ms, err := l.build(&a)
		if err != nil {
			return err
		}
		l.cur = ms
	} else {
		l.cur.reloadSources(&l.prev, &a)
		l.cur.reloadSchedule(&l.prev, &a)
	}
	l.prev = a
	return nil
}

// run runs the Scanner once the sources are built and applies each wake
// until ctx ends, then waits for the Scanner's return.
func (l *sourcesLoop) run(ctx context.Context) error {
	scanned := make(chan error, 1)
	running := false
	for {
		if !running && l.cur != nil {
			running = true
			go func() { scanned <- l.cur.scan.Run(ctx) }()
		}
		select {
		case <-ctx.Done():
			if running {
				return <-scanned
			}
			return nil
		case err := <-scanned:
			return err
		case <-l.wake:
			if l.beforeApply != nil {
				l.beforeApply(ctx)
			}
			if err := l.apply(); err != nil {
				return err
			}
		}
	}
}
