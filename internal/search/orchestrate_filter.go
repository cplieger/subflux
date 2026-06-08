package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Eligibility ---

// checkUpgradeEligibility determines if an existing subtitle is eligible for upgrade.
// Returns (currentScore, true) if eligible, (0, false) if not.
func (e *Engine) checkUpgradeEligibility(
	ctx context.Context, existing *existingSubs, searchCfg *api.SearchConfig,
	mediaType api.MediaType, mediaID, lang string, variant api.Variant, title string, cutoff time.Time,
) (int, bool) {
	if !existing.hasSubtitle(lang, variant) {
		return 0, false // No subtitle on disk; not an upgrade.
	}
	if !searchCfg.UpgradeEnabled {
		slog.Debug("subtitle exists, upgrades disabled",
			"media", title, "lang", lang)
		return 0, false
	}
	score, mediaImported, found, err := e.store.CurrentScore(ctx, mediaType, mediaID, lang)
	if err != nil {
		slog.Warn("CurrentScore failed, skipping upgrade check",
			"media", title, "lang", lang, "error", err)
		return 0, false
	}
	if !found || mediaImported.Before(cutoff) || score >= e.cfg.Scores().Hash {
		slog.Debug("subtitle exists, not eligible for upgrade",
			"media", title, "lang", lang,
			"score", score, "found", found)
		return 0, false
	}
	slog.Debug("subtitle eligible for upgrade",
		"media", title, "lang", lang,
		"current_score", score)
	return score, true
}

// logNoResults logs the appropriate message when no results meet the minimum
// score threshold, distinguishing between upgrade and initial search cases.
func (e *Engine) logNoResults(ctx context.Context, state *targetState, scored []scoredSub,
	outcome *searchOutcome, mediaType api.MediaType, mediaID, lang, label string,
	minScore int,
) {
	bestScore := 0
	if len(scored) > 0 {
		bestScore = scored[0].score
	}
	if state.isUpgrade {
		slog.Debug("upgrade: no improvement",
			"media", label, "lang", lang,
			"variant", state.variant,
			"current", state.currentScore,
			"best", bestScore)
	} else {
		slog.Info("no results above min score",
			"media", label, "lang", lang,
			"variant", state.variant,
			"best", bestScore, "min", minScore)
		e.recordProviderNoResults(ctx, mediaType, mediaID, lang,
			label, outcome.succeeded())
	}
}

// --- Pipeline filtering ---

// filterProviders returns the subset of engine providers allowed for the target.
func (e *Engine) filterProviders(target *api.SubtitleTarget) []api.Provider {
	allNames := make([]api.ProviderID, len(e.providers))
	for i, p := range e.providers {
		allNames[i] = p.Name()
	}
	allowedNames := e.cfg.ProvidersForTarget(target, allNames)
	allowedSet := make(map[api.ProviderID]struct{}, len(allowedNames))
	for _, n := range allowedNames {
		allowedSet[n] = struct{}{}
	}
	var out []api.Provider
	for _, p := range e.providers {
		if _, ok := allowedSet[p.Name()]; ok {
			out = append(out, p)
		}
	}
	return out
}

// recordProviderNoResults records adaptive backoff for each provider that
// responded successfully but returned no usable results. Providers that
// errored are NOT penalized (their failure is infrastructure, not content).
func (e *Engine) recordProviderNoResults(ctx context.Context, mediaType api.MediaType, mediaID, lang, title string, succeeded []api.ProviderID) {
	adaptive := e.cfg.Adaptive()
	if !adaptive.Enabled {
		return
	}
	var recorded []api.ProviderID
	for _, prov := range succeeded {
		if err := e.store.RecordNoResult(ctx, mediaType, mediaID, lang, prov,
			api.BackoffParams{
				InitialDelay: adaptive.InitialDelay,
				MaxDelay:     adaptive.MaxDelay,
				Multiplier:   adaptive.BackoffMultiplier,
			}); err != nil {
			slog.Warn("failed to record no-result backoff",
				"provider", prov, "media", title, "error", err)
		} else {
			recorded = append(recorded, prov)
		}
	}
	if len(recorded) > 0 {
		slog.Debug("no result, backoff recorded",
			"media", title, "media_id", mediaID, "lang", lang,
			"providers", recorded)
	}
}

// filterByVariant keeps only results matching the target variant.
// For "standard": non-HI, non-forced (with HI fallback if no regular found).
// For "forced": only forced subs.
// For "hi": only HI subs.
func filterByVariant(results []api.Subtitle, variant api.Variant) (filtered []api.Subtitle, fallback bool) {
	switch variant {
	case api.VariantForced:
		for i := range results {
			if results[i].Forced {
				filtered = append(filtered, results[i])
			}
		}
		return filtered, false
	case api.VariantHI:
		for i := range results {
			if results[i].HearingImp && !results[i].Forced {
				filtered = append(filtered, results[i])
			}
		}
		return filtered, false
	default: // api.VariantStandard, "", or unknown variant
		var regular, hiSubs []api.Subtitle
		for i := range results {
			if results[i].Forced {
				continue
			}
			if results[i].HearingImp {
				hiSubs = append(hiSubs, results[i])
			} else {
				regular = append(regular, results[i])
			}
		}
		if len(regular) > 0 {
			return regular, false
		}
		return hiSubs, len(hiSubs) > 0
	}
}

// filterByScore returns results at or above minScore.
func filterByScore(scored []scoredSub, minScore int) []scoredSub {
	var eligible []scoredSub
	for i := range scored {
		if scored[i].score >= minScore {
			eligible = append(eligible, scored[i])
		}
	}
	return eligible
}
