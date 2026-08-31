// The season batch (D2): ONE dispatcher entry whose items run sequentially
// through the injected executor. The batch is the CANCELLATION UNIT — one
// stop entry registered at batch start whose callback cancels the batch
// context; item leases never touch the stop registry, so there is no
// overwrite/unregister window between items. Every per-item fact lives in
// the job registry (all item records created at acceptance, ordinal set);
// the batch's activity entry carries the aggregate only.

package syncjobs

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
)

// BatchInput is one season batch's work order: pre-resolved items in ordinal
// order (the handler enumerates and resolves paths at accept time; the
// executor re-reads file content at run time) plus the aggregate identity
// the batch activity entry carries.
type BatchInput struct {
	// Detail is the batch activity entry's aggregate detail line.
	Detail string
	// Items in ordinal order; item i is ordinal i+1.
	Items []ExecInput
	// SeriesID and Season scope the batch: a dispatch for a scope that
	// already has a live batch answers that batch's id (idempotent).
	SeriesID int
	Season   int
}

// BatchAccepted is a season dispatch answer.
type BatchAccepted struct {
	// ActivityID is the batch activity entry's id — the 202 body, the
	// registry filter key, and the stop/cancel address.
	ActivityID string
	// Existing reports the idempotent same-scope answer: a live batch for
	// this (series, season) already exists and its id was returned.
	Existing bool
	// SkippedLive counts items not registered because their subtitle file
	// already has a live job (at most one live job per file — the dedupe
	// invariant; that job's own sync:done settles the file).
	SkippedLive int
}

// batch is the dispatcher's internal record for one season batch: one FIFO
// entry, one admission slot, one stop entry, N item job records.
type batch struct {
	cancel        context.CancelFunc
	unregister    func()
	endedAt       time.Time
	activityID    string
	detail        string
	state         JobState
	itemIDs       []int64
	seriesID      int
	season        int
	cancelPending bool
	popped        bool
}

// DispatchBatch enqueues one season batch: non-blocking, atomically
// reserving ONE admission slot for the whole batch. All item records are
// created NOW (state queued, ordinal set, batch_activity_id riding every
// record), so a registry read before any sync:done lists the full item set.
// A live batch for the same (series, season) answers its EXISTING id; a
// full lease answers ErrCapacity (429 on the wire).
func (d *Dispatcher) DispatchBatch(in *BatchInput) (BatchAccepted, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return BatchAccepted{}, ErrShuttingDown
	}
	for _, b := range d.batches {
		if b.state != StateDone && b.seriesID == in.SeriesID && b.season == in.Season {
			return BatchAccepted{ActivityID: b.activityID, Existing: true}, nil
		}
	}
	if d.accepted >= MaxJobs {
		return BatchAccepted{}, ErrCapacity
	}
	d.accepted++

	// Activity entry under mu, like Dispatch (lock order: mu > Log's).
	actID := d.deps.Log.StartQueued("Season Sync", in.Detail, activity.SourceManual)
	b := &batch{
		activityID: actID,
		detail:     in.Detail,
		seriesID:   in.SeriesID,
		season:     in.Season,
		state:      StateQueued,
	}
	skipped := 0
	now := time.Now()
	for i := range in.Items {
		item := &in.Items[i]
		if _, live := d.dedupe[item.SubtitlePath]; live {
			skipped++
			continue
		}
		d.nextID++
		id := d.nextID
		j := &job{
			batch:        b,
			subtitlePath: item.SubtitlePath,
			videoPath:    item.VideoPath,
			record: Job{
				JobID:           id,
				ActivityID:      actID,
				BatchActivityID: actID,
				SeriesID:        in.SeriesID,
				Season:          in.Season,
				Ordinal:         len(b.itemIDs) + 1,
				FileRef:         item.Ref,
				State:           StateQueued,
				DryRun:          item.DryRun,
				AcceptedAt:      now,
			},
		}
		d.jobs[id] = j
		d.dedupe[item.SubtitlePath] = id
		b.itemIDs = append(b.itemIDs, id)
	}
	d.batches[actID] = b
	d.deps.Log.Progress(actID, 0, len(b.itemIDs), "")
	d.queue = append(d.queue, queueEntry{batch: b})
	d.evictLocked()
	d.poke()
	return BatchAccepted{ActivityID: actID, SkippedLive: skipped}, nil
}

// cancelBatchLocked routes an activity-id cancellation to its batch. Caller
// holds mu; every arm unlocks before its activity transition.
func (d *Dispatcher) cancelBatchLocked(b *batch) CancelOutcome {
	switch {
	case b.state == StateDone:
		d.mu.Unlock()
		return CancelTerminal
	case b.state == StateQueued && !b.popped:
		// Still in the queue: settle the whole batch here — every item
		// done(cancelled) never executed, slot released IMMEDIATELY.
		d.queue = slices.DeleteFunc(d.queue, func(e queueEntry) bool { return e.batch == b })
		d.settleBatchItemsLocked(b, context.Canceled)
		d.finishBatchLocked(b)
		actID := b.activityID
		d.mu.Unlock()
		d.deps.Log.FinishCancelled(actID)
		return CancelledQueued
	case b.state == StateQueued:
		// Popped but not yet started: the run loop's pre-start arm settles.
		b.cancelPending = true
		cancel := b.cancel
		d.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return CancelConverted
	default:
		// Running: convert to a stop through the batch's single stop entry;
		// capacity releases when the batch loop exits.
		d.mu.Unlock()
		d.deps.Stops.RequestStop(b.activityID)
		return CancelConverted
	}
}

// settleBatchItemsLocked settles every still-queued item of b as
// done(cancelled) with cause err — settle, never execute; no sync:done (a
// never-admitted cancellation publishes nothing). Caller holds mu. The
// nil-guard is defense in depth: retention skips a live batch's items, so a
// missing id would mean that invariant broke, not a normal state.
func (d *Dispatcher) settleBatchItemsLocked(b *batch, err error) {
	for _, id := range b.itemIDs {
		j, ok := d.jobs[id]
		if !ok || j.record.State == StateDone {
			continue
		}
		d.settleLocked(j, ExecResult{Outcome: subflux.JobCancelled, Err: err})
	}
}

// finishBatchLocked moves b to done, releasing its admission slot and stop
// entry. Caller holds mu and owns the post-unlock activity transition.
func (d *Dispatcher) finishBatchLocked(b *batch) {
	if b.state == StateDone {
		return
	}
	if b.unregister != nil {
		b.unregister()
		b.unregister = nil
	}
	b.state = StateDone
	b.endedAt = time.Now()
	d.accepted--
}

// runBatch executes one season batch: items sequentially (item N+1 only
// after N terminates; the execution slot is the executor's, released
// between items, so the automatic sync path waits behind at most one item).
func (d *Dispatcher) runBatch(ctx context.Context, b *batch) {
	d.mu.Lock()
	if b.cancelPending || b.state == StateDone {
		settled := b.state == StateDone
		if !settled {
			d.settleBatchItemsLocked(b, context.Canceled)
			d.finishBatchLocked(b)
		}
		actID := b.activityID
		d.mu.Unlock()
		if !settled {
			d.deps.Log.FinishCancelled(actID)
		}
		return
	}
	// The batch context is the cancellation unit: derived from the run
	// loop's (the server lifetime), cancelled by the ONE stop entry
	// registered here at batch start. Stop entry FIRST, then the running
	// publish — a stop request never finds a registration gap, mid-item or
	// between items.
	batchCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.unregister = d.deps.Stops.RegisterStop(b.activityID, cancel)
	b.state = StateRunning
	itemIDs := slices.Clone(b.itemIDs)
	d.mu.Unlock()
	defer cancel()
	d.deps.Log.SetQueued(b.activityID, false)

	total := len(itemIDs)
	for i, id := range itemIDs {
		if batchCtx.Err() != nil {
			break
		}
		d.runBatchItem(batchCtx, id)
		d.deps.Log.Progress(b.activityID, i+1, total, "")
	}

	d.mu.Lock()
	cancelled := batchCtx.Err() != nil
	cause := context.Cause(batchCtx)
	if cause == nil {
		cause = context.Canceled
	}
	d.settleBatchItemsLocked(b, cause)
	applied, failed, low := d.batchOutcomesLocked(itemIDs)
	d.finishBatchLocked(b)
	actID, detail := b.activityID, b.detail
	d.mu.Unlock()

	if cancelled {
		d.deps.Log.FinishCancelled(actID)
		return
	}
	d.deps.Log.Progress(actID, total, total, batchSummary(detail, applied, failed, low))
	d.deps.Log.End(actID)
}

// runBatchItem executes one batch item through the injected executor. The
// item's context IS the batch context; its admission hook flips the item
// record queued→running at slot acquisition — it never touches the stop
// registry.
func (d *Dispatcher) runBatchItem(ctx context.Context, id int64) {
	d.mu.Lock()
	j := d.jobs[id]
	d.mu.Unlock()

	hook := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		// A stop lands as the batch context's cancel (the pre-start cancel
		// race is settled before any item runs).
		if ctx.Err() != nil {
			return false
		}
		now := time.Now()
		j.record.State = StateRunning
		j.record.StartedAt = &now
		return true
	}

	res := d.deps.Exec(ctx, &ExecInput{
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

	if ran || res.Outcome != subflux.JobCancelled {
		// Each item publishes its OWN sync:done, batch_activity_id set —
		// same rule as a single job (a never-admitted cancellation
		// publishes nothing; the registry is its record).
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
}

// batchOutcomesLocked folds the settled items into the aggregate counts the
// terminal summary reports. Caller holds mu. A missing id counts as failed
// (defense in depth beside settleBatchItemsLocked's guard).
func (d *Dispatcher) batchOutcomesLocked(itemIDs []int64) (applied, failed, low int) {
	for _, id := range itemIDs {
		j, ok := d.jobs[id]
		if !ok {
			failed++
			continue
		}
		rec := &j.record
		switch {
		case rec.Outcome == subflux.JobResult && rec.Applied:
			applied++
		case rec.Outcome == subflux.JobResult:
			low++
		default:
			failed++
		}
	}
	return applied, failed, low
}

// batchSummary renders the terminal aggregate detail: the batch label plus
// the counts the old per-client pool reported ("N synced, M failed, K low
// confidence"), zero parts omitted.
func batchSummary(detail string, applied, failed, low int) string {
	s := fmt.Sprintf("%s · %d synced", detail, applied)
	if failed > 0 {
		s += fmt.Sprintf(", %d failed", failed)
	}
	if low > 0 {
		s += fmt.Sprintf(", %d low confidence", low)
	}
	return s
}
