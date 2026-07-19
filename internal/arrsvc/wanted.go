package arrsvc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cplieger/arrapi"
	"golang.org/x/sync/errgroup"
)

// episodeFetchConcurrency limits parallel episode fetches per series. Bounded
// to avoid overwhelming the arr API with concurrent requests while still
// providing meaningful speedup over sequential fetching.
const episodeFetchConcurrency = 6

// seriesEpisodes pairs a series with the episodes that need a subtitle search.
type seriesEpisodes struct {
	episodes []arrapi.Episode
	series   arrapi.Series
}

// GetWantedEpisodes invokes fn for every episode that needs a subtitle search.
// It fetches the full series list first (closing that connection), then fetches
// each non-excluded series' episodes concurrently (bounded to 6 goroutines),
// then invokes fn sequentially. A series whose episode fetch keeps failing is
// logged and skipped rather than aborting the whole scan.
func (s *Sonarr) GetWantedEpisodes(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Series, arrapi.Episode) error) error {
	allSeries, err := s.GetSeries(ctx)
	if err != nil {
		return fmt.Errorf("fetch series list: %w", err)
	}
	logSeriesSummary(allSeries, excludeTagIDs)

	results, failedSeries := s.collectWantedEpisodes(ctx, allSeries, excludeTagIDs)
	if failedSeries > 0 {
		// Surface the partial-library condition in the scan summary: without
		// this, a repeatedly-failing series silently converts to "no wanted
		// episodes" and the scan reports ordinary completion.
		slog.Warn("scan covers a partial library: episode fetches failed for some series",
			"failed_series", failedSeries,
			"collected_series", len(results))
	}
	return dispatchEpisodes(ctx, results, fn)
}

// collectWantedEpisodes fetches episodes for every non-excluded series with
// bounded parallelism and returns the series that have at least one episode
// needing a subtitle search, plus the count of series whose episode fetch
// failed. It cannot fail: a series whose fetch errors is logged, counted, and
// skipped (see fetchWantedForSeries), so the scan always proceeds with
// whatever was collected.
func (s *Sonarr) collectWantedEpisodes(ctx context.Context, allSeries []arrapi.Series, excludeTagIDs map[int]struct{}) (wanted []seriesEpisodes, failed int) {
	var (
		mu           sync.Mutex
		results      []seriesEpisodes
		failedSeries int
	)

	var g errgroup.Group
	g.SetLimit(episodeFetchConcurrency)

	for i := range allSeries {
		if arrapi.HasAnyTag(allSeries[i].Tags, excludeTagIDs) {
			continue
		}
		ser := allSeries[i]
		g.Go(func() error {
			wanted, ok := s.fetchWantedForSeries(ctx, &ser)
			mu.Lock()
			switch {
			case !ok:
				failedSeries++
			case len(wanted) > 0:
				results = append(results, seriesEpisodes{series: ser, episodes: wanted})
			}
			mu.Unlock()
			return nil
		})
	}

	// The closures never return an error (skip-and-continue policy), so Wait
	// is used purely as a bounded-parallelism barrier.
	_ = g.Wait()
	return results, failedSeries
}

// fetchWantedForSeries fetches one series' episodes and returns those needing a
// subtitle search. A fetch error is logged and the series skipped (ok=false)
// so one failing series doesn't abort the scan; the caller counts the failure
// so the scan summary can report the partial library.
func (s *Sonarr) fetchWantedForSeries(ctx context.Context, ser *arrapi.Series) (wanted []arrapi.Episode, ok bool) {
	episodes, err := s.GetEpisodes(ctx, ser.ID)
	if err != nil {
		slog.Warn("failed to get episodes after retries, skipping series",
			"series", ser.Title, "series_id", ser.ID, "error", err)
		return nil, false
	}
	wanted = make([]arrapi.Episode, 0, len(episodes))
	for i := range episodes {
		if wantedEpisode(&episodes[i]) {
			wanted = append(wanted, episodes[i])
		}
	}
	return wanted, true
}

// dispatchEpisodes invokes fn for every collected episode sequentially,
// preserving the contract that fn is not called concurrently. It stops at the
// first callback error or context cancellation.
func dispatchEpisodes(ctx context.Context, results []seriesEpisodes, fn func(arrapi.Series, arrapi.Episode) error) error {
	for i := range results {
		for _, ep := range results[i].episodes {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := fn(results[i].series, ep); err != nil {
				return err
			}
		}
	}
	slog.Debug("finished iterating series", "processed", len(results))
	return nil
}

// GetWantedMovies invokes fn for every movie that needs a subtitle search.
// It fetches the full movie list first (closing that connection), then iterates
// locally, skipping movies with an excluded tag or no file.
func (r *Radarr) GetWantedMovies(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Movie) error) error {
	allMovies, err := r.GetMovies(ctx)
	if err != nil {
		return fmt.Errorf("fetch movie list: %w", err)
	}
	logMovieSummary(allMovies, excludeTagIDs)

	for i := range allMovies {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !wantedMovie(&allMovies[i], excludeTagIDs) {
			if allMovies[i].HasFile && allMovies[i].MovieFile == nil {
				slog.Debug("movie has file but no movieFile data", "movie", allMovies[i].Title)
			}
			continue
		}
		if err := fn(allMovies[i]); err != nil {
			return err
		}
	}
	return nil
}

// wantedEpisode reports whether an episode qualifies for subtitle search.
func wantedEpisode(ep *arrapi.Episode) bool {
	return ep.HasFile && ep.EpisodeFile != nil
}

// wantedMovie reports whether a movie qualifies for subtitle search.
func wantedMovie(m *arrapi.Movie, excludeTagIDs map[int]struct{}) bool {
	return m.HasFile && m.MovieFile != nil && !arrapi.HasAnyTag(m.Tags, excludeTagIDs)
}

// logSeriesSummary logs pre-scan totals from series statistics.
func logSeriesSummary(allSeries []arrapi.Series, excludeTagIDs map[int]struct{}) {
	var totalEpisodeFiles, targetSeries int
	for i := range allSeries {
		if arrapi.HasAnyTag(allSeries[i].Tags, excludeTagIDs) {
			continue
		}
		targetSeries++
		if allSeries[i].Statistics != nil {
			totalEpisodeFiles += allSeries[i].Statistics.EpisodeFileCount
		}
	}
	slog.Info("fetched series list",
		"total_series", len(allSeries),
		"target_series", targetSeries,
		"estimated_episode_files", totalEpisodeFiles)
}

// logMovieSummary logs pre-scan totals from the movie list.
func logMovieSummary(allMovies []arrapi.Movie, excludeTagIDs map[int]struct{}) {
	var targetMovies int
	for i := range allMovies {
		if wantedMovie(&allMovies[i], excludeTagIDs) {
			targetMovies++
		}
	}
	slog.Info("fetched movie list",
		"total_movies", len(allMovies),
		"target_movies", targetMovies)
}
