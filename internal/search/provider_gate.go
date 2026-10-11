package search

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// ProviderStatus reports every provider the health tracker or the provider
// gate has something to say about. timeoutsEnabled is false when the
// configuration disables provider timeouts; the gate's state is reported
// either way.
func (e *Engine) ProviderStatus() (status map[subflux.ProviderID]subflux.ProviderStatus, timeoutsEnabled bool) {
	health := e.timeout.Status()
	gate := e.providerGate.Status()
	status = make(map[subflux.ProviderID]subflux.ProviderStatus, len(health)+len(gate))
	for id, h := range health {
		status[id] = mergeProviderStatus(h, gate[id])
	}
	for id, g := range gate {
		if _, done := status[id]; !done {
			status[id] = mergeProviderStatus(subflux.ProviderStatus{}, g)
		}
	}
	return status, health != nil
}

// mergeProviderStatus is one provider's full status: the health fields from
// the tracker, the disable, pause and rejected settings from the gate.
func mergeProviderStatus(health subflux.ProviderStatus, gate providergate.Status) subflux.ProviderStatus { //nolint:gocritic // hugeParam: health is the copy the merge returns
	health.Disabled = gate.Disabled
	health.DisabledReason = gate.DisabledReason
	health.AuthFailures = gate.AuthFailures
	health.PausedFor = gate.PausedFor
	health.RejectedSettings = slices.Clone(gate.RejectedSettings)
	return health
}

// ResetProviderState clears every provider timeout, credential disable,
// rate-limit pause and rejected setting.
func (e *Engine) ResetProviderState(ctx context.Context) {
	e.timeout.Reset()
	e.providerGate.ResetAll(ctx)
}

// SweepNotice reports a provider that contributed no results to a sweep:
// either the gate refused it (Gated, with the gate's Reason) or its search
// failed (Err).
type SweepNotice struct {
	Err      error
	Provider subflux.ProviderID
	Reason   string
	Gated    bool
}

// SweepProviders queries every provider for req, each under its own timeout,
// with the provider gate applied and the health tracker and adaptive backoff
// bypassed. Results come back in provider order.
func (e *Engine) SweepProviders(ctx context.Context, req *subflux.SearchRequest,
	perProviderTimeout time.Duration,
) ([]subflux.Subtitle, []SweepNotice) {
	perProvider := make([][]subflux.Subtitle, len(e.providers))
	var (
		mu      sync.Mutex
		notices []SweepNotice
	)
	note := func(n SweepNotice) {
		mu.Lock()
		notices = append(notices, n)
		mu.Unlock()
	}
	var g errgroup.Group
	for i, p := range e.providers {
		if ok, reason := e.admit(p, providergate.OpSearch); !ok {
			note(SweepNotice{Provider: p.Name(), Gated: true, Reason: reason})
			continue
		}
		g.Go(func() error {
			pctx, cancel := context.WithTimeout(ctx, perProviderTimeout)
			defer cancel()
			subs, err := p.Search(pctx, req)
			e.observe(ctx, p, providergate.OpSearch, err)
			if err != nil {
				note(SweepNotice{Provider: p.Name(), Err: err})
				return nil
			}
			perProvider[i] = subs
			return nil
		})
	}
	_ = g.Wait()
	slices.SortFunc(notices, func(a, b SweepNotice) int { return cmp.Compare(a.Provider, b.Provider) })
	return slices.Concat(perProvider...), notices
}

// Download fetches sub from its provider through the provider gate; a
// refusal is ErrProviderGated and makes no request.
func (e *Engine) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	return e.downloadFromProvider(ctx, sub)
}

// HasShowCounter reports whether one of the engine's providers offers the
// show-level subtitle count.
func (e *Engine) HasShowCounter() bool { return e.showCounter != nil }

// CountShowSubtitles asks the engine's show-level counter, through the
// provider gate as a search. A refusal is ErrProviderGated with the gate's
// reason and makes no request; with no counter it is errProviderNotFound.
func (e *Engine) CountShowSubtitles(ctx context.Context, q subflux.ShowSubtitleQuery) (int, error) {
	if e.showCounter == nil {
		return 0, fmt.Errorf("%w: no show-level counter", errProviderNotFound)
	}
	name := e.showCounterID
	if ok, reason := e.providerGate.Admit(name, providergate.OpSearch); !ok {
		return 0, fmt.Errorf("%w: %s: %s", ErrProviderGated, name, reason)
	}
	start := time.Now()
	n, err := e.showCounter.CountShowSubtitles(ctx, q)
	if e.metrics != nil {
		e.metrics.RecordSearch(name, time.Since(start), err)
	}
	e.providerGate.Observe(context.WithoutCancel(ctx), name, providergate.OpSearch, err)
	return n, err
}
