package anidb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"subflux/internal/httputil"
)

// --- AniDB HTTP API ---

// buildEpisodeCacheKey builds the episodeCache key for a series and
// episode number string. Numeric episode numbers are normalized to their
// integer form so lookups from getEpisodeID (which formats keys with %d)
// match regardless of leading zeros or whitespace in the source XML.
// Non-numeric episode numbers (S1/C1/T1 for specials, credits, trailers)
// use the trimmed raw string.
func buildEpisodeCacheKey(seriesID int, epNo string) string {
	epNo = strings.TrimSpace(epNo)
	if n, err := strconv.Atoi(epNo); err == nil {
		return fmt.Sprintf("%d:%d", seriesID, n)
	}
	return fmt.Sprintf("%d:%s", seriesID, epNo)
}

// rateLimitAniDB enforces AniDB's 1-req-per-2s policy using a channel-based
// timer. The rateCh channel has capacity 1: a token is available when the
// next request is permitted. After consuming the token, a time.AfterFunc
// re-fills the channel after anidbMinInterval, ensuring precise spacing
// without holding the mutex during the wait.
func (m *Mapper) rateLimitAniDB(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.rateCh:
	}
	time.AfterFunc(anidbMinInterval, func() {
		m.rateCh <- struct{}{}
	})
	return nil
}

// getEpisodeID returns the AniDB episode ID for a series+episode pair.
// Fetches and caches all episodes from the series on first access.
//
// Concurrent callers for the same seriesID coalesce via singleflight:
// only one goroutine issues the HTTP request; the rest share its result.
// This defends AniDB's strict rate limit from a library-scan thundering herd (F3).
//
// A short-circuit on banUntil suppresses further API traffic when AniDB
// previously returned an <error> XML body (F2).
func (m *Mapper) getEpisodeID(ctx context.Context, seriesID, episodeNo int) (int, error) {
	cacheKey := fmt.Sprintf("%d:%d", seriesID, episodeNo)

	// Fast path: already cached.
	m.mu.Lock()
	if id, ok := m.episodeCache[cacheKey]; ok {
		m.mu.Unlock()
		return id, nil
	}
	if !m.banUntil.IsZero() && time.Now().Before(m.banUntil) {
		remaining := time.Until(m.banUntil).Round(time.Second)
		m.mu.Unlock()
		slog.Debug("anidb: skipping API call during cooldown", "remaining", remaining)
		return 0, fmt.Errorf("anidb in cooldown (%s remaining)", remaining)
	}
	m.mu.Unlock()

	// Coalesce concurrent fetches for the same series.
	sfKey := strconv.Itoa(seriesID)
	_, err, _ := m.sf.Do(sfKey, func() (any, error) {
		return nil, m.cacheEpisodes(ctx, seriesID)
	})
	if err != nil {
		return 0, err
	}

	m.mu.Lock()
	id, ok := m.episodeCache[cacheKey]
	m.mu.Unlock()
	if ok {
		return id, nil
	}
	return 0, fmt.Errorf("episode %d not found in AniDB series %d", episodeNo, seriesID)
}

// --- AniDB HTTP API types ---

type anidbAnime struct {
	Episodes []anidbEpisode `xml:"episodes>episode"`
}

type anidbEpisode struct {
	EpNo string `xml:"epno"`
	ID   int    `xml:"id,attr"`
}

// anidbError captures AniDB's error XML envelope. AniDB signals errors
// with HTTP 200 + <error>Banned</error> (or similar); without this check
// the response silently unmarshals into anidbAnime with zero episodes.
type anidbError struct {
	XMLName xml.Name `xml:"error"`
	Message string   `xml:",chardata"`
}

// cacheEpisodes retrieves all episodes for a series from the AniDB HTTP API
// and populates the shared episode cache. Returns an error on HTTP failure,
// malformed XML, or an AniDB <error> response. The caller reads results by
// cacheKey after this returns (command-query split per Q1).
func (m *Mapper) cacheEpisodes(ctx context.Context, seriesID int) error {
	slog.Debug("anidb: fetching episodes from API", "series_id", seriesID)

	if err := m.rateLimitAniDB(ctx); err != nil {
		return err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, episodesFetchTimeout)
	defer cancel()

	reqURL := fmt.Sprintf(
		"%s?request=anime&client=%s&clientver=%d&protover=1&aid=%d",
		apiURL, url.QueryEscape(m.clientKey), clientVer, seriesID)
	req, err := http.NewRequestWithContext(
		fetchCtx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if statusErr := httputil.CheckHTTPStatus(resp); statusErr != nil {
		return statusErr
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > httputil.MaxJSONResponseBytes {
		return fmt.Errorf("anidb episode XML exceeded %d bytes", httputil.MaxJSONResponseBytes)
	}

	data, err = decompressIfGzipped(data, httputil.MaxJSONResponseBytes)
	if err != nil {
		return fmt.Errorf("anidb: episodes decompress: %w", err)
	}

	// AniDB signals errors with HTTP 200 + <error>...</error>. Check for
	// the error envelope before unmarshalling the normal shape; otherwise
	// a Banned/Client-invalid response looks like a zero-episode series.
	var errCheck anidbError
	if err := xml.Unmarshal(data, &errCheck); err == nil && errCheck.Message != "" {
		slog.Error("anidb: API returned error",
			"series_id", seriesID, "error", errCheck.Message)
		m.recordBan()
		return fmt.Errorf("anidb API error: %s", errCheck.Message)
	}

	var anime anidbAnime
	if err := xml.Unmarshal(data, &anime); err != nil {
		return fmt.Errorf("parse episodes: %w", err)
	}

	// Cache all episodes from this series. See buildEpisodeCacheKey for
	// the normalization rules (integer epNo canonicalized to %d; specials
	// like S1/C1/T1 kept as strings).
	m.mu.Lock()
	for _, ep := range anime.Episodes {
		m.episodeCache[buildEpisodeCacheKey(seriesID, ep.EpNo)] = ep.ID
	}
	m.mu.Unlock()

	if len(anime.Episodes) == 0 {
		// Legitimate zero-episode response (e.g. fresh anime not yet populated).
		// Logged at INFO so operators can distinguish it from the silent-ban
		// case handled above.
		slog.Info("anidb: API returned zero episodes", "series_id", seriesID)
	} else {
		slog.Debug("anidb: episodes cached",
			"series_id", seriesID, "episodes", len(anime.Episodes))
	}
	return nil
}

// recordBan sets banUntil to suppress further API calls for banCooldown.
// Caller must not hold m.mu.
func (m *Mapper) recordBan() {
	m.mu.Lock()
	m.banUntil = time.Now().Add(banCooldown)
	m.mu.Unlock()
}
