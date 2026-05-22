package arrapi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"subflux/internal/api"
)

// doSingleflight wraps singleflight.Group.DoChan with type safety and context
// decoupling. The inner function runs with a detached context so that a single
// caller's cancellation doesn't abort the shared flight for all waiters. Each
// caller independently checks its own context after the flight completes.
func doSingleflight[T any](ctx context.Context, c *Client, key string, fn func(context.Context) (T, error)) (T, error) {
	ch := c.sfGroup.DoChan(key, func() (any, error) {
		return fn(context.WithoutCancel(ctx))
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			var zero T
			return zero, res.Err
		}
		return res.Val.(T), nil //nolint:errcheck // safe: fn returns T, DoChan preserves it
	}
}

// GetSeries fetches all series from Sonarr.
// Concurrent calls are coalesced via singleflight to avoid redundant HTTP requests.
func (c *Client) GetSeries(ctx context.Context) ([]api.Series, error) {
	return doSingleflight(ctx, c, "series", func(fCtx context.Context) ([]api.Series, error) {
		start := time.Now()
		series, err := fetchAll[api.Series](fCtx, c, apiPrefix+"/series")
		if err == nil {
			slog.Debug("fetched series from Sonarr", "count", len(series), "elapsed", time.Since(start).Round(time.Millisecond))
		}
		return series, err
	})
}

// getTags fetches all tags from the arr instance.
// Concurrent calls are coalesced via singleflight to avoid redundant HTTP requests.
func (c *Client) getTags(ctx context.Context) ([]api.Tag, error) {
	return doSingleflight(ctx, c, "tags", func(fCtx context.Context) ([]api.Tag, error) {
		tags, err := fetchAll[api.Tag](fCtx, c, apiPrefix+"/tag")
		if err == nil {
			slog.Debug("fetched tags from arr", "count", len(tags))
		}
		return tags, err
	})
}

// ResolveExcludeTagIDs fetches tags from the arr API and returns the IDs
// matching the given tag names. When logMissing is true, unknown names
// are logged once at INFO level; coverage API calls pass false to avoid
// repeating the message on every page load.
func (c *Client) ResolveExcludeTagIDs(ctx context.Context, names []string, logMissing bool) map[int]struct{} {
	if len(names) == 0 {
		return nil
	}
	tags, err := c.getTags(ctx)
	if err != nil {
		slog.Warn("failed to fetch tags, exclude_arr_tags will not work", "error", err)
		return nil
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	ids := make(map[int]struct{})
	for _, t := range tags {
		if _, ok := nameSet[t.Label]; ok {
			ids[t.ID] = struct{}{}
			delete(nameSet, t.Label)
		}
	}
	if logMissing {
		for name := range nameSet {
			slog.Info("exclude_tag not found in arr, create it in Settings > Tags",
				"tag", name)
		}
	}
	return ids
}

// GetEpisodes returns all episodes for a series from Sonarr.
// Concurrent calls for the same seriesID are coalesced via singleflight.
func (c *Client) GetEpisodes(ctx context.Context, seriesID int) ([]api.Episode, error) {
	return doSingleflight(ctx, c, fmt.Sprintf("episodes:%d", seriesID), func(fCtx context.Context) ([]api.Episode, error) {
		return fetchAll[api.Episode](fCtx, c, fmt.Sprintf(apiPrefix+"/episode?seriesId=%d&includeEpisodeFile=true", seriesID))
	})
}
