package providercalls

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/provider"
)

// Binding is one engine's view of one provider under one credential.
type Binding struct {
	f          *Funnel
	p          *providerState
	credential string
	released   atomic.Bool
	digest     [32]byte
}

// Call admits the call, runs fn under the op's deadline and the caller's
// context, and observes fn's error once. A refusal, with fn never run, is
// ErrReleased, ErrCredentialDisabled or a *BackoffError. When ctx ended and
// fn failed, Call returns ctx's error and observes nothing; fn's failure
// after the op's own deadline expired under a live ctx is Transient. op is
// Search or Fetch: a Test is Probe's.
func (b *Binding) Call(ctx context.Context, op Op, c Class, fn func(context.Context) error) error {
	if (op != Search && op != Fetch) || (c != Automatic && c != Interactive) {
		return errBadCall
	}
	err := b.run(ctx, op, c, fn)
	b.f.metrics.count(b.p.id, op, err)
	return err
}

func (b *Binding) run(ctx context.Context, op Op, c Class, fn func(context.Context) error) error {
	if err := b.admit(ctx, op, c); err != nil {
		return err
	}
	if c == Automatic {
		if err := b.p.pace(ctx); err != nil {
			return err
		}
	}
	if err := b.p.slots.acquire(ctx, c); err != nil {
		return err
	}
	defer b.p.slots.release()
	// The waits may have outlived a pause set or a release made meanwhile.
	if err := b.admit(ctx, op, c); err != nil {
		return err
	}
	err := b.f.runOp(ctx, b.p.id, op, fn)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	b.observe(ctx, op, err)
	return err
}

// runOp runs fn under op's deadline, making its failure past that deadline,
// under a live ctx, a Transient one.
func (f *Funnel) runOp(ctx context.Context, id provider.ProviderID, op Op, fn func(context.Context) error) error {
	opCtx, cancel := context.WithTimeout(ctx, op.deadline())
	defer cancel()
	start := time.Now()
	err := fn(opCtx)
	f.metrics.seconds.Observe(time.Since(start).Seconds(), string(id), op.String())
	if err == nil || ctx.Err() != nil || !errors.Is(opCtx.Err(), context.DeadlineExceeded) {
		return err
	}
	if fail := (*provider.Failure)(nil); errors.As(err, &fail) && fail.Kind == provider.Transient {
		return err
	}
	return &provider.Failure{Kind: provider.Transient, Cause: httpclient.CauseTimeout, Msg: op.String() + " passed its " + op.deadline().String() + " deadline"}
}

// admit is the admit rows, the first-call row included.
func (b *Binding) admit(ctx context.Context, op Op, c Class) error {
	if b.released.Load() {
		return ErrReleased
	}
	p := b.p
	p.mu.Lock()
	defer p.mu.Unlock()
	cred := p.credential(b.digest)
	if !cred.checked && b.credential != "" {
		if err := b.f.firstCall(ctx, p, cred, b.credential); err != nil {
			return err
		}
	}
	if cred.disabled {
		return ErrCredentialDisabled
	}
	now := time.Now()
	until := cred.authUntil
	if p.paused[op].After(until) {
		until = p.paused[op]
	}
	if c == Automatic && p.coolUntil.After(until) {
		until = p.coolUntil
	}
	if until.After(now) {
		return &BackoffError{Provider: p.id, Until: until}
	}
	return nil
}

// observe turns fn's error into at most one transition.
func (b *Binding) observe(ctx context.Context, op Op, err error) {
	p := b.p
	p.mu.Lock()
	defer p.mu.Unlock()
	released := b.released.Load()
	fail := (*provider.Failure)(nil)
	switch {
	case err == nil:
		if !released {
			b.f.success(p, b)
		}
	case !errors.As(err, &fail):
		// A *provider.Skipped sent nothing, and any other error is the
		// caller's own: neither is a fact about the provider.
	case fail.Kind == provider.RateLimited:
		b.f.ratePause(p, op, fail)
	case released:
	case fail.Kind == provider.Auth:
		if b.credential != "" {
			b.f.authFailure(ctx, p, b, fail)
		}
	case fail.Kind == provider.Transient:
		b.f.transient(p, fail)
	}
}

// Release refuses every later Call with ErrReleased and ignores what calls
// still running observe, but their RateLimited answers. It never blocks.
func (b *Binding) Release() { b.released.Store(true) }
