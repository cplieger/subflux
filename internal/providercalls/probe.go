package providercalls

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/provider"
)

// Probe runs fn, a connection test of credential, Interactive in the
// provider's slots under the Test deadline. It is no binding: a disabled
// credential or a cooldown never refuses it, and it feeds no ladder, health
// or live pause; a live Search or Test pause refuses it with a
// *BackoffError, and its RateLimited answer pauses Test only. A pass resets
// the credential's ladder; when the stored record is credential's it is
// deleted first (cleared true), admitting live bindings of it at once. A
// failed fn, record read, delete or Verify returns its error.
func (f *Funnel) Probe(ctx context.Context, id provider.ProviderID, credential string, fn func(context.Context) error) (cleared bool, err error) {
	p := f.provider(id)
	defer func() { f.metrics.count(id, Test, err) }()
	if err := p.admitProbe(); err != nil {
		return false, err
	}
	if err := p.slots.acquire(ctx, Interactive); err != nil {
		return false, err
	}
	defer p.slots.release()
	if err := p.admitProbe(); err != nil {
		return false, err
	}
	if err := f.runOp(ctx, id, Test, fn); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if fail := (*provider.Failure)(nil); errors.As(err, &fail) && fail.Kind == provider.RateLimited {
			p.mu.Lock()
			f.ratePause(p, Test, fail)
			p.mu.Unlock()
		}
		return false, err
	}
	return f.probePassed(ctx, p, credential)
}

func (p *providerState) admitProbe() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	until := p.paused[Search]
	if p.paused[Test].After(until) {
		until = p.paused[Test]
	}
	if until.After(time.Now()) {
		return &BackoffError{Provider: p.id, Until: until}
	}
	return nil
}

func (f *Funnel) probePassed(ctx context.Context, p *providerState, credential string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, found, err := f.db.CredentialDisable(ctx, string(p.id))
	if err != nil {
		return false, err
	}
	cleared := false
	if found {
		match, err := f.hasher.Verify(ctx, rec.Fingerprint, credential)
		if err != nil {
			return false, err
		}
		if match {
			if err := f.db.DeleteCredentialDisable(ctx, string(p.id)); err != nil {
				slog.Error("provider credential record delete failed", "provider", p.id, "err", err)
				return false, err
			}
			p.setDisabled(nil, "")
			f.clear(p)
			cleared = true
		}
	}
	if c := p.lookup(sha256.Sum256([]byte(credential))); c != nil {
		c.failures, c.authUntil = 0, time.Time{}
	}
	f.publish(p)
	return cleared, nil
}
