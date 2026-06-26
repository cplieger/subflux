package arrapi

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/sync/errgroup"
)

// episodeFetchConcurrency limits parallel episode fetches per series.
// Bounded to avoid overwhelming the arr API with concurrent requests
// while still providing meaningful speedup over sequential fetching.
const episodeFetchConcurrency = 6

// seriesEpisodes pairs a series with the episodes that need a subtitle search.
type seriesEpisodes struct {
	episodes []api.Episode
	series   api.Series
}

// GetWantedEpisodes returns episodes that need subtitle searches.
// Fetches the full series list first (closes the connection), then
// fetches episodes per series concurrently (bounded to 6 goroutines).
// The callback fn is invoked sequentially after all episodes are collected.
// Series or episodes with any excludeTagID are skipped.
func (c *Client) GetWantedEpisodes(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(api.Series, api.Episode) error) error {
	allSeries, err := c.GetSeries(ctx)
	if err != nil {
		return fmt.Errorf("fetch series list: %w", err)
	}

	logSeriesSummary(allSeries, excludeTagIDs)

	results, err := c.collectWantedEpisodes(ctx, allSeries, excludeTagIDs)
	if err != nil {
		return err
	}
	return dispatchEpisodes(ctx, results, fn)
}

// collectWantedEpisodes fetches episodes for every non-excluded series with
// bounded parallelism and returns the series that have at least one episode
// needing a subtitle search.
func (c *Client) collectWantedEpisodes(ctx context.Context, allSeries []api.Series, excludeTagIDs map[int]struct{}) ([]seriesEpisodes, error) {
	var (
		mu      sync.Mutex
		results []seriesEpisodes
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(episodeFetchConcurrency)

	for i := range allSeries {
		if api.HasExcludeTag(allSeries[i].Tags, excludeTagIDs) {
			continue
		}
		s := allSeries[i]
		g.Go(func() error {
			wanted := c.fetchWantedForSeries(gctx, &s)
			if len(wanted) > 0 {
				mu.Lock()
				results = append(results, seriesEpisodes{series: s, episodes: wanted})
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

// fetchWantedForSeries fetches one series' episodes and returns those needing
// a subtitle search. A fetch error is logged and the series skipped (returns
// nil) so one failing series doesn't abort the scan.
func (c *Client) fetchWantedForSeries(ctx context.Context, s *api.Series) []api.Episode {
	episodes, err := c.GetEpisodes(ctx, s.ID)
	if err != nil {
		slog.Warn("failed to get episodes after retries, skipping series",
			"series", s.Title, "series_id", s.ID, "error", err)
		return nil
	}
	wanted := make([]api.Episode, 0, len(episodes))
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
func dispatchEpisodes(ctx context.Context, results []seriesEpisodes, fn func(api.Series, api.Episode) error) error {
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

// logSeriesSummary logs pre-scan totals from series statistics.
func logSeriesSummary(allSeries []api.Series, excludeTagIDs map[int]struct{}) {
	var totalEpisodeFiles, targetSeries int
	for i := range allSeries {
		if api.HasExcludeTag(allSeries[i].Tags, excludeTagIDs) {
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

// wantedEpisode reports whether an episode qualifies for subtitle search.
func wantedEpisode(ep *api.Episode) bool {
	return ep.HasFile && ep.EpisodeFile != nil
}
