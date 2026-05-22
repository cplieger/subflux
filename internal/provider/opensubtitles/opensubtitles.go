// Package opensubtitles implements the OpenSubtitles.com REST API provider.
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"subflux/internal/api"
	"subflux/internal/httputil"
	"subflux/internal/provider"
	"subflux/internal/provider/classify"
	"subflux/internal/ssrf"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	providerName = api.ProviderNameOpenSubtitles
	baseURL      = "https://api.opensubtitles.com/api/v1"
	tokenExpiry  = 12 * time.Hour

	// Rate limits per OpenSubtitles API tier (source: API documentation).
	// VIP tier (paid subscription): 5 requests/second.
	// Free tier (default): 1 request/second.
	// These are request rate limits only; the daily download quota
	// (governed by the user's subscription level) is enforced separately
	// by the API and does not affect request pacing.
	vipRateLimit    = 200 * time.Millisecond // 5 req/s
	freeRateLimit   = time.Second            // 1 req/s
	schemeAired     = "aired"
	matchByTitle    = api.MatchByTitle
	matchByImdb     = api.MatchByIMDB
	settingPassword = provider.KeyPassword
)

// --- Factory and Provider ---

// Compile-time assertion: *Provider satisfies api.ShowSubtitleCounter.
var _ api.ShowSubtitleCounter = (*Provider)(nil)

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

// Factory creates an OpenSubtitles provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.Username == "" || ps.Password == "" {
		return nil, errors.New("opensubtitles: username and password required")
	}
	if ps.APIKey == "" {
		return nil, errors.New("opensubtitles: api_key required")
	}
	// Override default: OpenSubtitles defaults to use_hash=true when not specified.
	useHash := ps.UseHash
	if _, ok := settings[string(provider.KeyUseHash)]; !ok {
		useHash = true
	}
	includeAI := provider.SettingBool(settings, provider.KeyIncludeAI, false)

	// Channel-based token bucket: capacity 1, pre-filled so the first request
	// proceeds immediately. A background ticker refills at the rate limit interval.
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}

	return &Provider{
		username:  ps.Username,
		password:  ps.Password,
		apiKey:    ps.APIKey,
		useHash:   useHash,
		includeAI: includeAI,
		rateCh:    rateCh,
		client:    provider.NewHTTPClient(provider.HTTPTimeoutExtended),
	}, nil
}

// Provider implements the OpenSubtitles.com API.
type Provider struct {
	tokenTime  time.Time
	tokenSfg   singleflight.Group
	client     *http.Client
	rateCh     chan struct{} // token-bucket channel; one token per rate-limit interval
	token      string
	apiKey     string
	password   string
	serverHost string
	username   string
	tokenMu    sync.RWMutex
	vip        bool
	useHash    bool
	includeAI  bool
}

func (p *Provider) Name() api.ProviderID { return providerName }

// Search queries OpenSubtitles for subtitles matching the request. For episodes
// with alternate numbering (scene, absolute), it searches each scheme and merges
// deduplicated results.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	// For episodes with alternate numbering, search each scheme and merge.
	numberings := episodeNumberings(req)
	if len(numberings) <= 1 {
		return p.searchNumbering(ctx, req, req.Season, req.Episode)
	}

	// Search numbering schemes concurrently. The rate limiter serializes
	// actual HTTP calls, but errgroup overlaps response parsing with the
	// next request's rate-limit wait, saving ~30-50% wall-clock time.
	type numberingResult struct {
		err     error
		results []api.Subtitle
	}
	perScheme := make([]numberingResult, len(numberings))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(numberings))
	for i, n := range numberings {
		g.Go(func() error {
			slog.Debug("opensubtitles: trying numbering",
				"season", n.season, "episode", n.episode,
				"scheme", n.scheme, "media", req.Title)
			results, err := p.searchNumbering(gctx, req, n.season, n.episode)
			if err != nil {
				slog.Warn("opensubtitles: numbering search failed",
					"scheme", n.scheme, "error", err)
			}
			perScheme[i] = numberingResult{results: results, err: err}
			return nil // never fail the group; collect all results
		})
	}
	_ = g.Wait() //nolint:errcheck // errors are non-fatal; collected per-scheme above

	// Merge and dedup (single-threaded after collection).
	seen := make(map[string]bool)
	var merged []api.Subtitle
	var lastErr error
	for _, nr := range perScheme {
		if nr.err != nil {
			lastErr = nr.err
			continue
		}
		for i := range nr.results {
			if !seen[nr.results[i].ID] {
				seen[nr.results[i].ID] = true
				merged = append(merged, nr.results[i])
			}
		}
	}

	// If all numbering schemes failed with errors and we got no results,
	// propagate the last error so the caller doesn't penalize the provider
	// with adaptive backoff for a transient API failure.
	if len(merged) == 0 && lastErr != nil {
		return nil, fmt.Errorf("all numbering schemes failed: %w", lastErr)
	}

	slog.Info("opensubtitles multi-numbering search complete",
		"results", len(merged), "schemes", len(numberings),
		"media", req.Title)
	return merged, nil
}

// CountShowSubtitles returns the total number of subtitles available for a
// show (by IMDB ID) in a single language, without specifying season/episode.
// This enables show-level pre-checks: if a show has very few subtitles
// relative to its episode count, the caller can skip the entire series.
// Implements api.ShowSubtitleCounter.
func (p *Provider) CountShowSubtitles(ctx context.Context, imdbID, lang string) (int, error) {
	sanitized := classify.SanitizeImdbID(imdbID)
	if sanitized == "" {
		// Placeholder inputs like "tt0" / "tt00000" sanitize to empty.
		// Calling the API with parent_imdb_id= is a guaranteed zero-result
		// round trip, so short-circuit before rate-limiting a dead call.
		slog.Debug("opensubtitles show count skipped — empty imdb",
			"imdb", imdbID, "lang", lang)
		return 0, nil
	}
	if err := p.ensureToken(ctx); err != nil {
		return 0, fmt.Errorf("auth: %w", err)
	}

	params := url.Values{}
	params.Set("parent_imdb_id", sanitized)
	params.Set("languages", toOSLang(lang))
	if !p.includeAI {
		params.Set("ai_translated", "exclude")
	}

	slog.Debug("opensubtitles show count", "imdb", imdbID, "lang", lang)

	body, err := p.doGet(ctx, "/subtitles", params)
	if err != nil {
		slog.Warn("opensubtitles show count failed", "imdb", imdbID, "lang", lang, "error", err)
		return 0, fmt.Errorf("show count: %w", err)
	}
	defer func() { httputil.DrainClose(body) }()

	var resp searchResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		slog.Warn("opensubtitles show count decode failed", "imdb", imdbID, "lang", lang, "error", err)
		return 0, fmt.Errorf("decode show count: %w", err)
	}

	slog.Debug("opensubtitles show count result",
		"imdb", imdbID, "lang", lang, "total_count", resp.TotalCount)
	return resp.TotalCount, nil
}

func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	// Validate file ID is numeric to prevent JSON injection.
	fileID, err := strconv.Atoi(sub.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid file ID %q: %w", sub.ID, err)
	}

	slog.Debug("opensubtitles downloading subtitle", "file_id", fileID)

	// Request download link. The /download endpoint must always use the
	// default base URL (api.opensubtitles.com), not the VIP server host
	// returned by login. The VIP host is for search queries only.
	reqBody, err := json.Marshal(map[string]any{
		"file_id":    fileID,
		"sub_format": "srt",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal download request: %w", err)
	}

	body, err := p.doPostDownload(ctx, "/download", bytes.NewReader(reqBody))
	if err != nil {
		slog.Warn("opensubtitles download request failed",
			"file_id", fileID, "error", err)
		return nil, fmt.Errorf("request download: %w", err)
	}
	defer httputil.DrainClose(body)

	var dlResp downloadResponse
	if err := json.NewDecoder(body).Decode(&dlResp); err != nil {
		return nil, fmt.Errorf("decode download response: %w", err)
	}

	if dlResp.Link == "" {
		return nil, errors.New("empty download link")
	}

	slog.Debug("opensubtitles download link received", "file_id", fileID)
	p.logQuota(&dlResp)

	// Validate download URL to prevent SSRF via malicious API responses.
	if err := ssrf.ValidateURL(dlResp.Link); err != nil {
		return nil, fmt.Errorf("download URL rejected: %w", err)
	}

	return p.fetchSubtitleFile(ctx, fileID, dlResp.Link)
}

// fetchSubtitleFile downloads the subtitle content from the given URL.
func (p *Provider) fetchSubtitleFile(ctx context.Context, fileID int, link string) ([]byte, error) {
	if err := p.rateLimit(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download subtitle: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("opensubtitles subtitle fetch failed", "file_id", fileID, "status", resp.StatusCode)
		return nil, fmt.Errorf("download subtitle: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read subtitle: %w", err)
	}

	if err := api.ValidateSubtitleData(data); err != nil {
		return nil, fmt.Errorf("opensubtitles: %w", err)
	}

	slog.Info("opensubtitles subtitle downloaded",
		"file_id", fileID, "bytes", len(data))

	return data, nil
}
