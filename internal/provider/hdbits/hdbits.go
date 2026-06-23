// Package hdbits implements the HDBits.org subtitle provider.
// Uses the HDBits API with username/passkey authentication.
package hdbits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/provider/dlcache"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	settingPasskey  = "passkey"
	settingUsername = "username"
)

// --- Constants and factory ---

// hdbitsConfig holds tuning parameters for the HDBits provider.
// Grouped as a struct for inspectability and future configurability.
type hdbitsConfig struct {
	TorrentLookupDelay       time.Duration
	MaxCacheItemSize         int64
	MaxCacheEntries          int
	TorrentLookupConcurrency int
	MaxTorrentsPerSearch     int
}

// defaultHDBitsConfig returns the production tuning parameters.
var defaultHDBitsConfig = hdbitsConfig{
	TorrentLookupDelay:       500 * time.Millisecond,
	MaxCacheItemSize:         2 << 20, // 2 MB
	MaxCacheEntries:          100,
	TorrentLookupConcurrency: 3,
	MaxTorrentsPerSearch:     50,
}

const providerName = api.ProviderNameHDBits

// hdbAcceptedExts lists filename extensions accepted as subtitle content
// (direct files) or season-pack archives. Matched case-insensitively.
var hdbAcceptedExts = func() map[string]bool {
	m := make(map[string]bool, len(archive.SubtitleExts)+2)
	maps.Copy(m, archive.SubtitleExts)
	m[".zip"] = true
	m[".rar"] = true
	return m
}()

// Factory creates an HDBits provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.Username == "" || ps.Passkey == "" {
		return nil, errors.New("hdbits: username and passkey required")
	}
	slog.Debug("hdbits provider initialized", settingUsername, ps.Username)
	return &Provider{
		client:       provider.NewHTTPClient(provider.HTTPTimeoutStandard),
		username:     ps.Username,
		passkey:      ps.Passkey,
		cfg:          defaultHDBitsConfig,
		torrentCache: cache.New[[]int](1 * time.Hour),
		dlCache:      dlcache.New(defaultHDBitsConfig.MaxCacheEntries, defaultHDBitsConfig.MaxCacheItemSize),
	}, nil
}

// (download cache types + lifecycle methods moved to cache.go for clearer
// separation between API-interaction logic in this file and cache mechanics.)

// Provider implements the HDBits subtitle API.
type Provider struct {
	dlSfg        singleflight.Group
	client       *http.Client
	torrentCache *cache.Cache[[]int]
	dlCache      *dlcache.DownloadCache
	username     string
	passkey      string
	cfg          hdbitsConfig
}

// Compile-time interface checks.
var (
	_ api.Provider     = (*Provider)(nil)
	_ api.CacheClearer = (*Provider)(nil)
)

// --- Provider API (Name, Search, Download) ---

// Name returns the provider identifier for HDBits.
func (p *Provider) Name() api.ProviderID { return providerName }

// Search finds subtitles for the given request by resolving torrent IDs via the
// HDBits API and inspecting each torrent's subtitle metadata.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	slog.Debug("hdbits searching",
		"media_type", req.MediaType, "title", req.Title,
		"season", req.Season, "episode", req.Episode,
		"imdb_id", req.ImdbID, "tvdb_id", req.TvdbID)

	lookup, cacheKey := p.buildLookup(req)
	if lookup == nil {
		slog.Debug("hdbits: no usable ID, skipping")
		return nil, nil
	}

	ids, err := p.resolveTorrentIDs(ctx, lookup, cacheKey)
	if err != nil {
		return nil, fmt.Errorf("find torrents: %w", err)
	}

	slog.Debug("hdbits torrents found", "count", len(ids))

	var (
		mu      sync.Mutex
		results []api.Subtitle
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(p.cfg.TorrentLookupConcurrency)

	for i, id := range ids {
		// Rate limit: wait between dispatches (not between completions).
		if i > 0 {
			t := time.NewTimer(p.cfg.TorrentLookupDelay)
			select {
			case <-gctx.Done():
				t.Stop()
				return results, gctx.Err()
			case <-t.C:
			}
		}

		g.Go(func() error {
			subs, err := p.getSubtitles(gctx, id, req)
			if err != nil {
				slog.Warn("hdbits: failed to get subtitles for torrent", "error", err)
				return nil // non-fatal; continue other goroutines
			}
			mu.Lock()
			results = append(results, subs...)
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()

	slog.Info("hdbits search complete", "results", len(results), "media", req.MediaLabel())
	return results, nil
}

// Download fetches the subtitle content for the given search result.
// Season pack archives are cached per subtitle ID and reused across
// episodes; the target episode is extracted by S##E## filename match.
func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	// Validate ID is numeric to prevent path injection.
	if _, err := strconv.Atoi(sub.ID); err != nil {
		return nil, fmt.Errorf("invalid subtitle ID %q: %w", sub.ID, err)
	}

	data, err := p.fetchOrCached(ctx, sub.ID)
	if err != nil {
		return nil, err
	}

	// Normalize: extract the target episode from a season pack archive
	// when applicable, then validate once.
	result, err := provider.ExtractAndValidate(data, sub.Season, sub.Episode)
	if err != nil {
		return nil, fmt.Errorf("hdbits: %w", err)
	}
	if len(result) != len(data) {
		slog.Debug("hdbits: extracted from archive",
			"subtitle_id", sub.ID,
			"season", sub.Season, "episode", sub.Episode,
			"archive_bytes", len(data), "extracted_bytes", len(result))
	} else {
		slog.Debug("hdbits download complete", "id", sub.ID, "bytes", len(result))
	}
	return result, nil
}

// (ClearCache and evictOldest moved to cache.go alongside the cache types.)

// buildLookup constructs the HDBits API search parameters and cache key
// from the search request. Returns nil params when the request lacks the
// required ID for the media type.
func (p *Provider) buildLookup(req *api.SearchRequest) (params map[string]any, cacheKey string) {
	if req.MediaType == api.MediaTypeEpisode {
		if req.TvdbID <= 0 {
			return nil, ""
		}
		return map[string]any{
			settingUsername: p.username, settingPasskey: p.passkey,
			"tvdb": map[string]any{"id": req.TvdbID, "season": req.Season},
		}, fmt.Sprintf("torrents:tvdb:%d:s%d", req.TvdbID, req.Season)
	}
	if req.ImdbID == "" {
		return nil, ""
	}
	imdb := classify.SanitizeImdbID(req.ImdbID)
	imdbNum, err := strconv.Atoi(imdb)
	if err != nil {
		return nil, ""
	}
	return map[string]any{
		settingUsername: p.username, settingPasskey: p.passkey,
		"imdb": map[string]any{"id": imdbNum},
	}, "torrents:imdb:" + req.ImdbID
}

// resolveTorrentIDs returns the torrent IDs to iterate for a search,
// serving from cache when available and capping the list at
// maxTorrentsPerSearch so long-running series don't stall the scan.
func (p *Provider) resolveTorrentIDs(ctx context.Context, lookup map[string]any, cacheKey string) ([]int, error) {
	if cached, ok := p.torrentCache.Get(cacheKey); ok {
		return capTorrentIDs(cached, p.cfg.MaxTorrentsPerSearch, cacheKey), nil
	}
	ids, err := p.findTorrentIDs(ctx, lookup, cacheKey)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		p.torrentCache.Set(cacheKey, ids)
	}
	return capTorrentIDs(ids, p.cfg.MaxTorrentsPerSearch, cacheKey), nil
}

// capTorrentIDs trims ids to the given cap and logs once if the
// cap was hit, so operators notice when HDBits returns a pathologically
// large list.
func capTorrentIDs(ids []int, maxCap int, cacheKey string) []int {
	if len(ids) <= maxCap {
		return ids
	}
	slog.Warn("hdbits: torrent list exceeded cap, truncating",
		"returned", len(ids), "cap", maxCap,
		"cache_key", cacheKey)
	return ids[:maxCap]
}

// fetchOrCached returns cached download data for a subtitle ID, or fetches
// and caches it. Season pack zips are typically downloaded once and reused
// for each episode in the season. Concurrent requests for the same subtitle
// ID are deduplicated via singleflight.
func (p *Provider) fetchOrCached(ctx context.Context, subID string) ([]byte, error) {
	if cached, ok := p.dlCache.Get(subID); ok {
		slog.Debug("hdbits: serving from download cache",
			"subtitle_id", subID, "bytes", len(cached))
		return cached, nil
	}

	v, err, _ := p.dlSfg.Do(subID, func() (any, error) {
		// Re-check cache inside singleflight to avoid redundant fetch when
		// a concurrent caller already populated it.
		if cached, ok := p.dlCache.Get(subID); ok {
			return cached, nil
		}
		return p.doFetch(ctx, subID)
	})
	if err != nil {
		return nil, err
	}
	data, _ := v.([]byte)
	return data, nil
}

// doFetch performs the actual HTTP download and caches the result.
func (p *Provider) doFetch(ctx context.Context, subID string) ([]byte, error) {
	// Build URL via url.Values so any non-hex character in the passkey
	// cannot corrupt the request-line or split the secret across bogus
	// query params. HDBits passkeys are documented as 32-hex today, but
	// encoding costs nothing and matches the pattern used elsewhere.
	q := url.Values{}
	q.Set("id", subID)
	q.Set(settingPasskey, p.passkey)
	dlURL := "https://hdbits.org/getdox.php?" + q.Encode()

	slog.Debug("hdbits downloading subtitle", "subtitle_id", subID)
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, dlURL, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("hdbits download %s: %w", subID, err), p.passkey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "hdbits download "+subID, p.passkey)
	}
	defer resp.Body.Close()

	if httpErr := httputil.CheckHTTPStatus(resp); httpErr != nil {
		return nil, httputil.RedactSecret(httpErr, p.passkey)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if err != nil {
		return nil, httputil.RedactSecret(err, p.passkey)
	}

	p.dlCache.Put(subID, data, func() {
		slog.Warn("hdbits: download too large or cache full, will re-fetch for each episode",
			"subtitle_id", subID, "bytes", len(data))
	})

	return data, nil
}

// findTorrentIDs searches the HDBits API for torrents matching the given
// parameters and returns their IDs. debugKey is a redacted cache key used
// purely for structured logging; params contains the passkey and must
// never be logged.
func (p *Provider) findTorrentIDs(ctx context.Context, params map[string]any, debugKey string) ([]int, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	slog.Debug("hdbits searching torrents", "lookup", debugKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://hdbits.org/api/torrents", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(httputil.HeaderContentType, httputil.ContentTypeJSON)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result struct {
		Data []struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes)).Decode(&result); err != nil { // torrent list limit
		return nil, err
	}

	ids := make([]int, len(result.Data))
	for i, d := range result.Data {
		ids[i] = d.ID
	}
	return ids, nil
}

// getSubtitles fetches subtitle metadata for a single torrent and filters
// by language and content type (excludes commentary/extras).
func (p *Provider) getSubtitles(ctx context.Context, torrentID int, searchReq *api.SearchRequest) ([]api.Subtitle, error) {
	slog.Debug("hdbits fetching subtitles for torrent", "torrent_id", torrentID)

	params := map[string]any{
		settingUsername: p.username, settingPasskey: p.passkey,
		"torrent_id": torrentID,
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://hdbits.org/api/subtitles", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(httputil.HeaderContentType, httputil.ContentTypeJSON)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result struct {
		Data []hdbSubtitleItem `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes)).Decode(&result); err != nil { // subtitle list limit
		return nil, err
	}

	return filterSubtitleData(result.Data, searchReq), nil
}
