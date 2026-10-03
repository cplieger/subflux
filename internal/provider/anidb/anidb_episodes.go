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

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/xmlx"
)

// buildEpisodeCacheKey builds the episodeCache key for a series and
// episode number string. Numeric episode numbers are normalized to their
// integer form so lookups from episodeID (which builds the same key from
// an int) match regardless of leading zeros or whitespace. Non-numeric
// episode numbers (S1/C1/T1 for specials, credits, trailers) use the
// trimmed raw string.
//
// This is the write side of a grammar shared with episodeID's read side;
// both must agree byte for byte, or a lookup can resolve to the WRONG
// episode's AniDB id and the wrong subtitle gets written next to it.
func buildEpisodeCacheKey(seriesID int, epNo string) string {
	epNo = strings.TrimSpace(epNo)
	if n, err := strconv.Atoi(epNo); err == nil {
		return keyenc.Join(strconv.Itoa(seriesID), strconv.Itoa(n))
	}
	return keyenc.Join(strconv.Itoa(seriesID), epNo)
}

// rateLimitAniDB enforces AniDB's 1-req-per-2s policy using a channel-based
// timer so precise spacing doesn't require holding the mutex during the wait.
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

// episodeID returns the AniDB episode ID for a series+episode pair, fetching
// and caching the series' episodes on first access. Concurrent callers for one
// series share a single request, which keeps a library scan within AniDB's
// rate limit. A refused client key answers ahead of the cache, so a series
// cached while the key worked also searches by title; a ban until banUntil
// answers without a request.
func (m *Mapper) episodeID(ctx context.Context, seriesID, episodeNo int) (int, error) {
	cacheKey := keyenc.Join(strconv.Itoa(seriesID), strconv.Itoa(episodeNo))

	m.mu.Lock()
	if m.clientRejected != nil {
		err := m.clientRejected
		m.mu.Unlock()
		return 0, err
	}
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
//
// Code is the optional `code` attribute: errClientRejected names the client
// key, any other error is a reason to back off.
type anidbError struct {
	XMLName xml.Name `xml:"error"`
	Message string   `xml:",chardata"`
	Code    string   `xml:"code,attr"`
}

// cacheEpisodes retrieves all episodes for a series from the AniDB HTTP API
// and populates the shared episode cache. The caller reads results by
// cacheKey after this returns.
func (m *Mapper) cacheEpisodes(ctx context.Context, seriesID int) error {
	slog.Debug("anidb: fetching episodes from API", "series_id", seriesID)

	if err := m.rateLimitAniDB(ctx); err != nil {
		return err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, episodesFetchTimeout)
	defer cancel()

	reqURL := fmt.Sprintf(
		"%s?request=anime&client=%s&clientver=%d&protover=1&aid=%d",
		apiURL, url.QueryEscape(m.clientKey), clientVer, seriesID,
	)
	req, err := http.NewRequestWithContext(
		fetchCtx, http.MethodGet, reqURL, http.NoBody,
	)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return httpx.RedactTransportError(err, "anidb episodes", httpx.Secret(m.clientKey))
	}
	defer resp.Body.Close()

	if statusErr := httpwire.CheckHTTPStatus(resp); statusErr != nil {
		return statusErr
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxJSONResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > httpwire.MaxJSONResponseBytes {
		return fmt.Errorf("anidb episode XML exceeded %d bytes", httpwire.MaxJSONResponseBytes)
	}

	data, err = decompressIfGzipped(data, httpwire.MaxJSONResponseBytes)
	if err != nil {
		return fmt.Errorf("anidb: episodes decompress: %w", err)
	}

	// The byte cap and the inflate ceiling above bound the wire, not the
	// decode: encoding/xml materializes each token first, so gate the
	// inflated bytes once here, before either unmarshal below re-tokenizes
	// them.
	if err := xmlx.Preflight(data, episodeLimits); err != nil {
		return fmt.Errorf("anidb: episodes outside decode bounds: %w", err)
	}

	if err := m.apiError(data, seriesID); err != nil {
		return err
	}
	m.acceptClient()

	var anime anidbAnime
	if err := xml.Unmarshal(data, &anime); err != nil {
		return fmt.Errorf("parse episodes: %w", err)
	}

	m.mu.Lock()
	for _, ep := range anime.Episodes {
		m.episodeCache[buildEpisodeCacheKey(seriesID, ep.EpNo)] = ep.ID
	}
	m.mu.Unlock()

	if len(anime.Episodes) == 0 {
		// INFO, not WARN: a fresh anime not yet populated is a legitimate
		// zero-episode response, distinct from the silent-ban case above.
		slog.Info("anidb: API returned zero episodes", "series_id", seriesID)
	} else {
		slog.Debug("anidb: episodes cached",
			"series_id", seriesID, "episodes", len(anime.Episodes))
	}
	return nil
}

// apiError returns the error an AniDB <error> envelope in data reports, or
// nil when data is not one.
func (m *Mapper) apiError(data []byte, seriesID int) error {
	var errCheck anidbError
	if err := xml.Unmarshal(data, &errCheck); err != nil || errCheck.Message == "" {
		return nil
	}
	msg := m.upstreamText(errCheck.Message)
	if errCheck.Code == errClientRejected {
		return m.rejectClient(msg)
	}
	slog.Error("anidb: API returned error",
		"series_id", seriesID, "error", msg)
	m.recordBan()
	return fmt.Errorf("anidb API error: %s", msg)
}

// upstreamText prepares AniDB's error text for an error or a log line. The
// request carried the client key, so the text may echo it.
func (m *Mapper) upstreamText(s string) string {
	return logsafe.RedactedField(s, httpx.Secret(m.clientKey))
}

// rejectClient latches the client-key refusal, logging it the first time,
// and returns it.
func (m *Mapper) rejectClient(msg string) error {
	m.mu.Lock()
	first := m.clientRejected == nil
	if first {
		m.clientRejected = &subflux.AuthError{Msg: "AniDB refused the client key: " + msg}
	}
	m.clientAnswered = true
	err := m.clientRejected
	m.mu.Unlock()
	if first {
		slog.Error("anidb client key rejected; episode lookup disabled, searching by title",
			"provider", subflux.ProviderNameAnimeTosho, "reason", msg)
	}
	return err
}

// acceptClient records that AniDB answered a request carrying the client key
// with data rather than an error envelope.
func (m *Mapper) acceptClient() {
	m.mu.Lock()
	m.clientAnswered = true
	m.mu.Unlock()
}

// ClientKeyVerdict reports whether AniDB has said anything about the client
// key, by refusing it or by answering a request carrying it with data, and the
// refusal when it refused.
func (m *Mapper) ClientKeyVerdict() (answered bool, refusal error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clientAnswered, m.clientRejected
}

// ForgetClientKeyVerdict drops AniDB's answer about the client key and the
// episode IDs cached under it, so the next episode lookup asks AniDB again
// and latches anew if it refuses.
func (m *Mapper) ForgetClientKeyVerdict() {
	m.mu.Lock()
	m.clientAnswered = false
	m.clientRejected = nil
	clear(m.episodeCache)
	m.mu.Unlock()
}

// recordBan sets banUntil to suppress further API calls for banCooldown.
// Caller must not hold m.mu.
func (m *Mapper) recordBan() {
	m.mu.Lock()
	m.banUntil = time.Now().Add(banCooldown)
	m.mu.Unlock()
}
