package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- Eligibility ---

func (e *Engine) checkUpgradeEligibility(
	ctx context.Context, existing *existingSubs, searchCfg *subflux.SearchConfig,
	mediaType subflux.MediaType, mediaID, lang string, variant subflux.Variant, title string, cutoff time.Time,
) (int, bool) {
	if !existing.hasSubtitle(lang, variant) {
		return 0, false
	}
	if !searchCfg.UpgradeEnabled {
		slog.Debug("subtitle exists, upgrades disabled",
			"media", title, "lang", lang)
		return 0, false
	}
	score, mediaImported, found, err := e.store.CurrentScore(ctx, mediaType, mediaID, lang, variant)
	if err != nil {
		slog.Warn("CurrentScore failed, skipping upgrade check",
			"media", title, "lang", lang, "variant", variant, "error", err)
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

// logNoResults reports whether this is a genuine no-result (non-upgrade
// only); the caller aggregates this per language group so backoff is
// recorded at most once per group rather than once per variant.
func logNoResults(state *targetState, scored []scoredSub,
	lang, label string, minScore int,
) (noResult bool) {
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
		return false
	}
	slog.Info("no results above min score",
		"media", label, "lang", lang,
		"variant", state.variant,
		"best", bestScore, "min", minScore)
	return true
}

// --- Pipeline filtering ---

func (e *Engine) filterProviders(target *subflux.SubtitleTarget) []provider.Provider {
	allNames := make([]subflux.ProviderID, len(e.providers))
	for i, p := range e.providers {
		allNames[i] = p.Name()
	}
	allowedNames := e.cfg.ProvidersForTarget(target, allNames)
	allowedSet := make(map[subflux.ProviderID]struct{}, len(allowedNames))
	for _, n := range allowedNames {
		allowedSet[n] = struct{}{}
	}
	var out []provider.Provider
	for _, p := range e.providers {
		if _, ok := allowedSet[p.Name()]; ok {
			out = append(out, p)
		}
	}
	return out
}

// recordProviderNoResults does not penalize errored providers: their
// failure is infrastructure, not content.
func (e *Engine) recordProviderNoResults(ctx context.Context, mediaType subflux.MediaType, mediaID, lang, title string, succeeded []subflux.ProviderID) {
	adaptive := e.cfg.Adaptive()
	if !adaptive.Enabled {
		return
	}
	var recorded []subflux.ProviderID
	for _, prov := range succeeded {
		if err := e.store.RecordNoResult(ctx, mediaType, mediaID, lang, prov,
			subflux.BackoffParams{
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

// filterByVariant keeps only results matching the target variant; standard
// falls back to HI subs when no regular subtitle is found.
func filterByVariant(results []subflux.Subtitle, variant subflux.Variant) (filtered []subflux.Subtitle, fallback bool) {
	switch variant {
	case subflux.VariantForced:
		return forcedSubs(results), false
	case subflux.VariantHI:
		return hiOnlySubs(results), false
	default: // subflux.VariantStandard, "", or unknown variant
		return standardSubs(results)
	}
}

func forcedSubs(results []subflux.Subtitle) []subflux.Subtitle {
	var filtered []subflux.Subtitle
	for i := range results {
		if results[i].Forced {
			filtered = append(filtered, results[i])
		}
	}
	return filtered
}

func hiOnlySubs(results []subflux.Subtitle) []subflux.Subtitle {
	var filtered []subflux.Subtitle
	for i := range results {
		if results[i].HearingImp && !results[i].Forced {
			filtered = append(filtered, results[i])
		}
	}
	return filtered
}

// standardSubs falls back to HI subtitles when no regular subtitle is found;
// fallback reports whether that fallback was used.
func standardSubs(results []subflux.Subtitle) (filtered []subflux.Subtitle, fallback bool) {
	var regular, hi []subflux.Subtitle
	for i := range results {
		if results[i].Forced {
			continue
		}
		if results[i].HearingImp {
			hi = append(hi, results[i])
		} else {
			regular = append(regular, results[i])
		}
	}
	if len(regular) > 0 {
		return regular, false
	}
	return hi, len(hi) > 0
}

func filterByScore(scored []scoredSub, minScore int) []scoredSub {
	var eligible []scoredSub
	for i := range scored {
		if scored[i].score >= minScore {
			eligible = append(eligible, scored[i])
		}
	}
	return eligible
}
