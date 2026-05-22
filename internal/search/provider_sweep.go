package search

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"subflux/internal/api"
)

// --- Provider sweep ---

// searchProvider executes a single provider search and classifies the outcome.
// If trackTimeout is true, failures/successes are recorded in the timeout tracker.
func (e *Engine) searchProvider(ctx context.Context, p api.Provider,
	req *api.SearchRequest, trackTimeout bool) ([]api.Subtitle, api.ProviderID, error) {

	start := time.Now()
	subs, err := p.Search(ctx, req)
	dur := time.Since(start)

	name := p.Name()

	if e.metrics != nil {
		e.metrics.RecordSearch(name, dur, err)
	}

	if err != nil {
		slog.Warn("provider search failed",
			"provider", name,
			"duration_ms", dur.Milliseconds(),
			"error", err)
		if trackTimeout {
			e.timeout.RecordFailure(name, err)
		}
		return nil, name, err
	}

	if trackTimeout {
		e.timeout.RecordSuccess(name)
	}

	slog.Debug("provider search complete",
		"provider", name,
		"results", len(subs),
		"duration_ms", dur.Milliseconds())

	return subs, name, nil
}

// searchProvidersFiltered searches all eligible providers concurrently.
// Embedded runs outside the main pool (local ffprobe, no rate limits).
// Network providers in timeout are skipped.
// Returns results and per-provider success/error lists.
//
// Concurrent calls with identical request inputs (media id, language list,
// video path/hash, provider set) are coalesced via singleflight so that a
// poller event and a manual scan firing on the same media don't issue
// duplicate provider HTTP requests. The key includes VideoPath/VideoHash
// so requests differing only by file artifact get separate calls.
func (e *Engine) searchProvidersFiltered(ctx context.Context,
	req *api.SearchRequest, providers []api.Provider) searchOutcome {
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
	// Check the caller's own context after the flight completes.
	if ctx.Err() != nil {
		return searchOutcome{}
	}
	out, ok := v.(searchOutcome)
	if !ok {
		return searchOutcome{}
	}
	return out
}

// buildSearchKey produces a stable singleflight key from request inputs.
// Two callers with the same key share a single underlying provider sweep.
func buildSearchKey(req *api.SearchRequest, providers []api.Provider) string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = string(p.Name())
	}
	sort.Strings(names)

	// Languages may arrive in any order from upstream callers; sort for stability.
	langs := append([]string(nil), req.Languages...)
	sort.Strings(langs)

	var b strings.Builder
	b.WriteString(string(req.MediaType))
	b.WriteByte('|')
	b.WriteString(api.BuildMediaID(req))
	b.WriteByte('|')
	b.WriteString(strings.Join(langs, ","))
	b.WriteByte('|')
	b.WriteString(req.VideoPath)
	b.WriteByte('|')
	b.WriteString(req.VideoHash)
	b.WriteByte('|')
	b.WriteString(strings.Join(names, ","))
	return b.String()
}

// searchProvidersFilteredInner does the actual provider sweep — wrapped by
// searchProvidersFiltered to provide singleflight deduplication.
func (e *Engine) searchProvidersFilteredInner(ctx context.Context,
	req *api.SearchRequest, providers []api.Provider) searchOutcome {
	var (
		mu          sync.Mutex
		results     = make([]api.Subtitle, 0, len(providers)*16)
		provResults = make([]providerResult, 0, len(providers))
	)

	collect := func(subs []api.Subtitle, name api.ProviderID, err error) {
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

	for _, p := range providers {
		// Embedded is local (ffprobe); skip timeout check but still use errgroup.
		if p.Name() == api.ProviderNameEmbedded {
			g.Go(func() error {
				subs, name, err := e.searchProvider(ctx, p, req, false)
				collect(subs, name, err)
				return nil
			})
			continue
		}

		if e.timeout.IsTimedOut(p.Name()) {
			mu.Lock()
			provResults = append(provResults, providerResult{
				name: p.Name(), outcome: providerTimeout,
			})
			mu.Unlock()
			continue
		}

		g.Go(func() error {
			subs, name, err := e.searchProvider(ctx, p, req, true)
			collect(subs, name, err)
			return nil
		})
	}

	_ = g.Wait() //nolint:errcheck // goroutines always return nil

	// Log timed-out providers.
	if to := (searchOutcome{providers: provResults}).timedOut(); len(to) > 0 {
		slog.Debug("providers timed out, skipping",
			"providers", to, "media", req.MediaLabel(), "lang", req.Languages)
	}

	return searchOutcome{
		results:   results,
		providers: provResults,
	}
}

// downloadFromProvider finds the named provider and downloads the subtitle.
func (e *Engine) downloadFromProvider(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	p, ok := e.providersByName[sub.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, sub.Provider)
	}
	start := time.Now()
	data, err := p.Download(ctx, sub)
	if e.metrics != nil {
		e.metrics.RecordDownload(p.Name(), err)
	}
	if err != nil {
		return nil, err
	}
	slog.Debug("provider download complete",
		"provider", p.Name(),
		"bytes", len(data),
		"duration_ms", time.Since(start).Milliseconds())
	return data, nil
}

// unionProviders returns the union of providers across targets that need searching.
func (e *Engine) unionProviders(states []targetState) []api.Provider {
	names := make(map[api.ProviderID]bool)
	for i := range states {
		if !states[i].needsSearch {
			continue
		}
		for n := range states[i].allowedProvs {
			names[n] = true
		}
	}
	var out []api.Provider
	for _, p := range e.providers {
		if names[p.Name()] {
			out = append(out, p)
		}
	}
	return out
}

// filterByTargetProviders keeps only subtitles from providers allowed for the target.
func (e *Engine) filterByTargetProviders(subs []api.Subtitle, allowed map[api.ProviderID]struct{}) []api.Subtitle {
	var out []api.Subtitle
	for i := range subs {
		if _, ok := allowed[subs[i].Provider]; ok {
			out = append(out, subs[i])
		}
	}
	return out
}

// providerConcurrency returns the configured maximum provider concurrency,
// falling back to api.DefaultProviderConcurrency when unconfigured.
func (e *Engine) providerConcurrency() int {
	maxConc := e.cfg.Search().MaxProviderConcurrency
	if maxConc <= 0 {
		return api.DefaultProviderConcurrency
	}
	return maxConc
}
