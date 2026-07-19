package search

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Target state building ---

// Compile-time assertion: providerOutcome implements fmt.Stringer.
var _ fmt.Stringer = providerOutcome(0)

// providerOutcome classifies the result of a single provider search.
type providerOutcome int

const (
	providerSuccess providerOutcome = iota
	providerError
	providerTimeout
)

// String implements fmt.Stringer for debuggable logging.
func (o providerOutcome) String() string {
	switch o {
	case providerSuccess:
		return "success"
	case providerError:
		return "error"
	case providerTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// providerResult records the outcome of a single provider search.
type providerResult struct {
	err     error
	name    api.ProviderID
	outcome providerOutcome
}

// searchOutcome aggregates results from all providers for a single language query.
type searchOutcome struct {
	results   []api.Subtitle
	providers []providerResult
}

// attempted returns the number of providers the sweep actually queried
// (success or error). Providers skipped by the health timeout never issued
// a request, so they don't count: inter-item scan pacing keys on this via
// api.LangOutcome.Queried.
func (o *searchOutcome) attempted() int {
	n := 0
	for _, p := range o.providers {
		if p.outcome != providerTimeout {
			n++
		}
	}
	return n
}

// succeeded returns the names of providers that returned results without error.
func (o *searchOutcome) succeeded() []api.ProviderID {
	var names []api.ProviderID
	for _, p := range o.providers {
		if p.outcome == providerSuccess {
			names = append(names, p.name)
		}
	}
	return names
}

// timedOut returns the names of providers that timed out.
func (o searchOutcome) timedOut() []api.ProviderID {
	var names []api.ProviderID
	for _, p := range o.providers {
		if p.outcome == providerTimeout {
			names = append(names, p.name)
		}
	}
	return names
}

// errored returns the names of providers that returned errors.
func (o searchOutcome) errored() []api.ProviderID {
	var names []api.ProviderID
	for _, p := range o.providers {
		if p.outcome == providerError {
			names = append(names, p.name)
		}
	}
	return names
}

// targetState holds the computed search state for a single subtitle target.
type targetState struct {
	target       *api.SubtitleTarget
	allowedProvs map[api.ProviderID]struct{}
	variant      api.Variant
	currentScore int
	needsSearch  bool
	isUpgrade    bool
}

// targetLocked checks the per-variant manual lock for one target. Locks live
// on the (media_type, media_id, language, variant) quad, so a manual forced
// download blocks only the forced target while standard/hi automation
// continues. A store error fails CLOSED (treated as locked and skipped), the
// same conservative stance the store itself takes.
func (e *Engine) targetLocked(ctx context.Context, mediaType api.MediaType, mediaID, title, lang string, variant api.Variant) bool {
	locked, err := e.store.IsManuallyLocked(ctx, mediaType, mediaID, lang, variant)
	if err != nil {
		slog.Warn("IsManuallyLocked failed, skipping target",
			"media", title, "lang", lang, "variant", variant, "error", err)
		return true
	}
	if locked {
		slog.Debug("manually locked, skipping target",
			"media", title, "lang", lang, "variant", variant)
	}
	return locked
}

// buildTargetStates computes the search state for each target in a language group.
// Returns the states and whether any target needs searching.
func (e *Engine) buildTargetStates(ctx context.Context, req *api.SearchRequest,
	targets []api.SubtitleTarget, existing *existingSubs,
	searchCfg *api.SearchConfig, mediaType api.MediaType, mediaID, lang, label string,
	upgradeCutoff time.Time,
) ([]targetState, bool) {
	states := make([]targetState, len(targets))
	anyNeedsSearch := false

	for i := range targets {
		t := &targets[i]
		states[i].target = t
		states[i].variant = t.Variant

		// Build allowed providers for this target.
		provs := e.filterProviders(t)
		allowed := make(map[api.ProviderID]struct{}, len(provs))
		for _, p := range provs {
			allowed[p.Name()] = struct{}{}
		}
		states[i].allowedProvs = allowed

		// A manually locked target (per-variant lock) never searches; the
		// other variants of the language group are decided independently.
		if e.targetLocked(ctx, mediaType, mediaID, label, lang, t.Variant) {
			continue
		}

		needsSearch, isUpgrade, currentScore := decideTargetAction(
			ctx, existing, searchCfg, e, mediaType, mediaID, lang,
			t.Variant, label, upgradeCutoff, req.ForceUpgrade)

		states[i].needsSearch = needsSearch
		states[i].isUpgrade = isUpgrade
		states[i].currentScore = currentScore
		if needsSearch {
			anyNeedsSearch = true
		}
	}
	return states, anyNeedsSearch
}

// decideTargetAction determines whether a target needs searching and whether
// it's an upgrade. This is a pure decision function extracted for testability.
func decideTargetAction(ctx context.Context, existing *existingSubs, searchCfg *api.SearchConfig,
	e *Engine, mediaType api.MediaType, mediaID, lang string,
	variant api.Variant, label string, upgradeCutoff time.Time, forceUpgrade bool,
) (needsSearch, isUpgrade bool, currentScore int) {
	if !existing.hasSubtitle(lang, variant) {
		return true, false, 0
	}
	// ForceUpgrade bypasses the normal upgrade eligibility check.
	if forceUpgrade {
		score, _, found, err := e.store.CurrentScore(ctx, mediaType, mediaID, lang, variant)
		if err != nil {
			slog.Warn("CurrentScore failed during force upgrade",
				"media", label, "lang", lang, "variant", variant, "error", err)
			return true, true, 0
		}
		if found {
			return true, true, score
		}
		return true, true, 0
	}
	score, ok := e.checkUpgradeEligibility(ctx, existing, searchCfg,
		mediaType, mediaID, lang, variant, label, upgradeCutoff)
	if ok {
		return true, true, score
	}
	return false, false, 0
}

// filterBackedOff removes providers that are currently in adaptive backoff
// for the given media+language combination.
func (e *Engine) filterBackedOff(ctx context.Context, mediaType api.MediaType, mediaID, lang string, providers []api.Provider) []api.Provider {
	adaptive := e.cfg.Adaptive()
	if !adaptive.Enabled {
		return providers
	}
	backedOff, err := e.store.BackedOffProviders(ctx, mediaType, mediaID, lang, adaptive.MaxAttempts)
	if err != nil {
		slog.Warn("BackedOffProviders check failed, including all",
			"error", err)
		return providers
	}
	if len(backedOff) == 0 {
		return providers
	}
	backedOffSet := make(map[api.ProviderID]struct{}, len(backedOff))
	for _, name := range backedOff {
		backedOffSet[name] = struct{}{}
	}
	var eligible []api.Provider
	for _, p := range providers {
		if _, ok := backedOffSet[p.Name()]; ok {
			if e.metrics != nil {
				e.metrics.AdaptiveSkip()
			}
		} else {
			eligible = append(eligible, p)
		}
	}
	return eligible
}
