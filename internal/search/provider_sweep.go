package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// --- Provider sweep ---

// searchProvider queries one admitted provider for the automated sweep and
// reports the outcome to the provider gate and the health tracker. A refused
// credential or a rate limit is the gate's alone, so one fault never trips
// both.
func (e *Engine) searchProvider(ctx context.Context, p provider.Provider,
	req *subflux.SearchRequest,
) ([]subflux.Subtitle, subflux.ProviderID, error) {
	start := time.Now()
	subs, err := p.Search(ctx, req)
	dur := time.Since(start)

	name := p.Name()

	if e.metrics != nil {
		e.metrics.RecordSearch(name, dur, err)
	}
	e.observe(ctx, p, providergate.OpSearch, err)

	if err != nil {
		slog.Warn("provider search failed",
			"provider", name,
			"duration_ms", dur.Milliseconds(),
			"error", err)
		if !gateOwned(err) {
			e.timeout.RecordFailure(name, err)
		}
		return nil, name, err
	}

	e.timeout.RecordSuccess(name)

	slog.Debug("provider search complete",
		"provider", name,
		"results", len(subs),
		"duration_ms", dur.Milliseconds())

	return subs, name, nil
}

// admit asks the gate whether p may be called for op now and, when it may,
// brings p's answer about its optional setting in line with the gate before
// the call.
func (e *Engine) admit(p provider.Provider, op providergate.Op) (ok bool, reason string) {
	if ok, reason = e.providerGate.Admit(p.Name(), op); ok {
		if sr, isReporter := p.(provider.SettingReporter); isReporter {
			e.providerGate.PrepareSetting(p.Name(), sr)
		}
	}
	return ok, reason
}

// observe reports one provider call to the gate, then what the provider says
// the upstream answered about its optional setting. The write outlives the
// caller's cancellation, so a recorded failure is never lost to a hang-up.
func (e *Engine) observe(ctx context.Context, p provider.Provider, op providergate.Op, err error) {
	e.providerGate.Observe(context.WithoutCancel(ctx), p.Name(), op, err)
	if sr, ok := p.(provider.SettingReporter); ok {
		e.providerGate.ObserveSetting(p.Name(), sr)
	}
}

// gateOwned reports whether err is a refused credential or a rate limit, the
// two answers the provider gate owns.
func gateOwned(err error) bool {
	if _, ok := errors.AsType[*subflux.AuthError](err); ok {
		return true
	}
	_, ok := errors.AsType[*subflux.RateLimitError](err)
	return ok
}

// searchProvidersFiltered coalesces concurrent calls with identical request
// inputs via singleflight, so a poller event and a manual scan on the same
// media don't issue duplicate provider HTTP requests. The key includes
// VideoPath/VideoHash so requests differing only by file artifact get
// separate calls.
func (e *Engine) searchProvidersFiltered(ctx context.Context,
	req *subflux.SearchRequest, providers []provider.Provider,
) searchOutcome {
	key := buildSearchKey(req, providers)
	// Use a detached context for the shared work so that a single caller's
	// cancellation doesn't abort the flight for all waiters.
	v, err, _ := e.searchGroup.Do(key, func() (any, error) {
		detached := context.WithoutCancel(ctx)
		return e.searchProvidersFilteredInner(detached, req, providers), nil
	})
	if err != nil {
		return searchOutcome{}
	}
	if ctx.Err() != nil {
		return searchOutcome{}
	}
	out, ok := v.(searchOutcome)
	if !ok {
		return searchOutcome{}
	}
	return out
}

// buildSearchKey is assembled with keyenc rather than by concatenating with a
// separator, because three of its six fields can contain one. A collision here
// is not a cache miss: two different requests share one flight, and the second
// caller receives the FIRST request's subtitle results for its own video. The
// naive form was forgeable through the middle of the key, where BuildMediaID,
// the language list and the video path sit adjacent — a configured language
// code carrying the separator, or a malformed media id, shifts the split and
// makes two distinct sweeps look identical. The language and provider lists
// nest by composition (an inner keyenc value escaped again as one outer
// component) so a separator inside one language code cannot be read as a field
// boundary either.
func buildSearchKey(req *subflux.SearchRequest, providers []provider.Provider) string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = string(p.Name())
	}
	slices.Sort(names)

	// Languages may arrive in any order from upstream callers; sort for stability.
	langs := append([]string(nil), req.Languages...)
	slices.Sort(langs)

	return keyenc.Join(
		string(req.MediaType),
		mediaid.Build(req),
		keyenc.Join(langs...),
		req.VideoPath,
		req.VideoHash,
		keyenc.Join(names...),
	)
}

func (e *Engine) searchProvidersFilteredInner(ctx context.Context,
	req *subflux.SearchRequest, providers []provider.Provider,
) searchOutcome {
	var (
		mu          sync.Mutex
		results     = make([]subflux.Subtitle, 0, len(providers)*16)
		provResults = make([]providerResult, 0, len(providers))
	)

	collect := func(subs []subflux.Subtitle, name subflux.ProviderID, err error) {
		outcome := providerSuccess
		if err != nil {
			outcome = providerError
		}
		slog.Debug("provider search completed", "provider", name, "outcome", outcome.String(), "results", len(subs))
		mu.Lock()
		provResults = append(provResults, providerResult{name: name, err: err, outcome: outcome})
		if err == nil {
			results = append(results, subs...)
		}
		mu.Unlock()
	}

	g := new(errgroup.Group)
	g.SetLimit(e.providerConcurrency())

	skip := func(name subflux.ProviderID, outcome providerOutcome) {
		mu.Lock()
		provResults = append(provResults, providerResult{name: name, outcome: outcome})
		mu.Unlock()
	}
	for _, p := range providers {
		// Go blocks until a slot is free, and an admission claims a probe
		// slot that expires, so both checks run only once the call can start.
		g.Go(func() error {
			if e.timeout.IsTimedOut(p.Name()) {
				skip(p.Name(), providerTimeout)
				return nil
			}
			if ok, reason := e.admit(p, providergate.OpSearch); !ok {
				slog.Debug("provider gated, skipping",
					"provider", p.Name(), "reason", reason, "media", logsafe.Field(req.MediaLabel()))
				skip(p.Name(), providerGated)
				return nil
			}
			subs, name, err := e.searchProvider(ctx, p, req)
			collect(subs, name, err)
			return nil
		})
	}

	_ = g.Wait()

	if to := (searchOutcome{providers: provResults}).timedOut(); len(to) > 0 {
		slog.Debug("providers timed out, skipping",
			"providers", to, "media", logsafe.Field(req.MediaLabel()), "lang", req.Languages)
	}

	return searchOutcome{
		results:   results,
		providers: provResults,
	}
}

// downloadFromProvider fetches one subtitle through the provider gate. A
// refusal is ErrProviderGated carrying the gate's reason, and no request is
// made.
func (e *Engine) downloadFromProvider(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	p, ok := e.providersByName[sub.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, sub.Provider)
	}
	if ok, reason := e.admit(p, providergate.OpDownload); !ok {
		return nil, fmt.Errorf("%w: %s: %s", ErrProviderGated, p.Name(), reason)
	}
	start := time.Now()
	data, err := p.Download(ctx, sub)
	if e.metrics != nil {
		e.metrics.RecordDownload(p.Name(), err)
	}
	e.observe(ctx, p, providergate.OpDownload, err)
	if err != nil {
		return nil, err
	}
	slog.Debug("provider download complete",
		"provider", p.Name(),
		"bytes", len(data),
		"duration_ms", time.Since(start).Milliseconds())
	return data, nil
}

func (e *Engine) unionProviders(states []targetState) []provider.Provider {
	names := make(map[subflux.ProviderID]bool)
	for i := range states {
		if !states[i].needsSearch {
			continue
		}
		for n := range states[i].allowedProvs {
			names[n] = true
		}
	}
	var out []provider.Provider
	for _, p := range e.providers {
		if names[p.Name()] {
			out = append(out, p)
		}
	}
	return out
}

func filterByTargetProviders(subs []subflux.Subtitle, allowed map[subflux.ProviderID]struct{}) []subflux.Subtitle {
	var out []subflux.Subtitle
	for i := range subs {
		if _, ok := allowed[subs[i].Provider]; ok {
			out = append(out, subs[i])
		}
	}
	return out
}

func (e *Engine) providerConcurrency() int {
	maxConc := e.cfg.Search().MaxProviderConcurrency
	if maxConc <= 0 {
		return subflux.DefaultProviderConcurrency
	}
	return maxConc
}
