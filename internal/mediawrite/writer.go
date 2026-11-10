// Package mediawrite is the one writer of subtitle files beside videos. Each
// media root is its own os.Root keyed by its path; every change is staged
// behind a storage intent and published without replacing a file subflux
// cannot account for, so a crash or an outside writer never costs a file or
// its record.
package mediawrite

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/go/keylock"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

const (
	// probeInterval is the read-only probe's period: long enough for an idle
	// NAS to spin down between probes.
	probeInterval = 5 * time.Minute
	// rootProbeTimeout: an overdue probe marks its root unavailable, since a
	// hung hard mount blocks the syscall and Go cannot cancel it.
	rootProbeTimeout = 10 * time.Second
	// staleTempAge is the write probe's sweep age, well past the probe's
	// deadline, so it never removes a live probe's temp.
	staleTempAge = 10 * time.Minute
)

// Roots lists the configured media roots, absolute paths as subflux sees
// them.
type Roots interface {
	MediaRoots(ctx context.Context) []string
}

// Alerts is declared here, its caller; status.Store implements it.
type Alerts interface {
	Reconcile(source status.AlertSource, active bool, text string)
	RetainAlerts(kind string, live []status.AlertSource)
}

var _ Alerts = (*status.Store)(nil)

// Deps is what New needs; every field but Logger is required. Policy reads
// the language policy in force, which judges which supersedes are safe.
type Deps struct {
	Store  *statedb.Store
	Roots  Roots
	Alerts Alerts
	Policy func() language.Policy
	Logger *slog.Logger
}

// Writer serializes every write to the files beside each video and keeps
// each root's availability. Construct it with New and run Run; every call
// made before Run's first probe of the call's root waits for it.
type Writer struct {
	fs         fsOps
	roots      Roots
	alerts     Alerts
	leases     *keylock.Set[leaseKey]
	policy     func() language.Policy
	log        *slog.Logger
	store      *statedb.Store
	hashes     *hashCache
	unwritable *unwritableDirs
	cfg        atomic.Pointer[Config]
	started    chan struct{}               // closed once Run has read the root set
	kick       chan struct{}               // a pending request for a probe pass
	recoverReq chan struct{}               // a pending request to start recovery attempts
	paths      map[string]*rootState       // by configured path; guarded by mu
	outlived   map[statedb.Intent]struct{} // intents a pass left pending, until a count finds them settled; guarded by mu
	modes      publishModes
	recoveries sync.WaitGroup
	countMu    sync.Mutex // serializes countPending
	mu         sync.Mutex
	orphaning  bool // an orphan pass is running; guarded by mu
}

// New returns a Writer over d, whose post_processing section is the zero
// Config until SetConfig. It panics on a missing required dependency.
func New(d Deps) *Writer {
	if d.Store == nil || d.Roots == nil || d.Alerts == nil || d.Policy == nil {
		panic("mediawrite: New needs Store, Roots, Alerts and Policy")
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	w := &Writer{
		store:      d.Store,
		roots:      d.Roots,
		alerts:     d.Alerts,
		log:        log,
		policy:     d.Policy,
		fs:         productionFS(log, d.Store),
		leases:     newLeases(),
		hashes:     newHashCache(),
		unwritable: newUnwritableDirs(),
		started:    make(chan struct{}),
		kick:       make(chan struct{}, 1),
		recoverReq: make(chan struct{}, 1),
		paths:      map[string]*rootState{},
		outlived:   map[statedb.Intent]struct{}{},
	}
	w.cfg.Store(&Config{})
	return w
}

// Run reads the root set and probes every root at once, then every 5
// minutes and after each RootsChanged, until ctx ends; it returns nil
// once the recovery attempts it started have returned. Each probe runs in
// its own goroutine, one outstanding per root: a probe stuck in a hung
// mount's syscall outlives Run and is discarded when it returns. After
// each probe the root's pending writes are recovered, and a call under a
// root that returned waits for or fails during that recovery.
func (w *Writer) Run(ctx context.Context) error {
	defer w.recoveries.Wait()
	w.probePass(ctx)
	close(w.started)
	tick := time.NewTicker(probeInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.recoverReq:
			w.startRecoveries(ctx)
		case <-tick.C:
			w.probePass(ctx)
		case <-w.kick:
			w.probePass(ctx)
		}
	}
}

// RootsChanged asks Run for a probe pass now, which re-reads Roots; the
// caller changing what MediaRoots answers calls it after each change.
// Requests made while one is pending coalesce.
func (w *Writer) RootsChanged() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// probePass applies the configured root set, settles the intents no
// configured root holds any more, and starts a probe of every root that
// has none outstanding.
func (w *Writer) probePass(ctx context.Context) {
	paths := w.roots.MediaRoots(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applyRootsLocked(paths)
	w.startOrphansLocked(ctx)
	for _, rs := range w.paths {
		if rs.probe == nil {
			w.startProbeLocked(ctx, rs, rs.cur == nil)
		}
	}
}
