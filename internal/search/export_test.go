package search

import (
	"context"
	"errors"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// The engine's bounds and causes, for the external tests.
const (
	RunsInFlight         = runsInFlight
	ItemRunsInFlight     = itemRunsInFlight
	StoreRetryMax        = storeRetryMax
	NoResultInitial      = noResultInitial
	MaxCandidates        = maxCandidates
	HandleTTL            = handleTTL
	MaxHandles           = maxHandles
	MaxHandlesPerOwner   = maxHandlesPerOwner
	CauseSettingsChanged = causeSettingsChanged
)

// Picked is one wanted pair as a run ranks it.
type Picked struct {
	Target  language.Tag
	Ranked  []Ranked
	Variant language.Variant
	Skip    WhyNot
	// Upgrade: the pair is held by a file the run would replace.
	Upgrade bool
}

// Ranked is one usable candidate, in rank order.
type Ranked struct {
	Provider provider.ProviderID
	Release  string
	Facts    rank.Facts
	Fit      rank.Fit
}

// PickFor runs w up to the pick; nil when the run plans nothing.
func PickFor(ctx context.Context, e *Engine, w statedb.Work) ([]Picked, error) {
	r, picks, err := e.toPick(ctx, w)
	switch {
	case errors.Is(err, errInstanceGone):
		return nil, nil
	case err != nil || r == nil:
		return nil, err
	}
	out := make([]Picked, 0, len(picks))
	for _, p := range picks {
		got := Picked{Target: p.want.target, Variant: p.want.variant, Skip: p.skip, Upgrade: p.want.cur != nil}
		for _, j := range p.ranked {
			got.Ranked = append(got.Ranked, Ranked{Provider: j.from.entry.ID, Release: j.cand.Release, Facts: j.facts, Fit: j.fit})
		}
		out = append(out, got)
	}
	return out, nil
}

// RunFor runs w through its attempts and deliveries.
func RunFor(ctx context.Context, e *Engine, w statedb.Work) error {
	_, err := e.work(ctx, w)
	return err
}

// RunFunc is one row's run.
type RunFunc = func(ctx context.Context, w statedb.Work) error

// WrapRun has Drain run each row through wrap of e's own run.
func WrapRun(e *Engine, wrap func(RunFunc) RunFunc) {
	inner := e.run
	e.run = func(ctx context.Context, w statedb.Work) (runReport, error) {
		var rep runReport
		err := wrap(func(ctx context.Context, w statedb.Work) error {
			var err error
			rep, err = inner(ctx, w)
			return err
		})(ctx, w)
		return rep, err
	}
}

// NextFunc is the drainer's read of the work queue.
type NextFunc = func(ctx context.Context, limit int) ([]statedb.Work, error)

// WrapNext has Drain read the queue through wrap of e's own read.
func WrapNext(e *Engine, wrap func(NextFunc) NextFunc) { e.next = wrap(e.next) }

// BeginFunc is the status store's Begin.
type BeginFunc = func(ctx context.Context, job status.Job, stop func()) (status.Job, bool, error)

// WrapBegin has e begin its jobs through wrap of the status store's Begin.
func WrapBegin(e *Engine, wrap func(BeginFunc) BeginFunc) { e.begin = wrap(e.begin) }

// DeliverFunc is the writer's Deliver.
type DeliverFunc = func(ctx context.Context, d mediawrite.Delivery) (statedb.FileRecord, error)

// WrapDeliver has e deliver through wrap of the writer's Deliver.
func WrapDeliver(e *Engine, wrap func(DeliverFunc) DeliverFunc) { e.deliverTo = wrap(e.deliverTo) }

// FillHandles has e hold placeholder handles until MaxHandles are held,
// MaxHandlesPerOwner per owner from owner 1000 on.
func FillHandles(e *Engine) {
	for o := int64(1000); ; o++ {
		e.handles.mu.Lock()
		free := MaxHandles - len(e.handles.byHandle)
		e.handles.mu.Unlock()
		if free == 0 {
			return
		}
		hs := make([]*held, min(free, MaxHandlesPerOwner))
		for i := range hs {
			hs[i] = &held{}
		}
		if _, err := e.handles.insert(o, hs, time.Now()); err != nil {
			panic(err)
		}
	}
}

// BeginDrain sets e's draining flag alone, as Drain's stop does first,
// before it drops the handles and waits.
func BeginDrain(e *Engine) {
	e.mu.Lock()
	e.draining = true
	e.mu.Unlock()
}
