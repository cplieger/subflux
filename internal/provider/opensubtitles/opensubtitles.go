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

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/ssrf/v4"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	providerName = subflux.ProviderNameOpenSubtitles
	baseURL      = "https://api.opensubtitles.com/api/v1"
	tokenExpiry  = 12 * time.Hour

	// VIP (paid): 5 req/s. Free (default): 1 req/s. Request-rate limits
	// only; the daily download quota is enforced separately by the API.
	vipRateLimit    = 200 * time.Millisecond // 5 req/s
	freeRateLimit   = time.Second            // 1 req/s
	schemeAired     = "aired"
	matchByTitle    = subflux.MatchByTitle
	matchByImdb     = subflux.MatchByIMDB
	settingPassword = provider.KeyPassword
)

// --- Factory and Provider ---

// Factory creates an OpenSubtitles provider from settings.
func Factory(_ context.Context, settings map[string]any) (provider.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.Username == "" || ps.Password == "" {
		return nil, errors.New("opensubtitles: username and password required")
	}
	if ps.APIKey == "" {
		return nil, errors.New("opensubtitles: api_key required")
	}
	// use_hash's default (true) comes from the providerEntries schema entry
	// (P14); a bare settings map (unit tests) reads false, the typed zero.
	useHash := ps.UseHash
	includeAI := provider.SettingBool(settings, provider.KeyIncludeAI, false)
	includeMT := provider.SettingBool(settings, provider.KeyIncludeMT, false)

	// Channel token bucket, capacity 1, pre-filled; a background ticker
	// refills at the rate-limit interval.
	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}

	return &Provider{
		username:  ps.Username,
		password:  ps.Password,
		apiKey:    ps.APIKey,
		useHash:   useHash,
		includeAI: includeAI,
		includeMT: includeMT,
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
	includeMT  bool
}

// Name returns the provider identifier for OpenSubtitles.
func (p *Provider) Name() subflux.ProviderID { return providerName }

// numberingResult holds the outcome of searching one numbering scheme.
type numberingResult struct {
	err     error
	results []subflux.Subtitle
}

// Search queries OpenSubtitles for subtitles matching the request. For episodes
// with alternate numbering (scene, absolute), it searches each scheme and merges
// deduplicated results.
func (p *Provider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	numberings := episodeNumberings(req)
	if len(numberings) <= 1 {
		return p.searchNumbering(ctx, req, req.Season, req.Episode)
	}

	perScheme := p.searchNumberingsConcurrent(ctx, req, numberings)
	merged, lastErr := mergeNumberingResults(perScheme)

	// Propagate the last error when every scheme failed, so the caller
	// doesn't penalize the provider with adaptive backoff for a transient
	// API failure.
	if len(merged) == 0 && lastErr != nil {
		return nil, fmt.Errorf("all numbering schemes failed: %w", lastErr)
	}

	slog.Info("opensubtitles multi-numbering search complete",
		"results", len(merged), "schemes", len(numberings),
		"media", req.Title)
	return merged, nil
}

// searchNumberingsConcurrent searches each numbering scheme concurrently. The
// rate limiter serializes the actual HTTP calls, but errgroup overlaps response
// parsing with the next request's rate-limit wait, saving ~30-50% wall-clock
// time. The returned slice is index-aligned with numberings; a failed scheme
// carries its error and never aborts the group.
func (p *Provider) searchNumberingsConcurrent(ctx context.Context,
	req *subflux.SearchRequest, numberings []numbering,
) []numberingResult {
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
	_ = g.Wait()
	return perScheme
}

// mergeNumberingResults merges per-scheme results into a single slice
// deduplicated by subtitle ID, returning the last error seen (if any) so the
// caller can distinguish "no results" from "all schemes errored".
func mergeNumberingResults(perScheme []numberingResult) ([]subflux.Subtitle, error) {
	seen := make(map[string]bool)
	var merged []subflux.Subtitle
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
	return merged, lastErr
}

// CountShowSubtitles returns the total subtitle count for a show (by IMDB ID)
// in one language, without season/episode — used for show-level pre-checks
// that skip an entire series with too few subtitles. Implements the optional
// provider.ResolveShowCounter interface.
func (p *Provider) CountShowSubtitles(ctx context.Context, q subflux.ShowSubtitleQuery) (int, error) {
	imdbID, lang := q.ImdbID, q.Language
	sanitized := classify.SanitizeImdbID(imdbID)
	if sanitized == "" {
		// e.g. "tt0"/"tt00000" sanitize to empty; parent_imdb_id= would be
		// a guaranteed zero-result round trip.
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
	if p.includeMT {
		params.Set("machine_translated", "include")
	}

	slog.Debug("opensubtitles show count", "imdb", imdbID, "lang", lang)

	body, err := p.doGet(ctx, "/subtitles", params)
	if err != nil {
		return 0, fmt.Errorf("show count: %w", err)
	}
	defer func() { httpx.DrainClose(body) }()

	var resp searchResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return 0, fmt.Errorf("decode show count: %w", err)
	}

	slog.Debug("opensubtitles show count result",
		"imdb", imdbID, "lang", lang, "total_count", resp.TotalCount)
	return resp.TotalCount, nil
}

// Download requests a download link from OpenSubtitles and fetches the subtitle
// file. The /download endpoint always uses the default base URL, not the VIP host.
func (p *Provider) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	if err := p.ensureToken(ctx); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	// Validate file ID is numeric to prevent JSON injection.
	fileID, err := strconv.Atoi(sub.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid file ID %q: %w", sub.ID, err)
	}

	slog.Debug("opensubtitles downloading subtitle", "file_id", fileID)

	// /download must always use the default base URL, not the VIP host
	// returned by login (VIP host is search-only).
	reqBody, err := json.Marshal(map[string]any{
		"file_id":    fileID,
		"sub_format": "srt",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal download request: %w", err)
	}

	body, err := p.doPostDownload(ctx, "/download", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("request download (file_id %d): %w", fileID, err)
	}
	defer httpx.DrainClose(body)

	var dlResp downloadResponse
	if err := json.NewDecoder(body).Decode(&dlResp); err != nil {
		return nil, fmt.Errorf("decode download response: %w", err)
	}

	if dlResp.Link == "" {
		return nil, errors.New("empty download link")
	}

	slog.Debug("opensubtitles download link received", "file_id", fileID)
	p.logQuota(&dlResp)

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

	data, err := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("read subtitle: %w", err)
	}

	if err := subtitlefile.Validate(data); err != nil {
		return nil, fmt.Errorf("opensubtitles: %w", err)
	}

	slog.Info("opensubtitles subtitle downloaded",
		"file_id", fileID, "bytes", len(data))

	return data, nil
}
