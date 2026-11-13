package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
)

// Test checks cfg's credential for e's provider through f.Probe, never
// touching a live engine: ok with a message for the user, err only for
// subflux's own failure, such as a credential record or Verify error.
func Test(ctx context.Context, f *providercalls.Funnel, e provider.Entry, cfg provider.Config) (ok bool, message string, err error) { //nolint:gocritic // hugeParam: an Entry is the registry's value
	a, err := provider.Build(e, cfg)
	if err != nil {
		return false, "", fmt.Errorf("search: build %s: %w", e.ID, err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		closeAdapter(cctx, slog.Default(), &e, a)
	}()
	tester, has := a.(provider.Tester)
	if !has {
		return true, "no credentials to check", nil
	}
	cleared, err := f.Probe(ctx, e.ID, credential(&e, &cfg), tester.Test)
	var (
		backoff *providercalls.BackoffError
		fail    *provider.Failure
	)
	switch {
	case err == nil && cleared:
		return true, "re-enabled", nil
	case err == nil:
		return true, "connected", nil
	case errors.As(err, &backoff):
		return false, "rate limited until " + backoff.Until.UTC().Format(time.RFC3339), nil
	case errors.As(err, &fail) && fail.Kind == provider.Auth:
		return false, "the credentials were rejected", nil
	case errors.As(err, &fail):
		return false, "could not reach the service: " + fail.Msg, nil
	}
	return false, "", fmt.Errorf("search: test %s: %w", e.ID, err)
}
