// Package search implements the subtitle search engine.
//
// orchestrate.go: search pipeline orchestration (lang-group search, variant processing)
// orchestrate_filter.go: eligibility checks, variant/score filtering, provider filtering
// provider_sweep.go: provider sweep, singleflight dedup, download
// target_state.go: target state building, decision logic, backoff
package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/scoring"
)

// searchLangGroup searches providers once for a language and processes
// multiple variant targets from the shared results. Targets sharing the
// same language code are queried together to halve API calls (e.g. fr
// standard + fr forced). Each target's results are filtered by variant,
// scored, and downloaded independently. Returns the typed per-language
// outcome consumed by the season tracker and scan stats.
func (e *Engine) searchLangGroup(ctx context.Context, req *api.SearchRequest,
	targets []api.SubtitleTarget, videoPath string, mediaType api.MediaType, mediaID string,
	existing *existingSubs, searchCfg *api.SearchConfig,
	upgradeCutoff time.Time,
) api.LangOutcome {
	lang := targets[0].Code
	label := req.MediaLabel()
	out := api.LangOutcome{Lang: lang}

	// Manual locks are checked per target (per variant) inside
	// buildTargetStates: a locked variant is excluded while its siblings keep
	// searching, so there is no language-level lock gate here.
	states, anyNeedsSearch := e.buildTargetStates(ctx, req, targets, existing,
		searchCfg, mediaType, mediaID, lang, label, upgradeCutoff)
	if !anyNeedsSearch {
		out.Kind = api.LangSkipped
		out.Skipped = len(targets)
		return out
	}

	// Collect the union of providers across all targets that need searching.
	unionProvs := e.unionProviders(states)
	eligible := e.filterBackedOff(ctx, mediaType, mediaID, lang, unionProvs)
	if len(eligible) == 0 {
		slog.Info("all providers backed off",
			"media", label, "lang", lang,
			"total_providers", len(unionProvs))
		// The group needed a search but zero provider queries ran: its own
		// kind, NOT "searched" — counting it as searched fed synthetic
		// no-result evidence into the season tracker and overstated the scan
		// summary every cycle a 7-day backoff overlapped a 24h scan.
		out.Kind = api.LangBackedOff
		return out
	}
	out.Kind = api.LangSearched

	// Single provider query for this language.
	langReq := *req
	langReq.Languages = []string{lang}
	outcome := e.searchProvidersFiltered(ctx, &langReq, eligible)
	// Record how many providers were actually queried (the health timeout
	// can zero this even on a "searched" group): the scan loops key the
	// inter-item pacing delay on real provider traffic, not on Kind.
	out.Queried = outcome.attempted()

	// Identity validation (shared across all variants).
	kept, dropped := scoring.FilterByIdentity(outcome.results, req)
	if dropped > 0 {
		if len(kept) == 0 {
			slog.Info("identity filter dropped all results",
				"media", req.MediaLabel(), "dropped", dropped)
		} else {
			slog.Debug("identity filter dropped results",
				"media", req.MediaLabel(), "dropped", dropped, "kept", len(kept))
		}
	}
	outcome.results = kept

	// Process each target variant from the shared results.
	anyNoResult := false
	for i := range states {
		if !states[i].needsSearch {
			out.Skipped++
			continue
		}
		out.Searched++

		path, noResult := e.processTargetVariant(ctx, req, &states[i],
			&outcome, videoPath, mediaType, mediaID, lang, label)
		if path != "" {
			out.Paths = append(out.Paths, path)
		}
		if noResult {
			anyNoResult = true
		}
	}

	// ONE provider query ran for the whole language group, so adaptive
	// backoff is recorded at most once per group: recording per variant
	// would advance the backoff ladder two steps per scan for a two-variant
	// language. recordProviderNoResults with an empty succeeded set is a
	// no-op, matching the per-variant guards above.
	if anyNoResult {
		e.recordProviderNoResults(ctx, mediaType, mediaID, lang,
			label, outcome.succeeded())
	}
	return out
}

// processTargetVariant filters, scores, and downloads a subtitle for one
// variant target from the shared provider results. Applies variant filtering
// (standard/forced/HI), per-target provider filtering, scoring with upgrade
// awareness, and iterates download candidates in score order. Returns the
// saved path (empty if no suitable subtitle was found) and whether this
// variant hit a genuine no-result (non-upgrade, providers answered, nothing
// usable) — the caller records adaptive backoff once per language group.
func (e *Engine) processTargetVariant(ctx context.Context, req *api.SearchRequest,
	state *targetState, outcome *searchOutcome,
	videoPath string, mediaType api.MediaType, mediaID, lang, label string,
) (path string, noResult bool) {
	// Filter by variant.
	filtered, variantFallback := filterByVariant(
		outcome.results, state.variant)
	if variantFallback {
		slog.Info("no regular subs, using HI fallback",
			"media", label, "lang", lang,
			"variant", state.variant,
			"hi_count", len(filtered))
	}

	// Further filter to only providers allowed for this target.
	targetFiltered := e.filterByTargetProviders(filtered, state.allowedProvs)

	if len(targetFiltered) == 0 {
		if !state.isUpgrade && len(outcome.succeeded()) > 0 {
			slog.Info("no results",
				"media", label, "media_id", mediaID,
				"lang", lang, "variant", state.variant,
				"searched", outcome.succeeded())
			return "", true
		}
		return "", false
	}

	video := videoInfoFromRequest(req)
	scored := scoreResults(e.scorer, &video, targetFiltered,
		e.cfg.ProviderPriority)
	minScore := e.cfg.MinScoreForTarget(state.target, req.MediaType)
	if state.isUpgrade && state.currentScore >= minScore {
		minScore = state.currentScore + 1
	}
	aboveMin := filterByScore(scored, minScore)
	if len(aboveMin) == 0 {
		return "", logNoResults(state, scored, lang, label, minScore)
	}

	if state.isUpgrade {
		slog.Info("upgrade: better subtitle found",
			"media", label, "lang", lang,
			"variant", state.variant,
			"current_score", state.currentScore,
			"new_score", aboveMin[0].score,
			"provider", aboveMin[0].sub.Provider)
	}

	return e.downloadBestCandidate(ctx, req, aboveMin,
		videoPath, mediaType, mediaID, lang, state.variant, label), false
}
