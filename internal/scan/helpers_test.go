package scan_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"go.yaml.in/yaml/v4"
)

// src is the rig's source; other is a second source the activation holds,
// whose sightings a test writes straight to the store.
const (
	src   media.SourceID = "src"
	other media.SourceID = "other"
)

// daily is the default scan section.
var daily = mediasource.Schedule{Change: 30 * time.Second, Whole: 24 * time.Hour}

type yieldFunc = func(mediasource.Root, []mediasource.Item) error

// call is one adapter call the rig saw: its op, when it started, and the
// cursor bytes it read from (Changes) or the root it read (Root).
type call struct {
	at     time.Time
	op     string
	cursor []byte
	root   mediasource.NativeRef
}

// adapter wraps the rig's Fake, recording every call; items, when set,
// replaces the walk.
type adapter struct {
	*sourcetest.Fake
	items func(context.Context, func(mediasource.Root, []mediasource.Item) error, mediasource.Unread) error
	// onRoot, when set, runs at each Root call before the read.
	onRoot func(mediasource.NativeRef)
	// folders, when set, replaces the probe's answer.
	folders func(context.Context) ([]string, error)
	calls   []call
	mu      sync.Mutex
	// active counts the walks and root reads in flight; most is its peak.
	active, most int
}

func (a *adapter) enter() func() {
	a.mu.Lock()
	a.active++
	a.most = max(a.most, a.active)
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		a.active--
		a.mu.Unlock()
	}
}

func (a *adapter) peak() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.most
}

func (a *adapter) record(c call) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c.at = time.Now()
	a.calls = append(a.calls, c)
}

func (a *adapter) Items(ctx context.Context, y func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
	a.record(call{op: "Items"})
	defer a.enter()()
	a.mu.Lock()
	f := a.items
	a.mu.Unlock()
	if f != nil {
		return f(ctx, y, u)
	}
	return a.Fake.Items(ctx, y, u)
}

func (a *adapter) Root(ctx context.Context, ref mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error) {
	a.record(call{op: "Root", root: ref})
	defer a.enter()()
	a.mu.Lock()
	hook := a.onRoot
	a.mu.Unlock()
	if hook != nil {
		hook(ref)
	}
	return a.Fake.Root(ctx, ref)
}

func (a *adapter) Changes(ctx context.Context, cur mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error) {
	b, _ := cur.MarshalBinary()
	a.record(call{op: "Changes", cursor: b})
	return a.Fake.Changes(ctx, cur)
}

func (a *adapter) RootFolders(ctx context.Context) ([]string, error) {
	a.record(call{op: "RootFolders"})
	a.mu.Lock()
	f := a.folders
	a.mu.Unlock()
	if f != nil {
		return f(ctx)
	}
	return a.Fake.RootFolders(ctx)
}

func (a *adapter) setFolders(f func(context.Context) ([]string, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.folders = f
}

func (a *adapter) setOnRoot(f func(mediasource.NativeRef)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRoot = f
}

func (a *adapter) setItems(f func(context.Context, func(mediasource.Root, []mediasource.Item) error, mediasource.Unread) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = f
}

// took returns and forgets the calls of op so far.
func (a *adapter) took(op string) []call {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out, rest []call
	for _, c := range a.calls {
		if c.op == op {
			out = append(out, c)
		} else {
			rest = append(rest, c)
		}
	}
	a.calls = rest
	return out
}

// store wraps the rig's statedb, recording FinishPass calls and injecting
// faults.
type store struct {
	*statedb.Store
	cursorErr  error
	observeErr error
	finishErr  error
	finishes   []finish
	mu         sync.Mutex
}

type finish struct {
	cursor   []byte
	keep     []mediasource.NativeRef
	complete bool
}

func (s *store) SourceCursor(ctx context.Context, id media.SourceID) (mediasource.Cursor, bool, error) {
	s.mu.Lock()
	err := s.cursorErr
	s.mu.Unlock()
	if err != nil {
		return mediasource.Cursor{}, false, err
	}
	return s.Store.SourceCursor(ctx, id)
}

func (s *store) ObserveItems(ctx context.Context, id media.SourceID, p statedb.PassID, r mediasource.Root, items []mediasource.Item) error {
	s.mu.Lock()
	err := s.observeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.ObserveItems(ctx, id, p, r, items)
}

func (s *store) FinishPass(ctx context.Context, id media.SourceID, p statedb.PassID, complete bool, next mediasource.Cursor, keep []mediasource.NativeRef) ([]media.SourceID, error) {
	b, _ := next.MarshalBinary()
	s.mu.Lock()
	s.finishes = append(s.finishes, finish{complete: complete, cursor: b, keep: slices.Clone(keep)})
	err := s.finishErr
	s.mu.Unlock()
	if err != nil && complete {
		return nil, err
	}
	return s.Store.FinishPass(ctx, id, p, complete, next, keep)
}

func (s *store) fault(cursor, observe error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursorErr, s.observeErr = cursor, observe
}

func (s *store) failFinish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishErr = err
}

func (s *store) tookFinishes() []finish {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.finishes
	s.finishes = nil
	return out
}

// rig is one source "src" served by a Fake through a real Set, a real
// statedb under t.TempDir and a real status store; build it inside a
// synctest bubble.
type rig struct {
	lib     *sourcetest.Library
	ad      *adapter
	db      *store
	jobs    *status.Store
	status  *statusHook
	scanner *scan.Scanner
	set     *mediasource.Set
	stop    func()
	policy  language.Policy
	sched   mediasource.Schedule
}

// newRig builds the rig over caps and sched; start runs it.
// rigConfig is what a rig option changes: the source's entry and the Set's
// aired join.
type rigConfig struct {
	orders mediasource.OrderJoin
	cfg    mediasource.Config
}

type option func(*rigConfig)

func excluding(labels ...string) option {
	return func(c *rigConfig) { c.cfg.ExcludeTags = labels }
}

func joinedBy(j mediasource.OrderJoin) option {
	return func(c *rigConfig) { c.orders = j }
}

func newRig(t *testing.T, caps mediasource.Capabilities, sched mediasource.Schedule, opts ...option) *rig {
	t.Helper()
	r := &rig{lib: &sourcetest.Library{}, sched: sched}
	r.ad = &adapter{Fake: sourcetest.NewFake(r.lib, caps)}
	kind := mediasource.AdapterKind{Type: "fake", Schema: sourcetest.FakeSchema, New: func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) { return r.ad, nil }}
	rc := rigConfig{cfg: mediasource.Config{ID: src, Type: "fake"}, orders: sourcetest.OwnPlaces{}}
	for _, o := range opts {
		o(&rc)
	}
	set, err := mediasource.NewSet([]mediasource.AdapterKind{kind}, []mediasource.Config{rc.cfg}, mediasource.Deps{Logger: quiet()}, rc.orders)
	if err != nil {
		t.Fatal(err)
	}
	db, err := statedb.Open(filepath.Join(t.TempDir(), "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("default:\n  - tag: en\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	r.policy = p
	if err := db.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{other, src}, Languages: p}); err != nil {
		t.Fatal(err)
	}
	r.db = &store{Store: db}
	if r.jobs, err = status.New(t.Context(), statustest.NewMemPersister()); err != nil {
		t.Fatal(err)
	}
	r.set = set
	r.status = &statusHook{Store: r.jobs}
	r.scanner = scan.New(scan.Deps{Store: r.db, Status: r.status, Logger: quiet(), Schedule: sched}, set)
	return r
}

// statusHook is the rig's status store, with begin, when set, run inside
// every Begin before the record.
type statusHook struct {
	*status.Store
	begin func()
	mu    sync.Mutex
}

func (h *statusHook) Begin(ctx context.Context, j status.Job, stop func()) (status.Job, bool, error) {
	h.mu.Lock()
	f := h.begin
	h.mu.Unlock()
	if f != nil {
		f()
	}
	return h.Store.Begin(ctx, j, stop)
}

func (h *statusHook) onBegin(f func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.begin = f
}

// setOf builds a Set holding each source of srcs, in id order, served by
// its adapter; the aired join answers every episode at its own place.
func setOf(t *testing.T, srcs map[media.SourceID]*adapter) *mediasource.Set {
	t.Helper()
	ids := slices.Sorted(maps.Keys(srcs))
	kinds := make([]mediasource.AdapterKind, 0, len(ids))
	cfgs := make([]mediasource.Config, 0, len(ids))
	for i, id := range ids {
		ad := srcs[id]
		typ := "fake-" + string(id)
		kinds = append(kinds, mediasource.AdapterKind{Type: typ, Schema: sourcetest.FakeSchema, New: func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) { return ad, nil }})
		cfgs = append(cfgs, mediasource.Config{ID: id, Type: typ, Index: i})
	}
	set, err := mediasource.NewSet(kinds, cfgs, mediasource.Deps{Logger: quiet()}, sourcetest.OwnPlaces{})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// newAdapter serves lib with caps.
func newAdapter(lib *sourcetest.Library, caps mediasource.Capabilities) *adapter {
	return &adapter{Fake: sourcetest.NewFake(lib, caps)}
}

// restart stops the scanner and runs a new one over the same Set, store
// and status, as a process restart would, the store's facts kept.
func (r *rig) restart(t *testing.T) {
	t.Helper()
	r.stop()
	r.scanner = scan.New(scan.Deps{Store: r.db, Status: r.status, Logger: quiet(), Schedule: r.sched}, r.set)
	r.start(t)
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// start runs the scanner until the test ends.
func (r *rig) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.scanner.Run(ctx) }()
	var once sync.Once
	r.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Run = %v", err)
			}
		})
	}
	t.Cleanup(r.stop)
	synctest.Wait()
}

// advance moves the bubble's clock by d and lets every loop settle.
func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// movieRoot is movie n with one item at /movies/n.mkv.
func movieRoot(n int) sourcetest.LibRoot {
	id := mediasource.NativeID(fmt.Sprint(n))
	return sourcetest.LibRoot{
		Root:  mediasource.Root{Native: movieRef(n), Title: fmt.Sprintf("Movie %d", n), External: media.ExternalIDs{TMDB: uint64(n)}},
		Items: []mediasource.Item{{Native: id, VideoPath: fmt.Sprintf("/movies/%d.mkv", n), External: media.ExternalIDs{TMDB: uint64(n)}}},
	}
}

func movieRef(n int) mediasource.NativeRef {
	return mediasource.NativeRef{ID: mediasource.NativeID(fmt.Sprint(n)), Kind: media.Movie}
}

// put lists movies from to through, each its own event.
func (r *rig) put(from, through int) {
	for n := from; n <= through; n++ {
		r.lib.Put(movieRoot(n))
	}
}

// listed answers the stored roots' titles in key order.
func (r *rig) listed(t *testing.T) []string {
	t.Helper()
	rows, _, err := r.db.Roots(t.Context(), "", 500)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Root.Title)
	}
	slices.Sort(out)
	return out
}

// head is the Fake's head cursor now.
func (r *rig) head(t *testing.T) []byte {
	t.Helper()
	_, c, err := r.ad.Fake.Changes(t.Context(), mediasource.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.MarshalBinary()
	return b
}

// stored is the cursor the store holds, nil when none.
func (r *rig) stored(t *testing.T) []byte {
	t.Helper()
	c, ok, err := r.db.Store.SourceCursor(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return nil
	}
	b, _ := c.MarshalBinary()
	return b
}

// storeCursor completes one empty batch pass storing the Fake's head, as an
// earlier run of the loop would have.
func (r *rig) storeCursor(t *testing.T) []byte {
	t.Helper()
	_, c, err := r.ad.Fake.Changes(t.Context(), mediasource.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.db.BeginPass(t.Context(), src, statedb.PassScope{Roots: []mediasource.NativeRef{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Store.FinishPass(t.Context(), src, p, true, c, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := c.MarshalBinary()
	return b
}

// observeOther completes one whole pass of other over roots, as its own
// loop would, so src's sightings of the same videos overlap it.
func (r *rig) observeOther(t *testing.T, roots ...sourcetest.LibRoot) {
	t.Helper()
	p, err := r.db.BeginPass(t.Context(), other, statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, lr := range roots {
		if err := r.db.Store.ObserveItems(t.Context(), other, p, lr.Root, lr.Items); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Store.FinishPass(t.Context(), other, p, true, mediasource.Cursor{}, nil); err != nil {
		t.Fatal(err)
	}
}

// alert answers the live alert of source, false when none.
func (r *rig) alert(source status.AlertSource) (status.Alert, bool) {
	snap, _ := r.jobs.Snapshot()
	for _, a := range snap.Alerts {
		if a.Source == source {
			return a, true
		}
	}
	return status.Alert{}, false
}

// scanWork drains and answers the queued Scan work's instances.
func (r *rig) scanWork(t *testing.T) int {
	t.Helper()
	ws, err := r.db.NextWork(t.Context(), 500)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, w := range ws {
		if w.Cause == statedb.Scan {
			n++
		}
	}
	if err := r.db.AckWork(t.Context(), ws); err != nil {
		t.Fatal(err)
	}
	return n
}

// liveJobs answers the live jobs of kind, newest first.
func (r *rig) liveJobs(kind status.Kind) []status.Job {
	snap, _ := r.jobs.Snapshot()
	var out []status.Job
	for _, j := range snap.Jobs {
		if j.Kind == kind && j.State != status.Done {
			out = append(out, j)
		}
	}
	return out
}

// job reads id's current record.
func (r *rig) job(t *testing.T, id status.JobID) status.Job {
	t.Helper()
	j, ok, err := r.jobs.Get(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("Get(%s) = %v, %v", id, ok, err)
	}
	return j
}

// gaps answers the time between consecutive calls, and from since to the
// first.
func gaps(since time.Time, calls []call) []time.Duration {
	out := make([]time.Duration, 0, len(calls))
	prev := since
	for _, c := range calls {
		out = append(out, c.at.Sub(prev))
		prev = c.at
	}
	return out
}

// blockWalk makes the walk yield nothing until its context ends, and
// answers a channel closed once it has started.
func (a *adapter) blockWalk() <-chan struct{} {
	started := make(chan struct{})
	var once sync.Once
	a.setItems(func(ctx context.Context, _ func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: ctx.Err()}
	})
	return started
}

// walkStarted fails the test unless the walk blockWalk installed has
// started; it never blocks, so the bubble's clock stays where the test put it.
func walkStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	default:
		t.Fatalf("at %v no walk has started", time.Now())
	}
}

func unreachable(msg string) *mediasource.SourceError {
	return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: fmt.Errorf("%s", msg)}
}

var movieCaps = mediasource.Capabilities{Kinds: []media.Kind{media.Movie}, Changes: true}

func sameBytes(a, b []byte) bool { return bytes.Equal(a, b) }
