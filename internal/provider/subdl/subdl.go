// Package subdl implements the SubDL subtitle provider.
// REST API at api.subdl.com/api/v1, requires API key.
// Supports movies and TV episodes via IMDB/TMDB search.
// Rate limits: 2000/day, 1,000,000 lifetime.
package subdl

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
	"subflux/internal/httputil"
	"subflux/internal/provider"
	"subflux/internal/provider/classify"
	"github.com/cplieger/ssrf"
)

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const (
	providerName = api.ProviderNameSubDL
	apiURL       = "https://api.subdl.com/api/v1"
	dlBaseURL    = "https://dl.subdl.com"
)

// Factory creates a SubDL provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.APIKey == "" {
		return nil, errors.New("subdl: api_key is required")
	}
	return &Provider{
		apiKey:    ps.APIKey,
		dlBaseURL: dlBaseURL,
		client:    provider.NewHTTPClient(provider.HTTPTimeoutExtended),
	}, nil
}

// Provider implements the SubDL API client.
type Provider struct {
	client    *http.Client
	apiKey    string
	dlBaseURL string // download base URL; defaults to dlBaseURL const
}

func (p *Provider) Name() api.ProviderID { return providerName }

// Search finds subtitles matching the request via IMDB/TMDB ID or title.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.ImdbID == "" && req.TmdbID == 0 && req.Title == "" {
		slog.Debug("subdl: no IMDB ID, TMDB ID, or title, skipping")
		return nil, nil
	}

	langs := make([]string, 0, len(req.Languages))
	for _, lang := range req.Languages {
		sdlLang := iso2ToSubDL(lang)
		if sdlLang != "" {
			langs = append(langs, sdlLang)
		}
	}
	if len(langs) == 0 {
		return nil, nil
	}

	params := buildSearchParams(p.apiKey, req, langs)

	result, err := p.doAPIRequest(ctx, params)
	if err != nil {
		return nil, err
	}

	items, statusErr := checkAPIStatus(result, req.MediaLabel())
	if statusErr != nil {
		return nil, statusErr
	}
	if items == nil {
		return nil, nil
	}

	subs := filterResults(items, req.MediaType == api.MediaTypeEpisode, inferMatchedBy(params))

	slog.Info("subdl search complete", "results", len(subs),
		"media", req.MediaLabel())
	return subs, nil
}

// inferMatchedBy returns the label describing which identifier the SubDL
// request used. Mirrors the priority order in buildSearchParams.
func inferMatchedBy(params url.Values) api.MatchMethod {
	switch {
	case params.Get("film_name") != "":
		return api.MatchByTitle
	case params.Get("tmdb_id") != "":
		return api.MatchByTMDB
	default:
		return api.MatchByIMDB
	}
}

// buildSearchParams constructs the URL query parameters for a SubDL search.
// Pure function; all branching logic for episode vs movie and ID fallback
// is contained here for independent testability.
func buildSearchParams(apiKey string, req *api.SearchRequest, langs []string) url.Values {
	params := url.Values{
		"api_key":       {apiKey},
		"languages":     {strings.Join(langs, ",")},
		"subs_per_page": {"30"},
		"comment":       {"1"},
		"releases":      {"1"},
		"hi":            {"1"},
		"bazarr":        {"1"},
	}

	if req.MediaType == api.MediaTypeEpisode {
		params.Set("type", "tv")
		params.Set("season_number", strconv.Itoa(req.Season))
		params.Set("episode_number", strconv.Itoa(req.Episode))
		if req.ImdbID != "" {
			params.Set("imdb_id", req.ImdbID)
		} else {
			params.Set("film_name", req.Title)
		}
	} else {
		params.Set("type", "movie")
		switch {
		case req.ImdbID != "":
			params.Set("imdb_id", req.ImdbID)
		case req.TmdbID > 0:
			params.Set("tmdb_id", strconv.Itoa(req.TmdbID))
		default:
			params.Set("film_name", req.Title)
		}
	}
	return params
}

// errSubDLNotFound is a sentinel error indicating the SubDL API could not
// find the requested media. Callers can use errors.Is for dispatch.
var errSubDLNotFound = errors.New("subdl: not found")

// notFoundPatterns lists error message substrings that indicate the SubDL API
// could not find the requested media. Checked case-insensitively.
var notFoundPatterns = []string{"can't find", "cannot find", "not found"}

// isNotFoundError reports whether msg matches any known not-found pattern.
func isNotFoundError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, p := range notFoundPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// checkAPIStatus interprets the SubDL API status field. Returns the subtitle
// items on success, nil on "not found", or an error for API failures.
func checkAPIStatus(result *apiResponse, label string) ([]subtitleItem, error) {
	if result.Status {
		return result.Subtitles, nil
	}
	if isNotFoundError(result.Error) {
		slog.Debug("subdl: no results", "media", label)
		return nil, nil
	}
	if result.Error != "" {
		return nil, fmt.Errorf("subdl API: %w: %s", errSubDLNotFound, result.Error)
	}
	slog.Warn("subdl: API returned status=false with no error message", "media", label)
	return nil, nil
}

// filterResults converts raw API items into Subtitle values, applying
// language, forced, and season-pack filters. Pure function.
func filterResults(items []subtitleItem, isEpisode bool, matchedBy api.MatchMethod) []api.Subtitle {
	var subs []api.Subtitle
	for i := range items {
		item := &items[i]
		// Skip season packs for episode searches.
		if isEpisode && item.EpisodeFrom != item.EpisodeEnd {
			continue
		}

		isoLang := subdlToISO2(item.Language)
		if isoLang == "" {
			continue
		}

		hi := item.HI || classify.IsHearingImpaired(item.Comment, item.Name)
		if classify.IsForced(item.Comment) {
			continue
		}

		releaseName := strings.Join(item.Releases, " ")

		subs = append(subs, api.Subtitle{
			Provider:    providerName,
			ID:          item.Name,
			Language:    isoLang,
			ReleaseName: releaseName,
			DownloadURL: item.URL,
			HearingImp:  hi,
			MatchedBy:   matchedBy,
			Season:      item.Season,
			Episode:     item.Episode,
		})
	}
	return subs
}

// Download fetches the subtitle content for the given search result.
// SubDL download URLs are relative paths; absolute URLs are rejected.
func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	// DownloadURL from the API is a relative path (e.g. "/sd/...").
	// Reject absolute URLs to prevent URL injection via crafted API responses.
	if !strings.HasPrefix(sub.DownloadURL, "/") {
		return nil, fmt.Errorf("subdl: unexpected download path: %q", sub.DownloadURL)
	}
	fullURL := p.dlBaseURL + sub.DownloadURL
	if err := ssrf.ValidateURL(fullURL); err != nil {
		return nil, fmt.Errorf("subdl: %w", err)
	}

	slog.Debug("subdl downloading subtitle", "url", fullURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "subdl download", p.apiKey)
	}
	defer resp.Body.Close()

	data, err := handleDownloadResponse(resp, sub.Season, sub.Episode)
	if err != nil {
		var rateErr *api.RateLimitError
		if errors.As(err, &rateErr) {
			slog.Warn("subdl: download rate limited", "url", fullURL)
		}
		return nil, httputil.RedactSecret(err, p.apiKey)
	}
	slog.Debug("subdl download complete", "id", sub.ID, "bytes", len(data))
	return data, nil
}

// doAPIRequest performs a GET against the SubDL subtitles endpoint and
// returns the decoded response. HTTP/decode errors are wrapped and any
// api_key embedded in transport errors is redacted. API-level failures
// (result.Status == false) are NOT handled here; caller inspects the
// returned apiResponse via checkAPIStatus.
func (p *Provider) doAPIRequest(ctx context.Context, params url.Values) (*apiResponse, error) {
	u := apiURL + "/subtitles?" + params.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("subdl search: %w", err), p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, httputil.RedactTransportError(err, "subdl search", p.apiKey)
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, httputil.RedactSecret(err, p.apiKey)
	}

	var result apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxListResponseBytes)).Decode(&result); err != nil {
		return nil, httputil.RedactSecret(fmt.Errorf("decode response: %w", err), p.apiKey)
	}
	return &result, nil
}

// handleDownloadResponse processes the HTTP response from a subtitle download.
// Detects rate limiting (429, 500 with small body) and extracts subtitles
// from archive responses. Testable without HTTP infrastructure.
func handleDownloadResponse(resp *http.Response, season, episode int) ([]byte, error) {
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &api.RateLimitError{
			Msg:        "download rate limited (429)",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	// SubDL returns 500 with a tiny body when the download limit is exceeded.
	// ContentLength is -1 when unknown; only match when explicitly small.
	if resp.StatusCode == http.StatusInternalServerError && resp.ContentLength >= 0 && resp.ContentLength < 100 {
		return nil, &api.RateLimitError{
			Msg:        "download limit exceeded (500)",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httputil.HTTPStatusError{Code: resp.StatusCode}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if err != nil {
		return nil, err
	}

	result, err := provider.ExtractAndValidate(data, season, episode)
	if err != nil {
		return nil, fmt.Errorf("subdl: %w", err)
	}
	return result, nil
}

// --- API types ---

type apiResponse struct {
	Error     string         `json:"error"`
	Subtitles []subtitleItem `json:"subtitles"`
	Status    bool           `json:"status"`
}

type subtitleItem struct {
	Name        string   `json:"name"`
	Language    string   `json:"language"`
	URL         string   `json:"url"`
	Comment     string   `json:"comment"`
	Releases    []string `json:"releases"`
	Season      int      `json:"season"`
	Episode     int      `json:"episode"`
	EpisodeFrom int      `json:"episode_from"`
	EpisodeEnd  int      `json:"episode_end"`
	HI          bool     `json:"hi"`
}

// --- Language mapping ---
// SubDL uses uppercase ISO 639-1 codes (EN, FR, AR, etc.)

var iso2ToSubDLMap = map[string]string{
	"en": "EN", "fr": "FR", "es": "ES", "de": "DE",
	"it": "IT", "pt": "PT", "pb": "BR_PT", "nl": "NL", "ru": "RU",
	"ar": "AR", "ja": "JA", "zh": "ZH", "ko": "KO",
	"sv": "SV", "no": "NO", "da": "DA", "fi": "FI",
	"pl": "PL", "cs": "CS", "hu": "HU", "ro": "RO",
	"tr": "TR", "el": "EL", "he": "HE", "th": "TH",
	"vi": "VI", "id": "ID", "bg": "BG",
	"hr": "HR", "sr": "SR", "sl": "SL",
	"sk": "SK", "uk": "UK", "fa": "FA",
	"ms": "MS", "hi": "HI", "bn": "BN",
	"ta": "TA", "te": "TE", "ur": "UR",
	"is": "IS", "lt": "LT", "lv": "LV",
	"et": "ET", "sq": "SQ", "bs": "BS",
	"mk": "MK", "ca": "CA",
}

var subdlToISO2Map = func() map[string]string {
	m := make(map[string]string, len(iso2ToSubDLMap))
	for k, v := range iso2ToSubDLMap {
		m[v] = k
	}
	return m
}()

func iso2ToSubDL(code string) string {
	code = classify.Alpha2FromAlpha3(code)
	if v, ok := iso2ToSubDLMap[code]; ok {
		return v
	}
	return ""
}

func subdlToISO2(name string) string {
	name = strings.ToUpper(name)
	if v, ok := subdlToISO2Map[name]; ok {
		return v
	}
	return ""
}
