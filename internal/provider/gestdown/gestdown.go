// Package gestdown implements the Gestdown subtitle provider.
// Gestdown is an Addic7ed proxy with a clean REST API. TV shows only.
// API docs: https://gestdown.readme.io/reference
package gestdown

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cplieger/ssrf/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"golang.org/x/sync/errgroup"
)

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const (
	providerName = api.ProviderNameGestdown
	baseURL      = "https://api.gestdown.info"
)

// Provider implements the Gestdown API client.
type Provider struct {
	client    *http.Client
	showCache *cache.Cache[[]showResult]
	subCache  *cache.Cache[[]api.Subtitle]
}

// Factory creates a Gestdown provider. No configuration is required.
func Factory(_ context.Context, _ map[string]any) (api.Provider, error) {
	return &Provider{
		client:    provider.NewHTTPClient(provider.HTTPTimeoutStandard),
		showCache: cache.New[[]showResult](cache.DefaultTTL),
		subCache:  cache.New[[]api.Subtitle](cache.DefaultTTL),
	}, nil
}

// Name returns the provider identifier for Gestdown.
func (p *Provider) Name() api.ProviderID { return providerName }

// checkStatus maps gestdown's HTTP responses to typed errors. 423 Locked is
// gestdown's custom rate-limit signal (Addic7ed throttle) with Retry-After
// parsed into RateLimitError.RetryAfter so the scan engine's provider timeout
// manager can honor the hint. Everything else defers to httputil.CheckHTTPStatus,
// which handles 401/403/429 (also with Retry-After) and returns *HTTPStatusError
// for other 4xx/5xx.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode == http.StatusLocked {
		return &api.RateLimitError{
			Msg:        "HTTP 423: rate limited",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	return httputil.CheckHTTPStatus(resp)
}

// langEntry pairs a requested ISO language code with its Gestdown-specific
// (Addic7ed-style) language name.
type langEntry struct {
	iso  string
	gest string
}

// langResult holds one language goroutine's outcome: either the subtitles
// found for that language or the error that ended its show loop.
type langResult struct {
	err  error
	subs []api.Subtitle
}

// Search finds subtitles for TV episodes via TVDB ID lookup.
// Gestdown only supports TV shows; movies are skipped.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.MediaType != api.MediaTypeEpisode || req.TvdbID == 0 {
		slog.Debug("gestdown: not an episode or no TVDB ID, skipping")
		return nil, nil
	}

	shows, err := p.findShow(ctx, req.TvdbID)
	if err != nil {
		return nil, fmt.Errorf("gestdown find show: %w", err)
	}
	if len(shows) == 0 {
		slog.Debug("gestdown: show not found", "tvdb_id", req.TvdbID)
		return nil, nil
	}

	langs := collectLangs(req.Languages)
	if len(langs) == 0 {
		return nil, nil
	}

	// Search languages concurrently with bounded concurrency. Within each
	// language goroutine the show loop stays sequential (break-on-first-success).
	perLang := make([]langResult, len(langs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(3)
	for i, le := range langs {
		g.Go(func() error {
			perLang[i] = p.searchShowsForLang(gctx, shows, req, le.gest, le.iso)
			return nil
		})
	}
	_ = g.Wait()

	return aggregateResults(perLang, req)
}

// collectLangs maps each requested ISO language to its Gestdown language name,
// dropping languages Gestdown does not support.
func collectLangs(languages []string) []langEntry {
	var langs []langEntry
	for _, lang := range languages {
		gestLang := iso2ToGestdown(lang)
		if gestLang != "" {
			langs = append(langs, langEntry{iso: lang, gest: gestLang})
		}
	}
	return langs
}

// searchShowsForLang searches one language across the candidate shows,
// stopping at the first show that yields subtitles (or the first error).
func (p *Provider) searchShowsForLang(ctx context.Context, shows []showResult, req *api.SearchRequest, gestLang, isoLang string) langResult {
	var lr langResult
	for _, show := range shows {
		if show.ID == "" {
			continue
		}
		subs, searchErr := p.searchSeasonCached(ctx, show.ID, req.Season, req.Episode, gestLang, isoLang)
		if searchErr != nil {
			slog.Warn("gestdown search failed",
				"show", show.ID, "lang", isoLang, "error", searchErr)
			return langResult{err: searchErr}
		}
		lr = langResult{subs: subs}
		if len(subs) > 0 {
			break
		}
	}
	return lr
}

// aggregateResults flattens the per-language results. If every attempted
// language failed, the combined error is returned so the caller can
// distinguish a provider outage from a genuine no-results.
func aggregateResults(perLang []langResult, req *api.SearchRequest) ([]api.Subtitle, error) {
	var results []api.Subtitle
	attempted, failed := 0, 0
	for _, lr := range perLang {
		attempted++
		if lr.err != nil {
			failed++
			continue
		}
		results = append(results, lr.subs...)
	}

	if attempted > 0 && failed == attempted {
		slog.Warn("gestdown search complete (all calls failed)",
			"attempts", attempted, "media", req.MediaLabel())
		return nil, fmt.Errorf("gestdown: all %d attempts failed", attempted)
	}

	slog.Info("gestdown search complete", "results", len(results),
		"media", req.MediaLabel())
	return results, nil
}

// Download fetches the subtitle content for the given search result.
func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("gestdown: %w", err)
	}

	slog.Debug("gestdown downloading subtitle", "url", sub.DownloadURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.DownloadURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if sErr := checkStatus(resp); sErr != nil {
		return nil, sErr
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxSingleSubtitleBytes))
	if err != nil {
		return nil, err
	}
	if err := api.ValidateSubtitleData(data); err != nil {
		return nil, fmt.Errorf("gestdown: %w", err)
	}
	slog.Debug("gestdown download complete", "id", sub.ID, "bytes", len(data))
	return data, nil
}

// --- API types ---

type showResult struct {
	ID string `json:"id"`
}

type showsResponse struct {
	Shows []showResult `json:"shows"`
}

type subtitleResult struct {
	SubtitleID  string `json:"subtitleId"`
	Version     string `json:"version"`
	DownloadURI string `json:"downloadUri"`
	Completed   bool   `json:"completed"`
	HearingImp  bool   `json:"hearingImpaired"`
}

type seasonResponse struct {
	Episodes []seasonEpisode `json:"episodes"`
}

type seasonEpisode struct {
	Subtitles []subtitleResult `json:"subtitles"`
	Number    int              `json:"number"`
}

// --- API calls ---

func (p *Provider) findShow(ctx context.Context, tvdbID int) ([]showResult, error) {
	cacheKey := fmt.Sprintf("show:%d", tvdbID)
	return p.showCache.GetOrFetch(cacheKey, func() ([]showResult, error) {
		shows, err := p.findShowUncached(ctx, tvdbID)
		if err != nil || len(shows) == 0 {
			return nil, err
		}
		return shows, nil
	})
}

func (p *Provider) findShowUncached(ctx context.Context, tvdbID int) ([]showResult, error) {
	u := fmt.Sprintf("%s/shows/external/tvdb/%d", baseURL, tvdbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result showsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxSearchResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode shows: %w", err)
	}
	return result.Shows, nil
}

func (p *Provider) searchSeasonCached(ctx context.Context, showID string, season, episode int, gestLang, isoLang string) ([]api.Subtitle, error) {
	cacheKey := fmt.Sprintf("season:%s:%d:%s", showID, season, gestLang)

	allSubs, err := p.subCache.GetOrFetch(cacheKey, func() ([]api.Subtitle, error) {
		return p.searchSeasonRetry(ctx, showID, season, gestLang, isoLang)
	})
	if err != nil {
		return nil, err
	}
	return filterByEpisode(allSubs, episode), nil
}

// filterByEpisode returns only subtitles matching the given episode number.
func filterByEpisode(subs []api.Subtitle, episode int) []api.Subtitle {
	var results []api.Subtitle
	for i := range subs {
		if subs[i].Episode == episode {
			results = append(results, subs[i])
		}
	}
	return results
}

func (p *Provider) searchSeasonRetry(ctx context.Context, showID string, season int, gestLang, isoLang string) ([]api.Subtitle, error) {
	var subs []api.Subtitle
	err := httputil.RetryOnRateLimit(ctx, 3, 5*time.Minute, func() error {
		var searchErr error
		subs, searchErr = p.searchSeason(ctx, showID, season, gestLang, isoLang)
		return searchErr
	})
	if err != nil {
		return nil, err
	}
	return subs, nil
}

func (p *Provider) searchSeason(ctx context.Context, showID string, season int, gestLang, isoLang string) ([]api.Subtitle, error) {
	u := fmt.Sprintf("%s/shows/%s/%d/%s", baseURL, url.PathEscape(showID), season, gestLang)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}

	var result seasonResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes)).Decode(&result); err != nil { // season data limit
		return nil, fmt.Errorf("decode season: %w", err)
	}

	return buildSubtitles(result.Episodes, season, isoLang), nil
}

// convertSubtitle converts a raw subtitleResult into an api.Subtitle.
// Returns ok=false if the subtitle should be skipped (incomplete or invalid URI).
func convertSubtitle(s subtitleResult, episode, season int, isoLang string) (api.Subtitle, bool) {
	if !s.Completed {
		return api.Subtitle{}, false
	}
	if !strings.HasPrefix(s.DownloadURI, "/") {
		slog.Warn("gestdown: unexpected download URI",
			"uri", s.DownloadURI, "subtitle_id", s.SubtitleID)
		return api.Subtitle{}, false
	}
	releases := strings.Split(s.Version, ",")
	for i := range releases {
		releases[i] = strings.TrimSpace(releases[i])
	}
	return api.Subtitle{
		Provider:    providerName,
		ID:          s.SubtitleID,
		Language:    isoLang,
		ReleaseName: strings.Join(releases, " "),
		DownloadURL: baseURL + s.DownloadURI,
		HearingImp:  s.HearingImp,
		MatchedBy:   api.MatchByTVDB,
		Episode:     episode,
		Season:      season,
	}, true
}

// buildSubtitles converts raw season API response into api.Subtitle slice.
// Filters incomplete subtitles and validates download URI prefix.
func buildSubtitles(episodes []seasonEpisode, season int, isoLang string) []api.Subtitle {
	var subs []api.Subtitle
	for _, ep := range episodes {
		for _, s := range ep.Subtitles {
			if sub, ok := convertSubtitle(s, ep.Number, season, isoLang); ok {
				subs = append(subs, sub)
			}
		}
	}
	return subs
}

// --- Language mapping ---
// Gestdown uses Addic7ed-style language names.

func iso2ToGestdown(code string) string {
	return classify.LookupLangName(code, nil)
}
