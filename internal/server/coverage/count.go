package coverage

import (
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search"
)

// CountMissing returns the total number of missing subtitle targets across
// all series and movies.
func CountMissing(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []api.Series, allMovies []api.Movie) int {
	ignoredCodecs := search.IgnoredCodecsFromConfig(cfg)
	return CountMissingSeries(ctx, cfg, db, allSeries, ignoredCodecs) +
		CountMissingMovies(ctx, cfg, db, allMovies, ignoredCodecs)
}

// langKey identifies a subtitle language+variant for missing-count accounting.
type langKey struct{ lang, variant string }

// prefixCounts maps a language+variant to the number of episodes that have a
// usable subtitle for it, within a single series prefix.
type prefixCounts map[langKey]int

// CountMissingSeries returns the number of missing subtitle targets for series.
func CountMissingSeries(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []api.Series, ignoredCodecs map[string]bool) int {
	if len(allSeries) == 0 {
		return 0
	}
	epFiles, err := db.GetSubtitleFiles(ctx, api.MediaTypeEpisode, "")
	if err != nil {
		slog.Warn("countMissingSeries: DB query failed", "error", err)
		return 0
	}
	episodeSubs := IndexSubStatus(epFiles, ignoredCodecs)

	prefixes, prefixSet := seriesPrefixes(allSeries)
	prefixIdx := usableSubsByPrefix(episodeSubs, prefixSet)

	var missing int
	for i := range allSeries {
		ser := &allSeries[i]
		epCount := 0
		if ser.Statistics != nil {
			epCount = ser.Statistics.EpisodeFileCount
		}
		if epCount == 0 {
			continue
		}
		targets := cfg.ResolveTargetsWithFallback(ser.OriginalLangCode(), nil)
		missing += missingForSeries(epCount, targets, prefixIdx[prefixes[i]])
	}
	return missing
}

// seriesPrefixes returns the per-series media-ID prefixes (parallel to
// allSeries) together with the set of non-empty prefixes for membership tests.
func seriesPrefixes(allSeries []api.Series) (prefixes []string, prefixSet map[string]struct{}) {
	prefixes = make([]string, 0, len(allSeries))
	prefixSet = make(map[string]struct{}, len(allSeries))
	for i := range allSeries {
		prefix := api.BuildSeriesPrefix(allSeries[i].TvdbID, allSeries[i].ImdbID)
		prefixes = append(prefixes, prefix)
		if prefix != "" {
			prefixSet[prefix] = struct{}{}
		}
	}
	return prefixes, prefixSet
}

// usableSubsByPrefix groups, per known series prefix, the count of usable
// subtitles for each language+variant across all of that series' episodes.
// Episode media IDs that don't map to a series in prefixSet are ignored.
func usableSubsByPrefix(episodeSubs map[string]map[Key]*Status, prefixSet map[string]struct{}) map[string]prefixCounts {
	idx := make(map[string]prefixCounts, len(prefixSet))
	for epMediaID, subs := range episodeSubs {
		prefix := ExtractSeriesPrefix(epMediaID)
		if prefix == "" {
			continue
		}
		if _, ok := prefixSet[prefix]; !ok {
			continue
		}
		pc := idx[prefix]
		if pc == nil {
			pc = make(prefixCounts)
			idx[prefix] = pc
		}
		countUsableSubs(pc, subs)
	}
	return idx
}

// countUsableSubs increments pc for every usable subtitle in subs.
func countUsableSubs(pc prefixCounts, subs map[Key]*Status) {
	for k, st := range subs {
		if st != nil && st.Usable {
			pc[langKey{k.Lang, k.Variant}]++
		}
	}
}

// missingForSeries returns the number of missing subtitle slots for one series:
// for each target, the number of episodes lacking a usable subtitle. pc may be
// nil when the series has no indexed subtitles.
func missingForSeries(epCount int, targets []api.SubtitleTarget, pc prefixCounts) int {
	var missing int
	for _, t := range targets {
		have := 0
		if pc != nil {
			have = pc[langKey{t.Code, string(t.EffectiveVariant())}]
		}
		if have < epCount {
			missing += epCount - have
		}
	}
	return missing
}

// CountMissingMovies returns the number of missing subtitle targets for movies.
func CountMissingMovies(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allMovies []api.Movie, ignoredCodecs map[string]bool) int {
	if len(allMovies) == 0 {
		return 0
	}
	movieFiles, err := db.GetSubtitleFiles(ctx, api.MediaTypeMovie, "")
	if err != nil {
		slog.Warn("countMissingMovies: DB query failed", "error", err)
		return 0
	}
	movieSubs := IndexSubStatus(movieFiles, ignoredCodecs)
	var missing int
	for i := range allMovies {
		m := &allMovies[i]
		if !m.HasFile {
			continue
		}
		targets := cfg.ResolveTargetsWithFallback(m.OriginalLangCode(), nil)
		mediaID := api.BuildMovieID(m.TmdbID, m.ImdbID)
		if mediaID == "" {
			continue
		}
		subs := movieSubs[mediaID]
		for _, t := range targets {
			variant := t.EffectiveVariant()
			if st := subs[Key{Lang: t.Code, Variant: string(variant)}]; st == nil || !st.Usable {
				missing++
			}
		}
	}
	return missing
}
