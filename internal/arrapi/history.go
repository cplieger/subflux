package arrapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// GetHistorySince returns history events since the given time.
// Pass eventType to filter (0 = all types).
//
// Wrapped in retryWithBackoff so transient 5xx / 429 responses during
// the poller cycle survive brief arr restarts. The history endpoint is
// idempotent so retry is safe.
func (c *Client) GetHistorySince(ctx context.Context, since time.Time, eventType api.HistoryEventType) ([]api.HistoryEntry, error) {
	params := url.Values{}
	params.Set("date", since.UTC().Format(time.RFC3339))
	if eventType > 0 {
		params.Set("eventType", fmt.Sprintf("%d", eventType))
	}
	params.Set("includeSeries", "false")
	params.Set("includeEpisode", "false")
	path := apiPrefix + "/history/since?" + params.Encode()

	return retryWithBackoff(ctx, c.maxAttempts, c.retryDelay, "history/since",
		func(ctx context.Context) ([]api.HistoryEntry, error) {
			resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeJSONSlice
			if err != nil {
				return nil, fmt.Errorf("history/since: %w", err)
			}
			entries, err := decodeJSONSlice[api.HistoryEntry](resp, httputil.MaxDownloadBytes, "history")
			if err != nil {
				return nil, err
			}
			slog.Debug("fetched history entries", "count", len(entries), "since", since.UTC().Format(time.RFC3339))
			return entries, nil
		})
}

// GetSeriesByID fetches a single series by internal ID.
func (c *Client) GetSeriesByID(ctx context.Context, id int) (*api.Series, error) {
	var s api.Series
	if err := c.fetchByID(ctx, idPath(apiPrefix+"/series/", id, ""), "series", id, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetEpisodeByID fetches a single episode by internal ID.
func (c *Client) GetEpisodeByID(ctx context.Context, id int) (*api.Episode, error) {
	var ep api.Episode
	if err := c.fetchByID(ctx, idPath(apiPrefix+"/episode/", id, "?includeEpisodeFile=true"), "episode", id, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// GetMovieByID fetches a single movie by internal ID.
func (c *Client) GetMovieByID(ctx context.Context, id int) (*api.Movie, error) {
	var m api.Movie
	if err := c.fetchByID(ctx, idPath(apiPrefix+"/movie/", id, ""), "movie", id, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
