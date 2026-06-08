package arrapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
)

// GetMovies fetches all movies from Radarr.
// Concurrent calls are coalesced via singleflight to avoid redundant HTTP requests.
func (c *Client) GetMovies(ctx context.Context) ([]api.Movie, error) {
	return doSingleflight(ctx, c, "movies", func(fCtx context.Context) ([]api.Movie, error) {
		return fetchAll[api.Movie](fCtx, c, apiPrefix+"/movie")
	})
}

// GetWantedMovies returns movies that need subtitle searches.
// Fetches the full movie list first (closes the connection), then
// iterates locally.
// Movies with any excludeTagID are skipped.
func (c *Client) GetWantedMovies(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(api.Movie) error) error {
	allMovies, err := c.GetMovies(ctx)
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
