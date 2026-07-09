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

	results, err := s.collectWantedEpisodes(ctx, allSeries, excludeTagIDs)
	if err != nil {
		return err
	}
	return dispatchEpisodes(ctx, results, fn)
}

// collectWantedEpisodes fetches episodes for every non-excluded series with
// bounded parallelism and returns the series that have at least one episode
// needing a subtitle search.
func (s *Sonarr) collectWantedEpisodes(ctx context.Context, allSeries []arrapi.Series, excludeTagIDs map[int]struct{}) ([]seriesEpisodes, error) {
	var (
		mu      sync.Mutex
		results []seriesEpisodes
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(episodeFetchConcurrency)

	for i := range allSeries {
		if arrapi.HasAnyTag(allSeries[i].Tags, excludeTagIDs) {
			continue
		}
		ser := allSeries[i]
		g.Go(func() error {
			wanted := s.fetchWantedForSeries(gctx, &ser)
			if len(wanted) > 0 {
				mu.Lock()
				results = append(results, seriesEpisodes{series: ser, episodes: wanted})
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

// fetchWantedForSeries fetches one series' episodes and returns those needing a
// subtitle search. A fetch error is logged and the series skipped (returns nil)
// so one failing series doesn't abort the scan.
func (s *Sonarr) fetchWantedForSeries(ctx context.Context, ser *arrapi.Series) []arrapi.Episode {
	episodes, err := s.GetEpisodes(ctx, ser.ID)
	if err != nil {
		slog.Warn("failed to get episodes after retries, skipping series",
			"series", ser.Title, "series_id", ser.ID, "error", err)
		return nil
	}
	wanted := make([]arrapi.Episode, 0, len(episodes))
	for i := range episodes {
		if wantedEpisode(&episodes[i]) {
			wanted = append(wanted, episodes[i])
		}
	}
	return wanted
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
