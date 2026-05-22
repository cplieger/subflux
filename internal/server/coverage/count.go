package coverage

import (
	"context"
	"log/slog"

	"subflux/internal/api"
	"subflux/internal/search"
)

// CountMissing returns the total number of missing subtitle targets across
// all series and movies.
func CountMissing(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []api.Series, allMovies []api.Movie) int {
	ignoredCodecs := search.IgnoredCodecsFromConfig(cfg)
	return CountMissingSeries(ctx, cfg, db, allSeries, ignoredCodecs) +
		CountMissingMovies(ctx, cfg, db, allMovies, ignoredCodecs)
}

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

	type langKey struct{ lang, variant string }
	type prefixCounts map[langKey]int

	prefixes := make([]string, 0, len(allSeries))
	prefixSet := make(map[string]struct{}, len(allSeries))
	for i := range allSeries {
		prefix := api.BuildSeriesPrefix(allSeries[i].TvdbID, allSeries[i].ImdbID)
		prefixes = append(prefixes, prefix)
		if prefix != "" {
			prefixSet[prefix] = struct{}{}
		}
	}

	prefixIdx := make(map[string]prefixCounts, len(prefixes))
	for epMediaID, subs := range episodeSubs {
		prefix := ExtractSeriesPrefix(epMediaID)
		if prefix == "" {
			continue
		}
		if _, ok := prefixSet[prefix]; !ok {
			continue
		}
		pc := prefixIdx[prefix]
		if pc == nil {
			pc = make(prefixCounts)
			prefixIdx[prefix] = pc
		}
		for k, st := range subs {
			if st != nil && st.Usable {
				pc[langKey{k.Lang, k.Variant}]++
			}
		}
	}

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
		prefix := prefixes[i]
		pc := prefixIdx[prefix]
		for _, t := range targets {
			have := 0
			if pc != nil {
				have = pc[langKey{t.Code, string(t.EffectiveVariant())}]
			}
			if have < epCount {
				missing += epCount - have
			}
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
