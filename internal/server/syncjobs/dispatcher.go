// Package syncjobs owns the server-side async sync job machinery (D1, D2):
// a bounded FIFO dispatcher with an admission lease, the job record registry
// GET /api/sync/jobs serves, and the sync:done publication seam.
//
// The dispatcher accepts at most MaxJobs top-level entries (queued plus
// running); overflow is a typed capacity refusal the handler maps to 429, and
// a same-file dispatch answers the EXISTING job's ids even at cap. Entries
// run ONE at a time in FIFO order — a queued single behind a season batch
// waits, visibly — while the syncworker semaphore stays the execution-slot
// authority (the automatic sync path interleaves there between entries). A
// season batch (batch.go) is ONE entry holding N item job records.
//
// Locking: mu > activity.Log's lock (Dispatch creates the activity entry
// under mu so a same-file race cannot observe a half-registered job); the
// stop registry's callbacks run lock-free (a context cancel). No path takes
// a foreign lock and then mu.
package syncjobs

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
)

// MaxJobs is the admission lease: at most this many accepted (queued or
// running) top-level entries; the next dispatch answers a typed 429.
const MaxJobs = 8

// DefaultRegistryCap bounds retained terminal records between prune ticks.
// Cap eviction removes only records already past retention, so within the
// retention window every record survives whatever the count.
const DefaultRegistryCap = 256

// JobState is a job's lifecycle position. (Named for the flat TS wire
// namespace, where a bare State would collide with the activity vocabulary.)
type JobState string

// Job states: queued → running (at the admission hook) → done.
const (
	StateQueued  JobState = "queued"
	StateRunning JobState = "running"
	StateDone    JobState = "done"
)

// Job is one sync job record: the wire shape GET /api/sync/jobs serves.
// JobID is a NUMERIC process sequence — distinct from the activity log's
// string ids, which the record carries beside it. The registry is truth
// independent of the activity ring; a restart drops it (jobs are not
// persisted — the client re-attaches via this read and finds nothing).
type Job struct {
	AcceptedAt      time.Time          `json:"accepted_at"`
	StartedAt       *time.Time         `json:"started_at,omitempty"`
	EndedAt         *time.Time         `json:"ended_at,omitempty"`
	BatchActivityID string             `json:"batch_activity_id,omitempty"`
	Method          string             `json:"method,omitempty"`
	Error           string             `json:"error,omitempty"`
	State           JobState           `json:"state"`
	Outcome         subflux.JobOutcome `json:"outcome,omitempty"`
	ActivityID      string             `json:"activity_id"`
	FileRef         resolve.FileRef    `json:"file_ref"`
	JobID           int64              `json:"job_id"`
	OffsetMs        int64              `json:"offset_ms,omitempty"`
	SeriesID        int                `json:"series_id,omitempty"`
	Season          int                `json:"season,omitempty"`
	Ordinal         int                `json:"ordinal,omitempty"`
	Confidence      float64            `json:"confidence,omitempty"`
	Applied         bool               `json:"applied,omitempty"`
	DryRun          bool               `json:"dry_run,omitempty"`
}

// ExecInput is one job's work order, resolved at accept time (the handler
// validates and resolves before the 202; the executor reads the file at run
// time, so the analysis sees current content).
type ExecInput struct {
	SubtitlePath string
	VideoPath    string
	Ref          resolve.FileRef
	DryRun       bool
}

// ExecResult is the executor's answer for one job. OffsetMs is CUMULATIVE
// (the stored offset plus this run's correction).
type ExecResult struct {
	Err        error
	Method     string
	Outcome    subflux.JobOutcome
	OffsetMs   int64
	Confidence float64
	Applied    bool
}

// ExecFunc runs one job's analysis. The hook MUST be invoked exactly once at
// execution-slot acquisition (the typed core does this); returning false
// refuses the run and the executor reports subflux.JobCancelled.
type ExecFunc func(ctx context.Context, in *ExecInput, hook func() bool) ExecResult

// Accepted is a dispatch answer: the two ids the 202 hands the client, and
// whether they name a pre-existing live job (same-file dedupe).
type Accepted struct {
	ActivityID string
	JobID      int64
	Existing   bool
}

// CancelOutcome is Cancel's verdict for the activity endpoints.
type CancelOutcome int

// Cancel verdicts.
const (
	// CancelUnknown: the activity id names no sync job; callers fall through
	// to the legacy activity path.
	CancelUnknown CancelOutcome = iota
	// CancelledQueued: a queued entry was cancelled — settled done(cancelled),
	// slot released IMMEDIATELY (the next 202 enters), no success event.
	CancelledQueued
	// CancelConverted: the delete lost the race to admission and converted to
	// a running-cancel through the just-registered stop entry — 204; the job
	// settles done(cancelled) and releases capacity on worker exit.
	CancelConverted
	// CancelTerminal: the job is already done; dismissal is the caller's
	// (the registry is never touched by a terminal row's dismissal).
	CancelTerminal
)

// Typed dispatch refusals.
var (
	// ErrCapacity: the admission lease is full — mapped to the wire as 429.
	ErrCapacity = errors.New("sync job capacity reached")
	// ErrShuttingDown: the dispatcher has drained; no new work is accepted.
	ErrShuttingDown = errors.New("sync dispatcher is shutting down")
)

// Deps wires the dispatcher's collaborators.
type Deps struct {
	// Exec runs one job's analysis (synchandlers' audio executor).
	Exec ExecFunc
	// Log is the activity log: one entry per job, created queued at accept.
	Log *activity.Log
	// Stops is the live stop registry; the admission hook registers each
	// single-file job's stop entry there BEFORE publishing running.
	Stops *activity.StopRegistry
	// PublishDone publishes the sync:done event (EventBus.PublishSyncDone).
	PublishDone func(ev *events.SyncDoneEvent)
	// RegistryCap overrides DefaultRegistryCap when positive (tests).
	RegistryCap int
}

// job is the dispatcher's internal record: the wire Job plus run state.
// batch is non-nil for a season batch item, whose admission slot and stop
// entry belong to the batch.
type job struct {
	cancel        context.CancelFunc
	unregister    func()
	batch         *batch
	subtitlePath  string
	videoPath     string
	record        Job
	cancelPending bool
	popped        bool
}

// queueEntry is one FIFO slot: a single-file job or a season batch.
type queueEntry struct {
	batch *batch // non-nil for a season batch
	jobID int64  // the single job's id when batch is nil
}

// Dispatcher owns the FIFO, the admission lease, the dedupe map, per-job
// cancels, and the job registry. One Run loop executes entries in order.
type Dispatcher struct {
	deps       Deps
	jobs       map[int64]*job
	byActivity map[string]int64
	batches    map[string]*batch
	dedupe     map[string]int64
	wake       chan struct{}
	queue      []queueEntry
	cap        int
	nextID     int64
	accepted   int
	closed     bool
	mu         sync.Mutex
}

// New builds a Dispatcher over deps. Call Run exactly once to start it.
func New(deps Deps) *Dispatcher {
	capacity := deps.RegistryCap
	if capacity <= 0 {
		capacity = DefaultRegistryCap
	}
	return &Dispatcher{
		deps:       deps,
		jobs:       make(map[int64]*job),
		byActivity: make(map[string]int64),
		batches:    make(map[string]*batch),
		dedupe:     make(map[string]int64),
		wake:       make(chan struct{}, 1),
		cap:        capacity,
	}
}

// Dispatch enqueues one single-file job: non-blocking, atomically reserving
// an admission slot. A live job for the same subtitle file answers its
// EXISTING ids (even at cap); a full lease answers ErrCapacity (429 on the
// wire, never auto-retried by the client).
func (d *Dispatcher) Dispatch(in *ExecInput) (Accepted, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return Accepted{}, ErrShuttingDown
	}
	if id, ok := d.dedupe[in.SubtitlePath]; ok {
		j := d.jobs[id]
		return Accepted{JobID: id, ActivityID: j.record.ActivityID, Existing: true}, nil
	}
	if d.accepted >= MaxJobs {
		return Accepted{}, ErrCapacity
	}
	d.accepted++
	d.nextID++
	id := d.nextID

	// The activity entry is created under mu (lock order: mu > Log's) so a
	// concurrent same-file dispatch can never observe a job without its
	// activity id. Born queued in ONE upsert; the admission hook flips it to
	// running.
	actID := d.deps.Log.StartQueued("Audio Sync", filepath.Base(in.SubtitlePath), activity.SourceManual)

	j := &job{
		subtitlePath: in.SubtitlePath,
		videoPath:    in.VideoPath,
		record: Job{
			JobID:      id,
			ActivityID: actID,
			FileRef:    in.Ref,
			State:      StateQueued,
			DryRun:     in.DryRun,
			AcceptedAt: time.Now(),
		},
	}
	d.jobs[id] = j
	d.byActivity[actID] = id
	d.dedupe[in.SubtitlePath] = id
	d.queue = append(d.queue, queueEntry{jobID: id})
	d.evictLocked()
	d.poke() // non-blocking, safe under mu
	return Accepted{JobID: id, ActivityID: actID}, nil
}

// poke nudges the run loop (coalescing signal; extra sends dropped).
func (d *Dispatcher) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Cancel routes an activity-id cancellation into the dispatcher. See the
// CancelOutcome constants for the verdict semantics. A batch activity id
// addresses the WHOLE batch (the batch is the cancellation unit); its item
// records are settled by the batch arms, never individually.
func (d *Dispatcher) Cancel(activityID string) CancelOutcome {
	d.mu.Lock()
	if b, ok := d.batches[activityID]; ok {
		return d.cancelBatchLocked(b) // unlocks
	}
	id, ok := d.byActivity[activityID]
	if !ok {
		d.mu.Unlock()
		return CancelUnknown
	}
	j := d.jobs[id]
	switch {
	case j.record.State == StateDone:
		d.mu.Unlock()
		return CancelTerminal
	case j.record.State == StateQueued && !j.popped:
		// Still in the queue: settle done(cancelled) here — retained,
		// dedupe and capacity released immediately, no success event.
		d.queue = slices.DeleteFunc(d.queue, func(e queueEntry) bool { return e.batch == nil && e.jobID == id })
		d.settleLocked(j, ExecResult{Outcome: subflux.JobCancelled, Err: context.Canceled})
		d.mu.Unlock()
		d.deps.Log.FinishCancelled(activityID)
		return CancelledQueued
	case j.record.State == StateQueued:
		// Popped but not yet admitted: the cancel wins at the admission hook
		// (which refuses), or the context cancel unblocks the slot wait.
		// Either way the run loop settles done(cancelled).
		j.cancelPending = true
		cancel := j.cancel
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return CancelConverted
	default:
		// Running: the delete lost the race to admission. Convert to a
		// running-cancel through the just-registered stop entry; capacity
		// releases when the worker exits.
		d.mu.Unlock()
		d.deps.Stops.RequestStop(activityID)
		return CancelConverted
	}
}

// settleLocked moves j to done with res, releasing its dedupe entry — and,
// for a single-file job, its admission slot (a batch item's slot belongs to
// the batch, released once by finishBatchLocked). Caller holds mu and owns
// the post-unlock activity transition and any event publish.
func (d *Dispatcher) settleLocked(j *job, res ExecResult) {
	if j.record.State == StateDone {
		return
	}
	if j.unregister != nil {
		// Unregister BEFORE (atomically with, under mu) terminal publish.
		j.unregister()
		j.unregister = nil
	}
	now := time.Now()
	j.record.State = StateDone
	j.record.Outcome = res.Outcome
	j.record.EndedAt = &now
	j.record.OffsetMs = res.OffsetMs
	j.record.Confidence = res.Confidence
	j.record.Method = res.Method
	j.record.Applied = res.Applied
	if res.Err != nil {
		j.record.Error = res.Err.Error()
	}
	delete(d.dedupe, j.subtitlePath)
	if j.batch == nil {
		d.accepted--
	}
}

// Run executes queued entries one at a time in FIFO order until ctx (the
// server lifetime) dies, then drains: queued jobs settle done(cancelled)
// without ever executing, and the in-flight worker has already been waited
// for (its context derives from ctx, so the child is killed and reaped).
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			d.drain()
			return
		case <-d.wake:
		}
		if !d.runQueued(ctx) {
			d.drain()
			return
		}
	}
}

// runQueued executes every queued entry in FIFO order until the queue
// empties, reporting whether it emptied: false means ctx died mid-drain and
// the caller owns the drain.
func (d *Dispatcher) runQueued(ctx context.Context) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		j, b := d.pop()
		switch {
		case b != nil:
			d.runBatch(ctx, b)
		case j != nil:
			d.runJob(ctx, j)
		default:
			return true
		}
	}
}

// pop removes the FIFO head. A job record stays queued until the admission
// hook flips it; popped marks the in-flight-toward-admission window for
// Cancel's race arms (batches mirror it between pop and batch start).
func (d *Dispatcher) pop() (*job, *batch) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queue) == 0 {
		return nil, nil
	}
	e := d.queue[0]
	d.queue = d.queue[1:]
	if e.batch != nil {
		e.batch.popped = true
		return nil, e.batch
	}
	j := d.jobs[e.jobID]
	j.popped = true
	return j, nil
}

// runJob executes one entry through the injected executor and settles it.
func (d *Dispatcher) runJob(ctx context.Context, j *job) {
	d.mu.Lock()
	if j.cancelPending || j.record.State == StateDone {
		// A cancel won before anything started (or the queued-cancel already
		// settled it).
		settled := j.record.State == StateDone
		if !settled {
			d.settleLocked(j, ExecResult{Outcome: subflux.JobCancelled, Err: context.Canceled})
		}
		actID := j.record.ActivityID
		d.mu.Unlock()
		if !settled {
			d.deps.Log.FinishCancelled(actID)
		}
		return
	}
	// The per-job context derives from the run loop's (the server lifetime),
	// so shutdown cancels running work; a stop request cancels just this one.
	jobCtx, cancel := context.WithCancel(ctx)
	j.cancel = cancel
	d.mu.Unlock()
	defer cancel()

	hook := func() bool {
		d.mu.Lock()
		if j.cancelPending {
			d.mu.Unlock()
			return false
		}
		// Stop entry FIRST, then the running publish — a stop request
		// arriving between running-publish and registration is thereby
		// unrepresentable.
		j.unregister = d.deps.Stops.RegisterStop(j.record.ActivityID, cancel)
		now := time.Now()
		j.record.State = StateRunning
		j.record.StartedAt = &now
		d.mu.Unlock()
		d.deps.Log.SetQueued(j.record.ActivityID, false)
		return true
	}

	res := d.deps.Exec(jobCtx, &ExecInput{
		Ref:          j.record.FileRef,
		SubtitlePath: j.subtitlePath,
		VideoPath:    j.videoPath,
		DryRun:       j.record.DryRun,
	}, hook)

	d.mu.Lock()
	ran := j.record.StartedAt != nil
	d.settleLocked(j, res)
	rec := j.record
	d.mu.Unlock()

	switch res.Outcome {
	case subflux.JobResult:
		d.deps.Log.End(rec.ActivityID)
	case subflux.JobCancelled:
		d.deps.Log.FinishCancelled(rec.ActivityID)
	default:
		d.deps.Log.Fail(rec.ActivityID)
	}
	if ran || res.Outcome != subflux.JobCancelled {
		// sync:done settles the dialog for every job whose worker ran AND
		// for a pre-admission failure (the client must not wait forever); a
		// never-admitted CANCELLATION publishes no success event (the
		// registry and the activity terminal are its record).
		d.deps.PublishDone(&events.SyncDoneEvent{
			JobID:           rec.JobID,
			BatchActivityID: rec.BatchActivityID,
			FileRef:         rec.FileRef,
			Outcome:         rec.Outcome,
			OffsetMs:        rec.OffsetMs,
			Confidence:      rec.Confidence,
			Method:          rec.Method,
			Applied:         rec.Applied,
			DryRun:          rec.DryRun,
			Error:           rec.Error,
		})
	}
	if res.Outcome != subflux.JobResult && res.Outcome != subflux.JobCancelled {
		slog.Warn("sync job failed",
			"job_id", rec.JobID, "activity_id", rec.ActivityID,
			"outcome", string(res.Outcome), "error", rec.Error)
	}
}

// drain settles every still-queued entry as done(cancelled) — settle, never
// execute; whole batches included — and refuses further dispatches.
func (d *Dispatcher) drain() {
	d.mu.Lock()
	d.closed = true
	queued := d.queue
	d.queue = nil
	var acts []string
	for _, e := range queued {
		if e.batch != nil {
			if e.batch.state == StateDone {
				continue
			}
			d.settleBatchItemsLocked(e.batch, ErrShuttingDown)
			d.finishBatchLocked(e.batch)
			acts = append(acts, e.batch.activityID)
			continue
		}
		j := d.jobs[e.jobID]
		if j.record.State != StateQueued {
			continue
		}
		d.settleLocked(j, ExecResult{Outcome: subflux.JobCancelled, Err: ErrShuttingDown})
		acts = append(acts, j.record.ActivityID)
	}
	d.mu.Unlock()
	for _, id := range acts {
		d.deps.Log.FinishCancelled(id)
	}
}

// Jobs snapshots the registry in the total order accepted_at DESC, job_id
// DESC (numeric), optionally filtered by batch_activity_id. Batch item
// records live here like any job; the batch itself is not a record — its
// aggregate is the activity entry.
func (d *Dispatcher) Jobs(batchActivityID string) []Job {
	d.mu.Lock()
	out := make([]Job, 0, len(d.jobs))
	for _, j := range d.jobs {
		if batchActivityID != "" && j.record.BatchActivityID != batchActivityID {
			continue
		}
		out = append(out, j.record)
	}
	d.mu.Unlock()
	slices.SortFunc(out, func(a, b Job) int {
		if c := b.AcceptedAt.Compare(a.AcceptedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.JobID, a.JobID)
	})
	return out
}

// Prune removes done records older than maxAge (retention =
// activity.DefaultPruneAge, one owner: the server's prune ticker drives
// both). Queued and running records are never evicted, and neither is a
// done item of a still-live batch — the batch finalizers dereference every
// item id, and the reload read lists the full item set. Terminal batch
// entries age out with their items.
func (d *Dispatcher) Prune(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, j := range d.jobs {
		if j.batch != nil && j.batch.state != StateDone {
			continue
		}
		if j.record.State == StateDone && j.record.EndedAt != nil && j.record.EndedAt.Before(cutoff) {
			delete(d.jobs, id)
			delete(d.byActivity, j.record.ActivityID)
		}
	}
	for id, b := range d.batches {
		if b.state == StateDone && b.endedAt.Before(cutoff) {
			delete(d.batches, id)
		}
	}
}

// evictLocked enforces the registry cap: when the registry exceeds it, the
// oldest done records PAST RETENTION are evicted — never queued or running
// records, never an item of a still-live batch, and never a record still
// inside the retention window (the cap bounds between-tick growth without
// shortening the visibility promise).
func (d *Dispatcher) evictLocked() {
	if len(d.jobs) <= d.cap {
		return
	}
	cutoff := time.Now().Add(-activity.DefaultPruneAge)
	type candidate struct {
		ended time.Time
		id    int64
	}
	var evictable []candidate
	for id, j := range d.jobs {
		if j.batch != nil && j.batch.state != StateDone {
			continue
		}
		if j.record.State == StateDone && j.record.EndedAt != nil && j.record.EndedAt.Before(cutoff) {
			evictable = append(evictable, candidate{ended: *j.record.EndedAt, id: id})
		}
	}
	slices.SortFunc(evictable, func(a, b candidate) int { return a.ended.Compare(b.ended) })
	for _, c := range evictable {
		if len(d.jobs) <= d.cap {
			return
		}
		delete(d.byActivity, d.jobs[c.id].record.ActivityID)
		delete(d.jobs, c.id)
	}
}
