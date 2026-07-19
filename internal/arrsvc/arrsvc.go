// Package arrsvc composes the github.com/cplieger/arrapi Sonarr and Radarr
// clients with subflux's subtitle-specific operations: "wanted" iteration
// (which library items need a subtitle search), exclude-tag name resolution,
// and a fire-and-forget rescan. The raw library reads, per-item lookups,
// history polling, and connectivity ping are promoted from the embedded arrapi
// client unchanged, so this package adds only the app-level behavior arrapi
// deliberately does not provide.
package arrsvc

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// Retry policy preserving subflux's prior arr-client behavior: 3 total attempts
// with a 5s base delay (two between-attempt waits: 5s, 10s). arrapi keeps its
// 120s per-request timeout (when the caller's context has none) and its short
// ping timeout.
const (
	maxAttempts = 3
	baseDelay   = 5 * time.Second
)

// Sonarr composes arrapi.Sonarr with subflux's wanted-episode iteration,
// exclude-tag resolution, and error-only rescan.
type Sonarr struct {
	*arrapi.Sonarr
}

// Radarr composes arrapi.Radarr with subflux's wanted-movie iteration,
// exclude-tag resolution, and error-only rescan.
type Radarr struct {
	*arrapi.Radarr
}

// Compile-time role conformance: the composed clients satisfy the subflux
// consumer interfaces.
var (
	_ api.SonarrClient = (*Sonarr)(nil)
	_ api.RadarrClient = (*Radarr)(nil)
)

// NewSonarr builds a Sonarr service for the given base URL and API key.
func NewSonarr(baseURL, apiKey string) (*Sonarr, error) {
	c, err := arrapi.NewSonarr(baseURL, apiKey,
		arrapi.WithMaxAttempts(maxAttempts), arrapi.WithBaseDelay(baseDelay))
	if err != nil {
		return nil, err
	}
	return &Sonarr{Sonarr: c}, nil
}

// NewRadarr builds a Radarr service for the given base URL and API key.
func NewRadarr(baseURL, apiKey string) (*Radarr, error) {
	c, err := arrapi.NewRadarr(baseURL, apiKey,
		arrapi.WithMaxAttempts(maxAttempts), arrapi.WithBaseDelay(baseDelay))
	if err != nil {
		return nil, err
	}
	return &Radarr{Radarr: c}, nil
}

// RescanSeries asks Sonarr to rescan the series' folder for new or changed
// files (subflux calls this after writing a subtitle). It is fire-and-forget:
// arrapi returns the queued command, which subflux does not poll, so only the
// error is surfaced.
func (s *Sonarr) RescanSeries(ctx context.Context, seriesID int) error {
	_, err := s.Sonarr.RescanSeries(ctx, seriesID)
	return err
}

// RescanMovie asks Radarr to rescan the movie's folder for new or changed
// files. Fire-and-forget, like RescanSeries.
func (r *Radarr) RescanMovie(ctx context.Context, movieID int) error {
	_, err := r.Radarr.RescanMovie(ctx, movieID)
	return err
}

// ResolveExcludeTagIDs returns the arr tag IDs matching the given tag names.
// When logMissing is true, names with no matching tag are logged once at INFO
// (coverage passes false to avoid repeating the message on every page load).
func (s *Sonarr) ResolveExcludeTagIDs(ctx context.Context, names []string, logMissing bool) map[int]struct{} {
	return resolveExcludeTagIDs(ctx, s.ResolveTagIDs, names, logMissing)
}

// ResolveExcludeTagIDs is the Radarr-side counterpart.
func (r *Radarr) ResolveExcludeTagIDs(ctx context.Context, names []string, logMissing bool) map[int]struct{} {
	return resolveExcludeTagIDs(ctx, r.ResolveTagIDs, names, logMissing)
}

// resolveExcludeTagIDs delegates the fetch-and-match to arrapi.ResolveTagIDs and
// adds subflux's app-side behavior: fail open (log + nil) on a tag-fetch error,
// and, when logMissing is set, an INFO hint for each configured tag name that
// matched no arr tag.
func resolveExcludeTagIDs(ctx context.Context,
	resolve func(context.Context, ...string) (map[int]struct{}, []string, error),
	names []string, logMissing bool,
) map[int]struct{} {
	if len(names) == 0 {
		return nil
	}
	ids, unmatched, err := resolve(ctx, names...)
	if err != nil {
		slog.Warn("failed to fetch tags, exclude_arr_tags will not work", "error", err)
		return nil
	}
	if logMissing {
		for _, name := range unmatched {
			slog.Info("exclude_tag not found in arr, create it in Settings > Tags", "tag", name)
		}
	}
	return ids
}
