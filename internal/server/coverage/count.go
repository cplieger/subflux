package coverage

import (
	"context"
	"log/slog"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/search"
)

// FileReader is the ONE thing the missing-count pass asks of the store: read
// every subtitle-file row for a media type, once per type, and count against it
// in memory. One of the twelve methods the coverage surface offers — this pass
// records nothing, stamps nothing, and never reads scan state.
//
// Exported because queryhandlers names it: the CountMissing function value it
// is handed takes this as a parameter, and naming this type is what keeps that
// signature from drifting into a wider one.
type FileReader interface {
	GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleEntry, error)
}

// CountCfg is what the missing-count pass reads out of the configuration: the
// language targets a media item earns, and the embedded-codec policy that
// decides which existing tracks count as usable. 2 of the 37 values the config
// offers — counting is arithmetic over targets and rows, so it asks nothing
// about providers, scoring, paths or the server runtime.
//
// Exported because queryhandlers names it: the CountMissing function value the
// composition root binds and hands over takes this as its parameter, and naming
// this type is what keeps that signature from drifting back to a wider one.
type CountCfg interface {
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []api.SubtitleTarget
	EmbeddedPolicy() api.EmbeddedPolicy
}

// CountMissing returns the total number of missing subtitle targets across
// all series and movies.
func CountMissing(ctx context.Context, cfg CountCfg, db FileReader, allSeries []arrapi.Series, allMovies []arrapi.Movie) int {
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
func CountMissingSeries(ctx context.Context, cfg CountCfg, db FileReader, allSeries []arrapi.Series, ignoredCodecs map[string]bool) int {
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
		targets := cfg.ResolveTargetsWithFallback(arrsvc.OriginalLangCode(ser.OriginalLanguage), nil)
		missing += missingForSeries(epCount, targets, prefixIdx[prefixes[i]])
	}
	return missing
}

// seriesPrefixes returns the per-series media-ID prefixes (parallel to
// allSeries) together with the set of non-empty prefixes for membership tests.
func seriesPrefixes(allSeries []arrapi.Series) (prefixes []string, prefixSet map[string]struct{}) {
	prefixes = make([]string, 0, len(allSeries))
	prefixSet = make(map[string]struct{}, len(allSeries))
	for i := range allSeries {
		prefix := mediaid.SeriesPrefix(allSeries[i].TvdbID, allSeries[i].ImdbID)
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
func CountMissingMovies(ctx context.Context, cfg CountCfg, db FileReader, allMovies []arrapi.Movie, ignoredCodecs map[string]bool) int {
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
		targets := cfg.ResolveTargetsWithFallback(arrsvc.OriginalLangCode(m.OriginalLanguage), nil)
		mediaID := mediaid.Movie(m.TmdbID, m.ImdbID)
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
