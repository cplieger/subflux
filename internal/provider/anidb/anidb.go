// Package anidb provides TVDB → AniDB mapping using the community-maintained
// anime-list.xml from https://github.com/Anime-Lists/anime-lists.
// Optionally queries the AniDB HTTP API for episode-specific IDs.
package anidb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"subflux/internal/httputil"
	"subflux/internal/provider"
)

const (
	mappingURL = "https://raw.githubusercontent.com/Anime-Lists/anime-lists/master/anime-list.xml"
	apiURL     = "http://api.anidb.net:9001/httpapi"
	clientVer  = 1 // AniDB HTTP API client version; bump if AniDB requires it.

	// AniDB HTTP API policy: no more than one request every 2 seconds per
	// client IP. See https://wiki.anidb.net/HTTP_API_Definition.
	anidbMinInterval = 2 * time.Second

	// Per-request timeouts. Superseded the old shared 15s client timeout
	// because a 10 MB XML fetch over a throttled connection needs more
	// headroom than a small episode-list query.
	mappingFetchTimeout  = 60 * time.Second
	episodesFetchTimeout = 20 * time.Second

	// Ban cooldown: when AniDB returns an <error> XML body, suppress further
	// API calls for this long so we don't amplify a ban.
	banCooldown = 1 * time.Hour
)

// Mapper resolves TVDB IDs to AniDB episode IDs.
type Mapper struct {
	mappingTime  time.Time
	banUntil     time.Time
	sf           singleflight.Group
	mappingSF    singleflight.Group
	client       *http.Client
	parsedList   *animeList
	episodeCache map[string]int
	rateCh       chan struct{}
	clientKey    string
	mu           sync.Mutex
}

// NewMapper creates an AniDB mapper. clientKey is optional; if empty,
// only the XML mapping is used (no episode ID resolution).
func NewMapper(clientKey string) *Mapper {
	if clientKey != "" {
		slog.Warn("anidb: client key configured; API requests use plaintext HTTP (no HTTPS endpoint available)")
	}
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{} // initially ready
	return &Mapper{
		// Per-phase timeouts come from ssrf.SafeTransport (10s dial, 15s
		// response header) plus per-request context.WithTimeout at each
		// call site. The client-level Timeout is intentionally omitted so
		// large XML bodies aren't clipped mid-stream.
		client:       provider.NewHTTPClientNoClientTimeout(),
		clientKey:    clientKey,
		episodeCache: make(map[string]int),
		rateCh:       rateCh,
	}
}

// EpisodeResult holds the resolved AniDB IDs.
type EpisodeResult struct {
	AniDBSeriesID  int
	AniDBEpisodeID int // 0 if API key not configured or lookup failed
	AniDBEpisodeNo int // episode number within the AniDB entry
}

// Resolve maps a TVDB series ID + season + episode to AniDB IDs.
// Returns nil if no mapping exists.
func (m *Mapper) Resolve(ctx context.Context, tvdbID, season, episode int) *EpisodeResult {
	list, err := m.getParsedMapping(ctx)
	if err != nil {
		slog.Warn("anidb: failed to fetch mapping", "error", err)
		return nil
	}

	seriesID, epNo := findInMapping(list, tvdbID, season, episode)
	if seriesID == 0 {
		slog.Debug("anidb: no mapping found",
			"tvdb_id", tvdbID, "season", season, "episode", episode)
		return nil
	}

	result := &EpisodeResult{
		AniDBSeriesID:  seriesID,
		AniDBEpisodeNo: epNo,
	}

	// If we have an API key, resolve the episode-specific ID.
	if m.clientKey != "" && seriesID > 0 && epNo > 0 {
		epID, epErr := m.getEpisodeID(ctx, seriesID, epNo)
		if epErr != nil {
			slog.Debug("anidb: episode ID lookup failed",
				"series_id", seriesID, "ep_no", epNo, "error", epErr)
		} else {
			result.AniDBEpisodeID = epID
		}
	}

	return result
}

// --- Mapping XML ---

// getParsedMapping returns the cached parsed anime list, fetching and
// parsing the XML if the cache is stale or empty. The lock is held for
// the entire fetch to prevent thundering herd on cache expiry. Since
// the cache refreshes every 24h and the fetch takes <2s, blocking
// concurrent callers is acceptable. On fetch failure, returns the stale
// cache (if any) with a warning log rather than failing the lookup.
func (m *Mapper) getParsedMapping(ctx context.Context) (*animeList, error) {
	m.mu.Lock()
	if m.parsedList != nil && time.Since(m.mappingTime) < 24*time.Hour {
		list := m.parsedList
		m.mu.Unlock()
		return list, nil
	}
	m.mu.Unlock()

	// Use singleflight to coalesce concurrent mapping fetches.
	v, err, _ := m.mappingSF.Do("mapping", func() (any, error) {
		list, fetchErr := m.fetchMapping(ctx)
		if fetchErr != nil {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.parsedList != nil {
				slog.Warn("anidb: fetch failed, using stale mapping",
					"error", fetchErr, "cache_age", time.Since(m.mappingTime).Round(time.Minute))
				return m.parsedList, nil
			}
			return nil, fetchErr
		}

		m.mu.Lock()
		m.parsedList = list
		m.mappingTime = time.Now()
		clear(m.episodeCache)
		m.mu.Unlock()
		return list, nil
	})
	if err != nil {
		return nil, err
	}
	list, _ := v.(*animeList) //nolint:errcheck // singleflight guarantees *animeList on nil error
	return list, nil
}

// fetchMapping downloads and parses the anime-list.xml mapping file.
// Public raw-file fetch on raw.githubusercontent.com; any non-200 is a
// fetch failure, not an auth/rate-limit signal, so we keep a plain error
// here rather than calling httputil.CheckHTTPStatus (which would map 403
// to *api.AuthError, misleading for a public blob fetch).
func (m *Mapper) fetchMapping(ctx context.Context) (*animeList, error) {
	slog.Debug("anidb: fetching anime-list.xml")

	fetchCtx, cancel := context.WithTimeout(ctx, mappingFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		fetchCtx, http.MethodGet, mappingURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > httputil.MaxDownloadBytes {
		return nil, fmt.Errorf("anime-list.xml exceeded %d bytes", httputil.MaxDownloadBytes)
	}

	// Defensive: GitHub's CDN always auto-negotiates gzip, which net/http's
	// Transport decompresses transparently. If a future refactor disables
	// that (Accept-Encoding override, Transport.DisableCompression=true),
	// the body arrives as raw gzip bytes and xml.Unmarshal fails with a
	// garbled error. Detecting the magic header surfaces the real cause.
	data, err = decompressIfGzipped(data, httputil.MaxDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("anidb: mapping decompress: %w", err)
	}

	var list animeList
	if err := xml.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse mapping: %w", err)
	}

	slog.Debug("anidb: mapping loaded", "bytes", len(data),
		"entries", len(list.Animes))
	return &list, nil
}
