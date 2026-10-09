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

	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/subflux"
)

// searchLangGroup searches providers once for a language and processes
// multiple variant targets from the shared results. Targets sharing the
// same language code are queried together to halve API calls (e.g. fr
// standard + fr forced). Each target's results are filtered by variant,
// scored, and downloaded independently. Returns the typed per-language
// outcome consumed by the season tracker and scan stats, and the folder fault
// a save in the group learned.
func (e *Engine) searchLangGroup(ctx context.Context, req *subflux.SearchRequest,
	targets []subflux.SubtitleTarget, videoPath string, mediaType subflux.MediaType, mediaID string,
	existing *existingSubs, searchCfg *subflux.SearchConfig,
	upgradeCutoff time.Time,
) (subflux.LangOutcome, *mediawrite.UnwritableError) {
	lang := targets[0].Code
	label := logsafe.Field(req.MediaLabel())
	out := subflux.LangOutcome{Lang: lang}

	// Manual locks are checked per target (per variant) inside
	// buildTargetStates: a locked variant is excluded while its siblings keep
	// searching, so there is no language-level lock gate here.
	states, anyNeedsSearch := e.buildTargetStates(ctx, req, targets, existing,
		searchCfg, mediaType, mediaID, lang, label, upgradeCutoff)
	if !anyNeedsSearch {
		out.Kind = subflux.LangSkipped
		return out, nil
	}
	if folder, blocked := e.media.Blocked(videoPath); blocked {
		slog.Debug("media folder not writable, skipping search",
			"media", label, "lang", lang, "folder", folder)
		writeBlockedGroup(&out, states)
		return out, nil
	}

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
		out.Kind = subflux.LangBackedOff
		return out, nil
	}
	out.Kind = subflux.LangSearched

	langReq := *req
	langReq.Languages = []string{lang}
	outcome := e.searchProvidersFiltered(ctx, &langReq, eligible)
	// Record how many providers were actually queried (the health timeout
	// can zero this even on a "searched" group): the scan loops key the
	// inter-item pacing delay on real provider traffic, not on Kind.
	out.Queried = outcome.attempted()

	keepIdentityMatches(&outcome, req)
	out.Answered = len(outcome.succeeded())

	anyNoResult := false
	var writeFailure *mediawrite.UnwritableError
	for i := range states {
		if !states[i].needsSearch {
			continue
		}
		out.Searched++

		path, res, wf := e.processTargetVariant(ctx, req, &states[i],
			&outcome, videoPath, mediaType, mediaID, lang, label)
		if writeFailure == nil {
			writeFailure = wf
		}
		switch res {
		case targetSaved:
			out.Paths = append(out.Paths, path)
		case targetNoResult:
			anyNoResult = true
		case targetDownloadFailed:
			out.Failed++
		case targetWriteBlocked:
			out.WriteBlocked++
		case targetNone:
		}
	}

	// ONE provider query ran for the whole language group, so adaptive
	// backoff is recorded at most once per group: recording per variant
	// would advance the backoff ladder two steps per scan for a two-variant
	// language. A failed download in the group means a provider HAD a
	// candidate, and the retry it needs must not be backed off; an
	// unwritable folder says nothing about the providers.
	if anyNoResult && out.Failed == 0 && out.WriteBlocked == 0 {
		e.recordProviderNoResults(ctx, mediaType, mediaID, lang,
			label, outcome.succeeded())
	}
	return out, writeFailure
}

func writeBlockedGroup(out *subflux.LangOutcome, states []targetState) {
	for i := range states {
		if states[i].needsSearch {
			out.WriteBlocked++
		}
	}
	out.Kind = subflux.LangWriteBlocked
}

func keepIdentityMatches(outcome *searchOutcome, req *subflux.SearchRequest) {
	kept, dropped := scoring.FilterByIdentity(outcome.results, req)
	if dropped > 0 {
		if len(kept) == 0 {
			slog.Info("identity filter dropped all results",
				"media", logsafe.Field(req.MediaLabel()), "dropped", dropped)
		} else {
			slog.Debug("identity filter dropped results",
				"media", logsafe.Field(req.MediaLabel()), "dropped", dropped, "kept", len(kept))
		}
	}
	outcome.results = kept
}

type targetResult int

const (
	// targetNone: nothing to record, because no provider answered or an
	// upgrade found nothing better.
	targetNone targetResult = iota
	// targetSaved: a subtitle was written.
	targetSaved
	// targetNoResult: providers answered and, after the variant, provider and
	// score filters, not one candidate was left. The only result that is
	// evidence of absence.
	targetNoResult
	// targetDownloadFailed: candidates existed and none was saved, whatever
	// the reason (gated, refused, rate limited, a provider error, bad
	// content, a write the target's path refused). Upgrades included.
	targetDownloadFailed
	// targetWriteBlocked: the target's folder refuses writes, so nothing was
	// tried or the save stopped there. A fact about the folder, recorded
	// against nothing.
	targetWriteBlocked
)

// processTargetVariant searches one variant target in the language group's
// shared results; see targetResult for what each outcome means. The path is
// set only for targetSaved, the folder fault only when a save learned one.
func (e *Engine) processTargetVariant(ctx context.Context, req *subflux.SearchRequest,
	state *targetState, outcome *searchOutcome,
	videoPath string, mediaType subflux.MediaType, mediaID, lang, label string,
) (path string, res targetResult, writeFailure *mediawrite.UnwritableError) {
	filtered, variantFallback := filterByVariant(
		outcome.results, state.variant,
	)
	if variantFallback {
		slog.Info("no regular subs, using HI fallback",
			"media", label, "lang", lang,
			"variant", state.variant,
			"hi_count", len(filtered))
	}

	targetFiltered := filterByTargetProviders(filtered, state.allowedProvs)

	if len(targetFiltered) == 0 {
		if !state.isUpgrade && len(outcome.succeeded()) > 0 {
			slog.Info("no results",
				"media", label, "media_id", mediaID,
				"lang", lang, "variant", state.variant,
				"searched", outcome.succeeded())
			return "", targetNoResult, nil
		}
		return "", targetNone, nil
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
		if logNoResults(state, scored, lang, label, minScore) {
			return "", targetNoResult, nil
		}
		return "", targetNone, nil
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
		videoPath, mediaType, mediaID, lang, state.variant, label)
}
