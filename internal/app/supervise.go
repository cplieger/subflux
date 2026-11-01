package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// component is one long-running part of the process, named for the cause it
// becomes when it returns. A producer's stop, when set, ends what its
// context's cancellation does not (HTTP shutdown); the drain calls it.
type component struct {
	run  func(ctx context.Context) error
	stop func(ctx context.Context) error
	name string
}

// supervision is what supervise runs and drains. closeStore may be nil.
type supervision struct {
	beginDrain       func()
	shutdownSettings func(ctx context.Context) error
	closeStore       func() error
	producers, sinks []component
	drainBudget      time.Duration
	httpGrace        time.Duration
}

type exit struct {
	err  error
	name string
	live bool
}

// supervise returns at the first of: ctx ends (nil), or a component returns
// while its phase is live, the cause even when nil ("<name> exited"). Each
// cause runs one drain: beginDrain; within httpGrace the producers cancelled,
// stopped and awaited, then the settings shut down; then the sinks
// cancelled and awaited until drainBudget. An overrun or a drain-time error
// joins the cause and leaves the store open for a request or job still on it.
func supervise(ctx context.Context, sv *supervision) error {
	// Only the drain stops a component, so beginDrain precedes every stop.
	prodCtx, cancelProducers := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelProducers()
	sinkCtx, cancelSinks := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSinks()
	exits := make(chan exit, len(sv.producers)+len(sv.sinks))
	producers := start(prodCtx, sv.producers, exits)
	sinks := start(sinkCtx, sv.sinks, exits)

	var cause error
	select {
	case <-ctx.Done():
	case e := <-exits:
		cause = e.err
		slog.Error("app: a component stopped; shutting down", "component", e.name, "error", e.err)
	}

	deadline := time.Now().Add(sv.drainBudget)
	sv.beginDrain()
	cancelProducers()
	drainErrs := sv.phaseOne(producers)
	cancelSinks()
	phase2, cancel2 := context.WithDeadline(context.Background(), deadline)
	defer cancel2()
	if !await(phase2, sinks) {
		drainErrs = append(drainErrs, errors.New("app: sinks overran the drain budget"))
	}
	for len(exits) > 0 {
		drainErrs = appendDrainErr(drainErrs, <-exits)
	}
	if len(drainErrs) > 0 {
		err := errors.Join(drainErrs...)
		slog.Error("app: the drain did not finish cleanly; leaving the store open", "error", err)
		return errors.Join(cause, err)
	}
	if sv.closeStore != nil {
		if err := sv.closeStore(); err != nil {
			return errors.Join(cause, fmt.Errorf("app: close store: %w", err))
		}
	}
	return cause
}

func (sv *supervision) phaseOne(producers <-chan struct{}) []error {
	ctx, cancel := context.WithTimeout(context.Background(), sv.httpGrace)
	defer cancel()
	var errs []error
	for _, c := range sv.producers {
		if c.stop == nil {
			continue
		}
		if err := c.stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
		}
	}
	if !await(ctx, producers) {
		errs = append(errs, errors.New("app: producers overran the HTTP grace"))
	}
	if err := sv.shutdownSettings(ctx); err != nil {
		errs = append(errs, fmt.Errorf("app: a settings save overran the HTTP grace: %w", err))
	}
	return errs
}

// appendDrainErr keeps the error of a component that returned after the
// first cause; a return at its context's cancellation is a clean stop.
func appendDrainErr(errs []error, e exit) []error {
	if e.err == nil || (!e.live && errors.Is(e.err, context.Canceled)) {
		return errs
	}
	return append(errs, e.err)
}

// start runs each component on ctx, reporting its return on exits, and
// returns a channel closed once all have returned.
func start(ctx context.Context, cs []component, exits chan<- exit) <-chan struct{} {
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Go(func() {
			err := c.run(ctx)
			live := ctx.Err() == nil
			switch {
			case err == nil && live:
				err = fmt.Errorf("%s exited", c.name)
			case err != nil:
				err = fmt.Errorf("%s: %w", c.name, err)
			}
			exits <- exit{name: c.name, err: err, live: live}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func await(ctx context.Context, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-ctx.Done():
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}
