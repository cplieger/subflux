// Package subsource implements the SubSource subtitle provider.
// SubSource has a REST API at api.subsource.net. Requires an API key.
// Supports both movies and TV episodes via IMDB ID search.
// Rate limits: 60/min, 1800/hour, 7200/day.
package subsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"subflux/internal/api"
	"subflux/internal/cache"
	"subflux/internal/httputil"
	"subflux/internal/provider"
	"subflux/internal/provider/classify"
	"subflux/internal/ssrf"

	"golang.org/x/sync/errgroup"
)

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const (
	providerName  = api.ProviderNameSubSource
	baseURL       = "https://api.subsource.net/api/v1"
	matchedByIMDB = api.MatchByIMDB
	paramAPIKey   = "api_key"
)

// Factory creates a SubSource provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.APIKey == "" {
		return nil, errors.New("subsource: api_key is required")
	}
	return &Provider{
		apiKey:     ps.APIKey,
		client:     provider.NewHTTPClient(provider.HTTPTimeoutExtended),
		titleCache: cache.New[int](cache.DefaultTTL),
	}, nil
}

// Provider implements the SubSource API client.
type Provider struct {
	client     *http.Client
	titleCache *cache.Cache[int]
	apiKey     string
}

func (p *Provider) Name() api.ProviderID { return providerName }

// Search finds subtitles matching the request via IMDB ID lookup.
// Tries alternative titles if the primary title is not found.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.ImdbID == "" {
		slog.Debug("subsource: no IMDB ID, skipping")
		return nil, nil
	}

	titleID, err := p.searchTitleWithAlternatives(ctx, req)
	if err != nil {
		return nil, err
	}
	if titleID == 0 {
		slog.Debug("subsource: title not found", "imdb_id", req.ImdbID)
		return nil, nil
	}

	// Collect valid languages for concurrent search.
	type langEntry struct {
		iso string
		ss  string
	}
	var langs []langEntry
	for _, lang := range req.Languages {
		ssLang := iso2ToSubSource(lang)
		if ssLang != "" {
			langs = append(langs, langEntry{iso: lang, ss: ssLang})
		}
	}
	if len(langs) == 0 {
		return nil, nil
	}

	// Search languages concurrently with bounded concurrency.
	type langResult struct {
		err  error
		subs []api.Subtitle
	}
	perLang := make([]langResult, len(langs))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(langs))
	for i, le := range langs {
		g.Go(func() error {
			subs, queryErr := p.querySubtitles(gctx, titleID, le.ss, le.iso, req)
			if queryErr != nil {
				slog.Warn("subsource query failed", "lang", le.iso, "error", httputil.RedactSecret(queryErr, p.apiKey))
				perLang[i] = langResult{err: queryErr}
			} else {
				perLang[i] = langResult{subs: subs}
			}
			return nil
		})
	}
	_ = g.Wait()

	var results []api.Subtitle
	var lastErr error
	var anySuccess bool
	for _, lr := range perLang {
		if lr.err != nil {
			lastErr = lr.err
			continue
		}
		anySuccess = true
		results = append(results, lr.subs...)
	}

	// If no language query succeeded, propagate the error so the caller
	// can distinguish provider failure from genuine no-results.
	if !anySuccess && lastErr != nil {
		return nil, fmt.Errorf("subsource: all language queries failed: %w", lastErr)
	}

	slog.Info("subsource search complete", "results", len(results),
		"media", req.MediaLabel())
	return results, nil
}

// Download fetches the subtitle content for the given search result.
// SubSource returns archives; the subtitle file is extracted automatically.
func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("subsource: %w", err)
	}

	u, err := url.Parse(sub.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("subsource: invalid download URL: %w", err)
	}
	q := u.Query()
	q.Set(paramAPIKey, p.apiKey)
	u.RawQuery = q.Encode()
	dlURL := u.String()

	slog.Debug("subsource downloading subtitle", "id", sub.ID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "subsource download", p.apiKey)
	}
	defer resp.Body.Close()

	// Use CheckHTTPStatus for unified typed error dispatch. This honors the
	// Retry-After header on 429 (via httputil.ParseRetryAfter) and returns
	// *api.AuthError for 401/403 just like the search path.
	if statusErr := httputil.CheckHTTPStatus(resp); statusErr != nil {
		return nil, httputil.RedactSecret(statusErr, p.apiKey)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("subsource: empty response body for subtitle %s", sub.ID)
	}

	// SubSource returns archives; extract the subtitle file.
	result, err := provider.ExtractAndValidate(data, sub.Season, sub.Episode)
	if err != nil {
		return nil, fmt.Errorf("subsource: %w", err)
	}
	slog.Debug("subsource download complete", "id", sub.ID, "bytes", len(result), "archive", len(result) != len(data))
	return result, nil
}

// searchTitleWithAlternatives tries the primary title, then alternative titles.
// Rate-limit and auth errors short-circuit the loop since they won't resolve
// by trying a different title.
func (p *Provider) searchTitleWithAlternatives(ctx context.Context, req *api.SearchRequest) (int, error) {
	titleID, err := p.searchTitle(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("subsource search title: %w", err)
	}
	if titleID > 0 {
		return titleID, nil
	}

	// Try alternative titles before giving up. Track the first real error
	// separately from the loop cursor so a transient failure followed by a
	// clean "not found" doesn't silently mask the original problem.
	var firstErr error
	for _, alt := range req.AlternativeTitles {
		altReq := *req
		altReq.Title = alt
		id, err := p.searchTitle(ctx, &altReq)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// Rate-limit and auth errors won't resolve by trying another
			// title; surface them immediately so the scan engine can pause
			// or re-auth instead of burning the remaining alternatives.
			if _, isRL := errors.AsType[*api.RateLimitError](err); isRL {
				return 0, fmt.Errorf("subsource search alt title: %w", err)
			}
			if _, isAuth := errors.AsType[*api.AuthError](err); isAuth {
				return 0, fmt.Errorf("subsource search alt title: %w", err)
			}
			continue
		}
		if id > 0 {
			return id, nil
		}
	}
	if firstErr != nil {
		return 0, fmt.Errorf("subsource search alt title: %w", firstErr)
	}
	return 0, nil
}

// --- API types ---

type searchResult struct {
	Title       string  `json:"title"`
	ReleaseYear FlexInt `json:"releaseYear"`
	MovieID     int     `json:"movieId"`
}

type searchResponse struct {
	Data []searchResult `json:"data"`
}

type subtitleItem struct {
	Language     string   `json:"language"`
	Commentary   string   `json:"commentary"`
	ReleaseInfo  []string `json:"releaseInfo"`
	SubtitleID   int      `json:"subtitleId"`
	HearingImp   bool     `json:"hearingImpaired"`
	ForeignParts bool     `json:"foreignParts"`
}

type subtitleResponse struct {
	Error   string         `json:"error,omitempty"`
	Data    []subtitleItem `json:"data"`
	Success bool           `json:"success"`
}

// --- API calls ---

func (p *Provider) searchTitle(ctx context.Context, req *api.SearchRequest) (int, error) {
	cacheKey := "title:" + req.ImdbID
	return p.titleCache.GetOrFetch(cacheKey, func() (int, error) {
		return p.searchTitleUncached(ctx, req)
	})
}

func (p *Provider) searchTitleUncached(ctx context.Context, req *api.SearchRequest) (int, error) {
	params := url.Values{
		paramAPIKey:  {p.apiKey},
		"searchType": {string(matchedByIMDB)},
		"imdb":       {req.ImdbID},
	}
	if req.MediaType == api.MediaTypeEpisode && req.Season > 0 {
		params.Set("season", strconv.Itoa(req.Season))
	}

	data, err := p.doSearch(ctx, params)
	if err != nil {
		return 0, err
	}

	// If IMDB search returned nothing, try text search.
	if len(data) == 0 && req.Title != "" {
		slog.Debug("subsource: IMDB search returned no results, falling back to text search",
			"imdb_id", req.ImdbID, "title", req.Title)
		return p.searchTitleByText(ctx, req)
	}

	return matchTitle(data, req.Title, req.Year), nil
}

func (p *Provider) searchTitleByText(ctx context.Context, req *api.SearchRequest) (int, error) {
	params := url.Values{
		paramAPIKey:  {p.apiKey},
		"searchType": {"text"},
		"q":          {strings.ToLower(req.Title)},
	}
	if req.MediaType == api.MediaTypeEpisode && req.Season > 0 {
		params.Set("season", strconv.Itoa(req.Season))
	}

	data, err := p.doSearch(ctx, params)
	if err != nil {
		return 0, err
	}

	return matchTitle(data, req.Title, req.Year), nil
}

// doSearch executes a title search request and returns the decoded results.
// Transport errors are redacted to prevent api_key leakage via *url.Error.
func (p *Provider) doSearch(ctx context.Context, params url.Values) ([]searchResult, error) {
	u := baseURL + "/movies/search?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("subsource title search: %w", err), p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "subsource title search", p.apiKey)
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}

	var result searchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxSearchResponseBytes)).Decode(&result); err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("decode search: %w", err), p.apiKey)
	}
	return result.Data, nil
}

// matchTitle finds the first search result whose title contains the query
// and whose year matches (if specified). Returns 0 if no match.
func matchTitle(data []searchResult, title string, year int) int {
	lower := strings.ToLower(title)
	for _, r := range data {
		if strings.Contains(strings.ToLower(r.Title), lower) {
			if year == 0 || int(r.ReleaseYear) == year {
				return r.MovieID
			}
		}
	}
	return 0
}

func (p *Provider) querySubtitles(ctx context.Context, titleID int, ssLang, isoLang string, req *api.SearchRequest) ([]api.Subtitle, error) {
	params := url.Values{
		paramAPIKey: {p.apiKey},
		"language":  {strings.ToLower(ssLang)},
		"limit":     {"100"},
		"movieId":   {strconv.Itoa(titleID)},
	}
	if req.MediaType == api.MediaTypeEpisode {
		params.Set("seasonNumber", strconv.Itoa(req.Season))
		params.Set("episodeNumber", strconv.Itoa(req.Episode))
	}

	u := baseURL + "/subtitles?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("subsource subtitles: %w", err), p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "subsource subtitles", p.apiKey)
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}

	var result subtitleResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxListResponseBytes)).Decode(&result); err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("decode subtitles: %w", err), p.apiKey)
	}

	if !result.Success {
		// API-level failure arriving as HTTP 200 (same shape as subdl's
		// status=false path). Log at warn so operators can see partial
		// degradation in Loki instead of a silent "0 results".
		slog.Warn("subsource: API returned success=false",
			"title_id", titleID, "lang", isoLang, "error", result.Error)
		return nil, nil
	}

	return buildSubtitles(result.Data, isoLang, req.Season, req.Episode), nil
}

// buildSubtitles converts raw API subtitle items into api.Subtitle values.
// Filters out forced/foreign-parts subtitles, detects HI, and expands
// multi-release entries into individual results. Pure function.
func buildSubtitles(items []subtitleItem, isoLang string, season, episode int) []api.Subtitle {
	var subs []api.Subtitle
	for _, item := range items {
		// Skip forced subs unless explicitly requested.
		if item.ForeignParts || classify.IsForced(item.Commentary) {
			continue
		}

		hi := item.HearingImp || classify.IsHearingImpaired(item.Commentary, "")

		// Create one entry per release name so the scorer can evaluate
		// each independently. Same subtitle file, different metadata.
		releases := item.ReleaseInfo
		if len(releases) == 0 {
			releases = []string{""}
		}
		for _, rel := range releases {
			subs = append(subs, api.Subtitle{
				Provider:    providerName,
				ID:          strconv.Itoa(item.SubtitleID),
				Language:    isoLang,
				ReleaseName: rel,
				DownloadURL: baseURL + "/subtitles/" + strconv.Itoa(item.SubtitleID) + "/download",
				HearingImp:  hi,
				MatchedBy:   matchedByIMDB,
				Season:      season,
				Episode:     episode,
			})
		}
	}
	return subs
}

// FlexInt is a JSON type that unmarshals both string and number representations
// to an int value. SubSource's API returns releaseYear as either a string or number.
// Uses lenient semantics: errors default to zero (year=0 means "unknown").
type FlexInt int

// UnmarshalJSON implements json.Unmarshaler for FlexInt.
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	n, err := provider.ParseFlexInt(data)
	if err != nil {
		// Lenient: default to zero on parse errors.
		*f = 0
		return nil
	}
	*f = FlexInt(n)
	return nil
}

// --- Language mapping ---
// SubSource uses capitalized English language names.

var iso2ToSubSourceMap = map[string]string{
	"en": "English", "fr": "French", "es": "Spanish", "de": "German",
	"it": "Italian", "pt": "Portuguese", "pb": "Brazillian Portuguese",
	"nl": "Dutch", "ru": "Russian",
	"ar": "Arabic", "ja": "Japanese", "zh": "Chinese BG code", "ko": "Korean",
	"sv": "Swedish", "no": "Norwegian", "da": "Danish", "fi": "Finnish",
	"pl": "Polish", "cs": "Czech", "hu": "Hungarian", "ro": "Romanian",
	"tr": "Turkish", "el": "Greek", "he": "Hebrew", "th": "Thai",
	"vi": "Vietnamese", "id": "Indonesian", "bg": "Bulgarian",
	"hr": "Croatian", "sr": "Serbian", "sl": "Slovenian",
	"sk": "Slovak", "uk": "Ukrainian", "ca": "Catalan",
	"eu": "Basque", "fa": "Farsi_persian", "ms": "Malay",
	"sq": "Albanian", "bs": "Bosnian", "hy": "Armenian",
	"az": "Azerbaijani", "bn": "Bengali", "mk": "Macedonian",
	"hi": "Hindi", "ta": "Tamil", "te": "Telugu",
	"ur": "Urdu", "is": "Icelandic", "lt": "Lithuanian",
	"lv": "Latvian", "et": "Estonian", "sw": "Swahili",
}

func iso2ToSubSource(code string) string {
	code = classify.Alpha2FromAlpha3(code)
	if v, ok := iso2ToSubSourceMap[code]; ok {
		return v
	}
	return ""
}
