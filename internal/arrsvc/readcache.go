package arrsvc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/cache"
)

// fetchFn is one upstream pass for one key, returning the entry payload.
type fetchFn func(ctx context.Context) (any, error)

// readEntry is the cached value of one arr-read key: the payload (a snapshot
// type carrying its derived indexes inside the value) and the instant its
// upstream read began. Written atomically as one Set; immutable by
// convention — consumers receive it read-only.
type readEntry struct {
	payload   any
	readBegin time.Time
}

// wave is one shared recovery pass for one key. It is joinable from record
// creation until read-begin; join and read-begin are serialized under the
// owning keyState's mutex, so a join happens-before start or not at all.
type wave struct {
	done       chan struct{} // closed at settle; val/err are readable after
	discard    chan struct{} // closed when the last pre-start waiter detaches
	val        any
	err        error
	eligibleAt time.Time // the scheduled instant — never the freshness measurand
	waiters    int       // guarded by keyState.mu; meaningful pre-start only
	started    bool      // guarded by keyState.mu: read-begin happened
	discarded  bool      // guarded by keyState.mu
}

// settle publishes the wave's outcome to every waiter.
func (w *wave) settle(v any, err error) {
	w.val, w.err = v, err
	close(w.done)
}

// plainFlight is one in-progress plain fetch shared by concurrent plain
// readers of one key (the wrapper's own singleflight).
type plainFlight struct {
	done chan struct{}
	val  any
	err  error
}

// keyState is the per-key coordination record. Its mutex serializes the
// joinable→started wave transition, the read-begin comparison, and the cache
// Set; it is never held across an arr call.
type keyState struct {
	live          *wave        // the joinable (pre-read-begin) wave, if any
	flight        *plainFlight // the in-progress plain fetch, if any
	lastWaveStart time.Time    // floor clock: newest wave read-begin (scan write-through included)
	lastCommit    time.Time    // write ordering: newest committed read-begin
	mu            sync.Mutex
}

// readTable is the per-arr-instance half of the wrapper: the entry stores and
// the per-key wave/flight coordination. A config reload publishes a fresh
// instance, so an old instance's in-flight wave writes land in an orphaned
// table — revoked — and post-reload reads start cold against the new arr
// client.
type readTable struct {
	gate     *ReadGate
	cache    *cache.Cache[readEntry]
	episodes *episodeStore
	keys     map[string]*keyState
	mu       sync.Mutex
}

func newReadTable(gate *ReadGate) *readTable {
	return &readTable{
		gate:     gate,
		cache:    cache.New[readEntry](arrCacheTTL),
		episodes: newEpisodeStore(),
		keys:     make(map[string]*keyState),
	}
}

// lookup answers from the store the key's family lives in.
func (t *readTable) lookup(key string) (readEntry, bool) {
	if isEpisodesKey(key) {
		return t.episodes.get(key)
	}
	return t.cache.Get(key)
}

// put stores an entry in the store the key's family lives in.
func (t *readTable) put(key string, e readEntry) {
	if isEpisodesKey(key) {
		t.episodes.put(key, e)
		return
	}
	t.cache.Set(key, e)
}

// key returns the coordination record for key, creating it on first use.
func (t *readTable) key(key string) *keyState {
	t.mu.Lock()
	defer t.mu.Unlock()
	ks, ok := t.keys[key]
	if !ok {
		ks = &keyState{}
		t.keys[key] = ks
	}
	return ks
}

// read dispatches on the context's recovery marker: marked reads are served
// by a wave whose read began at or after this call, plain reads by the cache
// or the per-key singleflight.
func (t *readTable) read(ctx context.Context, key string, plain, wave fetchFn) (any, error) {
	if rec, ok := recoveryFrom(ctx); ok {
		return t.waveRead(ctx, rec, key, wave)
	}
	return t.plainRead(ctx, key, plain)
}

// commitLocked applies the write-ordering rule — EVERY writer's write lands
// only if its read-begin is newer than the entry's current one — and stores
// the entry. The caller holds ks.mu.
func (t *readTable) commitLocked(ks *keyState, key string, e readEntry) {
	if !ks.lastCommit.IsZero() && !e.readBegin.After(ks.lastCommit) {
		return
	}
	ks.lastCommit = e.readBegin
	t.put(key, e)
}

func (t *readTable) commit(key string, e readEntry) {
	ks := t.key(key)
	ks.mu.Lock()
	defer ks.mu.Unlock()
	t.commitLocked(ks, key, e)
}

// writeThrough registers a scan-engine fetch as an already-begun wave: the
// floor clock advances to its read-begin and its write competes under
// newest-write-wins. It never passes the admission queue or budgets.
func (t *readTable) writeThrough(key string, e readEntry) {
	ks := t.key(key)
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if e.readBegin.After(ks.lastWaveStart) {
		ks.lastWaveStart = e.readBegin
	}
	t.commitLocked(ks, key, e)
}

// plainRead answers from the cache, else joins or starts the key's plain
// singleflight. The fetch runs detached from every waiter's context (the
// GetOrFetchCtx precedent), so the leader's request dying mid-flight never
// robs a joiner; each waiter still honors its own context. A marked read
// never joins a plain flight and a plain read never joins a wave.
func (t *readTable) plainRead(ctx context.Context, key string, fetch fetchFn) (any, error) {
	if e, ok := t.lookup(key); ok {
		return e.payload, nil
	}
	ks := t.key(key)
	ks.mu.Lock()
	if f := ks.flight; f != nil {
		ks.mu.Unlock()
		return awaitFlight(ctx, f)
	}
	f := &plainFlight{done: make(chan struct{})}
	ks.flight = f
	ks.mu.Unlock()

	fctx := context.WithoutCancel(ctx)
	go func() {
		begin := time.Now()
		v, err := fetch(fctx)
		if err == nil {
			f.val = v
			t.commit(key, readEntry{payload: v, readBegin: begin})
		} else {
			f.err = err // errors are never cached
		}
		ks.mu.Lock()
		if ks.flight == f {
			ks.flight = nil
		}
		ks.mu.Unlock()
		close(f.done)
	}()
	return awaitFlight(ctx, f)
}

func awaitFlight(ctx context.Context, f *plainFlight) (any, error) {
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// waveRead serves one marked read: admit (join a live pre-read wave, start
// one under the floor, or join the shared follow-up), then wait, bounded by
// the request's one deadline. Expiry answers THIS reader the typed refusal
// immediately; the wave may complete for other waiters.
func (t *readTable) waveRead(ctx context.Context, rec recoveryState, key string, fetch fetchFn) (any, error) {
	if !rec.deadline.After(time.Now()) {
		return nil, fmt.Errorf("%w: request deadline (%s) expired before the %s read", ErrRecoveryRefused, requestDeadline, key)
	}
	ks := t.key(key)
	w := t.admit(ks, key, fetch)

	deadline := time.NewTimer(time.Until(rec.deadline))
	defer deadline.Stop()
	select {
	case <-w.done:
		return w.val, w.err
	case <-ctx.Done():
		t.detach(ks, w)
		return nil, ctx.Err()
	case <-deadline.C:
		t.detach(ks, w)
		return nil, fmt.Errorf("%w: request deadline (%s) expired waiting for %s", ErrRecoveryRefused, requestDeadline, key)
	}
}

// admit applies the admission arms in order: (1) JOIN any live pre-read
// wave; (2) else create a wave now if no wave for the key BEGAN a read
// within the floor; (3) else SCHEDULE the shared follow-up at the previous
// wave's start + floor and join it — delay, never pre-arrival data. Every
// wave start is ≥ every joiner's arrival by construction.
func (t *readTable) admit(ks *keyState, key string, fetch fetchFn) *wave {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if w := ks.live; w != nil {
		w.waiters++
		return w
	}
	now := time.Now()
	w := &wave{
		done:       make(chan struct{}),
		discard:    make(chan struct{}),
		waiters:    1,
		eligibleAt: now,
	}
	if last := ks.lastWaveStart; !last.IsZero() && now.Sub(last) < waveFloor {
		w.eligibleAt = last.Add(waveFloor)
	}
	ks.live = w
	t.gate.goRun(func() { t.runWave(ks, key, w, fetch) })
	return w
}

// detach removes one waiter. Before read-begin, the last waiter's departure
// discards the wave unexecuted; after read-begin the pass proceeds
// regardless (a waiter's cancellation never cancels the pass).
func (t *readTable) detach(ks *keyState, w *wave) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if w.started || w.discarded {
		return
	}
	w.waiters--
	if w.waiters > 0 {
		return
	}
	w.discarded = true
	if ks.live == w {
		ks.live = nil
	}
	close(w.discard)
}

// settleUnstarted settles a wave that never reached read-begin (refusal or
// shutdown), closing the join window first so no later arrival joins a
// settled wave.
func (t *readTable) settleUnstarted(ks *keyState, w *wave, err error) {
	ks.mu.Lock()
	if ks.live == w {
		ks.live = nil
	}
	ks.mu.Unlock()
	w.settle(nil, err)
}

// runWave executes one wave: hold until the scheduled instant, admit through
// the global FIFO under the admission budget, re-check liveness, then run
// the single-attempt pass under the execution budget on a context derived
// from the server lifetime. The per-key lock protects only the
// joinable→started transition and is released before the arr call.
func (t *readTable) runWave(ks *keyState, key string, w *wave, fetch fetchFn) {
	life := t.gate.lifetime()

	if d := time.Until(w.eligibleAt); d > 0 {
		hold := time.NewTimer(d)
		select {
		case <-hold.C:
		case <-w.discard:
			hold.Stop()
			return // discarded unexecuted: timer cancelled, no waiters left
		case <-life.Done():
			hold.Stop()
			t.settleUnstarted(ks, w, fmt.Errorf("%w: server shutting down", ErrRecoveryFailed))
			return
		}
	}

	switch t.gate.queue.acquire(w.eligibleAt.Add(admissionBudget), w.discard, life.Done()) {
	case admitGranted:
	case admitDiscarded:
		return // queued acquire cancelled; nothing held, nothing to settle
	case admitShutdown:
		t.settleUnstarted(ks, w, fmt.Errorf("%w: server shutting down", ErrRecoveryFailed))
		return
	case admitTimedOut:
		t.settleUnstarted(ks, w, fmt.Errorf("%w: wave for %s not admitted within %s of its scheduled instant",
			ErrRecoveryRefused, key, admissionBudget))
		return
	}
	defer t.gate.queue.release()

	ks.mu.Lock()
	if w.discarded {
		// Post-permit discard: liveness re-checked after permit acquisition;
		// the deferred release advances the FIFO.
		ks.mu.Unlock()
		return
	}
	start := time.Now() // THE read-begin: after the permit, immediately before the arr call
	w.started = true
	ks.lastWaveStart = start
	ks.live = nil
	ks.mu.Unlock()

	cctx, cancel := context.WithTimeout(life, executionBudget)
	v, err := fetch(cctx)
	cancel()
	if err != nil {
		w.settle(nil, fmt.Errorf("%w: %w", ErrRecoveryFailed, err))
		return
	}
	t.commit(key, readEntry{payload: v, readBegin: start})
	w.settle(v, nil)
}
