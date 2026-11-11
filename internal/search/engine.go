package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/go/keylock"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
	"golang.org/x/sync/errgroup"
)

// Deps are the engine's collaborators; every field but Logger is required.
type Deps struct {
	Starts   *Starts
	Store    *statedb.Store
	Funnel   *providercalls.Funnel
	Writer   *mediawrite.Writer
	Prober   *embedded.Prober
	Aligner  Aligner
	Evidence Evidence
	Status   *status.Store
	// Logger, nil for slog.Default().
	Logger *slog.Logger
}

// maxSummary bounds an Entry's Summary, one line.
const maxSummary = 120

// Engine is one configuration's search engine.
type Engine struct {
	deps  Deps
	items errgroup.Group
	scans errgroup.Group
	// run, next, begin and deliverTo are one row's run, the drainer's store
	// read, the status store's Begin and the writer's Deliver; tests wrap
	// them.
	run       func(ctx context.Context, w statedb.Work) (runReport, error)
	begin     func(ctx context.Context, job status.Job, stop func()) (status.Job, bool, error)
	deliverTo func(ctx context.Context, d mediawrite.Delivery) (statedb.FileRecord, error)
	log       *slog.Logger
	gate      *keylock.Set[statedb.InstanceID]
	// done coalesces the wakes of runs that returned.
	done chan struct{}
	// slots bounds the Item group: Item rows and manual downloads.
	slots chan struct{}
	// The drainer's state, under mu: the instances running, those whose
	// run ended since the current poll's read, those waiting out a
	// failure, and each manual job's cancel.
	inFlight  map[statedb.InstanceID]bool
	leftSince map[statedb.InstanceID]bool
	retries   map[statedb.InstanceID]*retry
	jobs      map[status.JobID]context.CancelFunc
	// waiters are the SearchNow jobs no run has taken yet, by instance;
	// running the Item runs that took some.
	waiters map[statedb.InstanceID][]*waiter
	running map[statedb.InstanceID]*itemRun
	handles handleStore
	next    func(ctx context.Context, limit int) ([]statedb.Work, error)
	policy  language.Policy
	// providers are the enabled ones, by priority then id.
	providers []bound
	cfg       Config
	mu        sync.Mutex
	timing    timing.Config
	drained   bool
	draining  bool
}

// bound is one enabled provider: its entry and config, its built adapter
// and its binding to the funnel.
type bound struct {
	adapter provider.Adapter
	binding *providercalls.Binding
	entry   provider.Entry
	config  provider.Config
}

// errRegistry is every New refusal: a fault in the provider registry or in
// a configuration no parser returned, never a user's value.
var errRegistry = errors.New("search: registry fault")

// New checks every entry's Summary and repeats ParseConfig's and
// provider.ParseConfigs' checks, then binds each enabled provider to
// deps.Funnel, its Automatic calls spaced by cfg.ScanDelay, and registers
// each enabled entry's Data with a report reconciling
// status.ProviderDataAlert of that provider. A refusal binds and registers
// nothing.
func New(deps Deps, entries []provider.Entry, cfgs []provider.Config, p language.Policy, cfg Config, tc timing.Config) (*Engine, error) { //nolint:gocritic // hugeParam: New keeps its own copy of deps

	if err := checkDeps(&deps); err != nil {
		return nil, err
	}
	if err := cfg.check(p); err != nil {
		return nil, fmt.Errorf("%w: %w", errRegistry, err)
	}
	enabled, err := build(entries, cfgs)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		deps: deps, log: deps.Logger, policy: p, cfg: cfg, timing: tc, next: deps.Store.NextWork,
		gate: keylock.New(cmp.Compare[statedb.InstanceID]), done: make(chan struct{}, 1),
		inFlight: map[statedb.InstanceID]bool{}, leftSince: map[statedb.InstanceID]bool{}, retries: map[statedb.InstanceID]*retry{},
		jobs: map[status.JobID]context.CancelFunc{}, slots: make(chan struct{}, itemRunsInFlight),
		waiters: map[statedb.InstanceID][]*waiter{}, running: map[statedb.InstanceID]*itemRun{},
		handles: newHandleStore(), begin: deps.Status.Begin, deliverTo: deps.Writer.Deliver,
	}
	e.run = e.work
	e.scans.SetLimit(runsInFlight)
	if e.log == nil {
		e.log = slog.Default()
	}
	for i := range enabled {
		b := &enabled[i]
		b.binding = deps.Funnel.Bind(b.entry.ID, credential(&b.entry, &b.config), cfg.ScanDelay)
		registerData(deps.Status, &b.entry)
	}
	e.providers = enabled
	return e, nil
}

func checkDeps(d *Deps) error {
	if d.Starts == nil || d.Store == nil || d.Funnel == nil || d.Writer == nil || d.Prober == nil || d.Aligner == nil || d.Evidence == nil || d.Status == nil {
		return fmt.Errorf("%w: Deps lacks a required field", errRegistry)
	}
	return nil
}

// build checks each entry's Summary and each config's provider, then
// builds every enabled one, by priority then id.
func build(entries []provider.Entry, cfgs []provider.Config) ([]bound, error) {
	for i := range entries {
		if s := entries[i].Summary; s == "" || len(s) > maxSummary || strings.ContainsAny(s, "\r\n") {
			return nil, fmt.Errorf("%w: %s's Summary is not one line of 1 to %d bytes", errRegistry, entries[i].ID, maxSummary)
		}
	}
	var out []bound
	seen := map[provider.ProviderID]bool{}
	for _, c := range cfgs {
		j := slices.IndexFunc(entries, func(e provider.Entry) bool { return e.ID == c.ID })
		switch {
		case j < 0:
			return nil, fmt.Errorf("%w: no entry for provider %q", errRegistry, c.ID)
		case seen[c.ID]:
			return nil, fmt.Errorf("%w: provider %q configured twice", errRegistry, c.ID)
		case c.Priority < 0 || c.Priority > provider.MaxPriority:
			return nil, fmt.Errorf("%w: provider %q priority %d", errRegistry, c.ID, c.Priority)
		}
		seen[c.ID] = true
		if !c.Enabled {
			continue
		}
		a, err := provider.Build(entries[j], c)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errRegistry, err)
		}
		out = append(out, bound{entry: entries[j], config: c, adapter: a})
	}
	slices.SortFunc(out, func(a, b bound) int {
		if a.config.Priority != b.config.Priority {
			return a.config.Priority - b.config.Priority
		}
		return strings.Compare(string(a.entry.ID), string(b.entry.ID))
	})
	return out, nil
}

// credential is what the funnel keys a provider's state on: the keyenc join
// of the entry's Secret and Required String values in schema order, "" for
// an entry with none.
func credential(e *provider.Entry, c *provider.Config) string {
	var parts []string
	for _, f := range e.Schema.Fields {
		switch {
		case f.Kind == formschema.Secret:
			parts = append(parts, c.Values.Secret(f.Key))
		case f.Kind == formschema.String && f.Required:
			parts = append(parts, c.Values.String(f.Key))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return keyenc.Join(parts...)
}

// registerData registers e's Data, when set, with a report raising and
// clearing status.ProviderDataAlert of e's provider; a later registration,
// as at an engine swap, replaces it.
func registerData(st *status.Store, e *provider.Entry) {
	if e.Data == nil {
		return
	}
	src := status.ProviderDataAlert(string(e.ID))
	e.Data(func(text string, active bool) { st.Reconcile(src, active, text) })
}
