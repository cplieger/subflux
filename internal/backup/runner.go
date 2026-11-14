package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/status"
)

// NameLayout is the one backup file name, a time.Format layout in UTC; its
// time is the last success. A name after now is excluded from the retention
// count and never pruned.
const NameLayout = "subflux-20060102T150405Z.bolt"

// retryAfter is how long a failed run waits before the next attempt;
// ParseConfig's 1 h minimum frequency keeps every schedule longer.
const retryAfter = 15 * time.Minute

// tick is how often Run reads the section and checks whether a run is due.
const tick = time.Minute

// store is the one statedb call the runner makes.
type store interface {
	Backup(ctx context.Context, path string) error
}

type metrics interface {
	SetBackupSchedule(enabled bool, interval time.Duration)
	SetBackupLastSuccess(time.Time)
	IncBackupFailure()
}

// Deps is what a Runner works over. Every field but Logger is required;
// a nil Logger is slog.Default().
type Deps struct {
	Store   store
	Status  *status.Store
	Metrics metrics
	// Config is the backup section in force, read once per tick.
	Config func() Config
	Logger *slog.Logger
}

// Runner keeps the scheduled backups; New builds it and Run drives it.
type Runner struct {
	d Deps
	// warned names each future-dated backup already logged.
	warned map[string]bool
	// retryAt is when the next attempt may run after a failed one; failed
	// is false once a run succeeds or backups are turned off.
	retryAt time.Time
	failed  bool
}

// New builds a Runner over d.
func New(d Deps) *Runner {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Runner{d: d, warned: map[string]bool{}}
}

// Run checks at once and then every minute whether a backup is due, and
// runs it, until ctx ends; it returns nil after the run in progress, whose
// context ctx's end cancels.
func (r *Runner) Run(ctx context.Context) error {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		r.check(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// check is one tick: the schedule gauges, then, when enabled and no failed
// run is waiting, the listing, the last-success gauge and a run when one is
// due or the last failed.
func (r *Runner) check(ctx context.Context) {
	cfg := r.d.Config()
	r.d.Metrics.SetBackupSchedule(cfg.enabled, cfg.frequency)
	if !cfg.enabled {
		r.failed = false
		r.d.Status.Reconcile(status.AlertBackup, false, "")
		return
	}
	now := time.Now()
	if r.failed && now.Before(r.retryAt) {
		return
	}
	past, err := r.list(cfg.path, now)
	if err != nil {
		r.run(ctx, func(context.Context) (time.Time, error) { return time.Time{}, err })
		return
	}
	var last time.Time
	if len(past) > 0 {
		last = past[len(past)-1].at
	}
	r.d.Metrics.SetBackupLastSuccess(last)
	due := last.IsZero() || now.Sub(last) >= cfg.frequency
	if !due && !r.failed {
		return
	}
	r.run(ctx, func(ctx context.Context) (time.Time, error) {
		var made time.Time
		if due {
			b := backupName(now)
			if err := r.d.Store.Backup(ctx, filepath.Join(cfg.path, b.name)); err != nil {
				return time.Time{}, err
			}
			past, made = append(past, b), b.at
		}
		return made, prune(cfg.path, past, cfg.retention)
	})
}

// run records work as a SystemOwner Backup job and ends it with work's
// outcome. A failure counts, raises backup and waits retryAfter; a success
// clears backup and, when work made a copy, sets the last-success gauge to
// its time.
func (r *Runner) run(ctx context.Context, work func(context.Context) (time.Time, error)) {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	j, _, err := r.d.Status.Begin(ctx, status.Job{Kind: status.Backup, Owner: status.SystemOwner}, cancel)
	if err != nil {
		r.d.Logger.Warn("backup: cannot record the backup job; it waits for the next tick", "error", err)
		return
	}
	r.d.Status.Update(j.ID, func(j *status.Job) { j.State = status.Running })
	r.d.Logger.Info("backup: run started", "job", string(j.ID))
	made, err := work(jobCtx)
	switch {
	case err == nil:
		r.failed = false
		r.d.Status.Reconcile(status.AlertBackup, false, "")
		if !made.IsZero() {
			r.d.Metrics.SetBackupLastSuccess(made)
		}
		r.d.Status.Update(j.ID, func(j *status.Job) { j.State, j.Outcome = status.Done, status.Succeeded })
		r.d.Logger.Info("backup: run completed", "job", string(j.ID))
	case jobCtx.Err() != nil:
		r.d.Status.Update(j.ID, func(j *status.Job) { j.State, j.Outcome = status.Done, status.Cancelled })
		r.d.Logger.Info("backup: run cancelled", "job", string(j.ID))
	default:
		r.failed, r.retryAt = true, time.Now().Add(retryAfter)
		r.d.Metrics.IncBackupFailure()
		r.d.Status.Reconcile(status.AlertBackup, true, err.Error())
		r.d.Status.Update(j.ID, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, status.Failed, err.Error() })
		r.d.Logger.Error("backup: run failed; it retries in 15 minutes", "job", string(j.ID), "error", err)
	}
}

// named is one backup file: its name and the time it holds.
type named struct {
	at   time.Time
	name string
}

func backupName(now time.Time) named {
	at := now.UTC().Truncate(time.Second)
	return named{at: at, name: at.Format(NameLayout)}
}

// list creates dir when absent and answers its backups not after now,
// oldest first, logging each later one at WARN once.
func (r *Runner) list(dir string, now time.Time) ([]named, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var past []named
	for _, e := range entries {
		at, ok := parseName(e.Name())
		switch {
		case !ok || !e.Type().IsRegular():
		case at.After(now):
			if !r.warned[e.Name()] {
				r.warned[e.Name()] = true
				r.d.Logger.Warn("backup: a backup is named after now, so the clock may have moved back; it is kept and not counted", "name", e.Name())
			}
		default:
			past = append(past, named{at: at, name: e.Name()})
		}
	}
	slices.SortFunc(past, func(a, b named) int { return a.at.Compare(b.at) })
	return past, nil
}

// parseName reads a NameLayout name, refusing any other spelling of the
// same time.
func parseName(name string) (time.Time, bool) {
	at, err := time.Parse(NameLayout, name)
	if err != nil || at.Format(NameLayout) != name {
		return time.Time{}, false
	}
	return at, true
}

// prune deletes the oldest of past, oldest first, beyond retention.
func prune(dir string, past []named, retention int) error {
	var errs []error
	for i := range len(past) - retention {
		if err := os.Remove(filepath.Join(dir, past[i].name)); err != nil {
			errs = append(errs, fmt.Errorf("backup: prune %s: %w", past[i].name, err))
		}
	}
	return errors.Join(errs...)
}
