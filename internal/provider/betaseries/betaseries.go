// Package betaseries implements the BetaSeries subtitle provider.
// TV shows only, uses TVDB IDs for lookup.
package betaseries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"subflux/internal/api"
	"subflux/internal/httputil"
	"subflux/internal/provider"
	"subflux/internal/ssrf"
)

const sourceSeriessub = "seriessub"

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const baseURL = "https://api.betaseries.com/"

const (
	providerName = api.ProviderNameBetaSeries
)

// Factory creates a BetaSeries provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	if ps.Token == "" {
		return nil, errors.New("betaseries: token required")
	}
	return &Provider{
		client: provider.NewHTTPClient(provider.HTTPTimeoutStandard),
		token:  ps.Token,
	}, nil
}

// Provider implements the BetaSeries subtitle API.
type Provider struct {
	client *http.Client
	token  string // API key for X-BetaSeries-Key header.
}

func (p *Provider) Name() api.ProviderID { return providerName }

func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.MediaType != api.MediaTypeEpisode {
		slog.Debug("betaseries: not an episode, skipping",
			"media_type", req.MediaType)
		return nil, nil
	}

	// BetaSeries uses TVDB IDs natively (same as Bazarr's implementation).
	// The shows/episodes endpoint accepts thetvdb_id, not imdb_id.
	if req.TvdbID <= 0 {
		slog.Debug("betaseries: no TVDB ID, skipping")
		return nil, nil
	}

	// Search by series TVDB ID + season + episode.
	searchURL := fmt.Sprintf("%sshows/episodes?thetvdb_id=%d&season=%d&episode=%d&subtitles=1&v=3.0",
		baseURL, req.TvdbID, req.Season, req.Episode)

	slog.Debug("betaseries searching", "tvdb_id", req.TvdbID,
		"season", req.Season, "episode", req.Episode)

	body, err := p.doGet(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer body.Close()

	var resp episodesResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		slog.Debug("betaseries: failed to decode response", "error", err)
		return nil, fmt.Errorf("decode: %w", err)
	}

	if len(resp.Errors) > 0 {
		slog.Warn("betaseries: API returned errors",
			"errors", resp.Errors,
			"tvdb_id", req.TvdbID,
			"season", req.Season,
			"episode", req.Episode)
		return nil, nil
	}

	var subs []subtitleEntry
	if len(resp.Episodes) > 0 && len(resp.Episodes[0].Subtitles) > 0 {
		subs = resp.Episodes[0].Subtitles
	}

	results := filterSubtitleEntries(subs, req.Languages, req.Season, req.Episode)

	slog.Info("betaseries search complete",
		"results", len(results), "media", req.MediaLabel())
	return results, nil
}

func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	// Validate download URL to prevent SSRF via malicious API responses.
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("betaseries: %w", err)
	}

	slog.Debug("betaseries downloading subtitle", "url", sub.DownloadURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.DownloadURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		slog.Debug("betaseries: subtitle not found", "url", sub.DownloadURL)
		return nil, nil
	}
	if err2 := httputil.CheckHTTPStatus(resp); err2 != nil {
		return nil, err2
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if err != nil {
		return nil, err
	}

	// Check if it's an archive and extract subtitle.
	result, err := provider.ExtractAndValidate(data, sub.Season, sub.Episode)
	if err != nil {
		return nil, fmt.Errorf("betaseries: %w", err)
	}
	slog.Debug("betaseries download complete", "id", sub.ID, "bytes", len(result), "archive", len(result) != len(data))
	return result, nil
}

// doGet performs an authenticated GET request to the BetaSeries API.
// Returns a size-limited ReadCloser for the response body. Handles the
// BetaSeries-specific 400/4001 "not found" response by returning a
// synthetic empty episodes response.
func (p *Provider) doGet(ctx context.Context, reqURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-BetaSeries-Key", p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	// BetaSeries returns 400 for "not found" (code 4001) and auth errors
	// (code 1001). Parse the error body to distinguish them.
	if resp.StatusCode == http.StatusBadRequest {
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB error response limit
		if err != nil {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return classifyBadRequest(data)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, &api.RateLimitError{
			Msg:        "rate limited (429)",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes), resp.Body}, nil
}

// classifyBadRequest parses a BetaSeries 400 error response body and returns
// the appropriate result. Returns a synthetic empty response for "not found"
// (code 4001), an AuthError for invalid API key (code 1001), or a generic
// HTTP 400 error for unrecognized codes.
func classifyBadRequest(body []byte) (io.ReadCloser, error) {
	var errResp struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &errResp) == nil && len(errResp.Errors) > 0 {
		switch errResp.Errors[0].Code {
		case 4001:
			slog.Debug("betaseries: series not found (4001)")
			return io.NopCloser(strings.NewReader(`{"episodes":[]}`)), nil
		case 1001:
			return nil, &api.AuthError{Msg: "invalid API key (1001)"}
		}
	}
	return nil, fmt.Errorf("HTTP %d", http.StatusBadRequest)
}

// filterSubtitleEntries converts raw BetaSeries subtitle entries into Subtitle
// values. Filters by requested languages (mapping BetaSeries vo/vf codes to
// ISO 639-1) and skips the sourceSeriessub source (dead links). Pure function.
func filterSubtitleEntries(entries []subtitleEntry, languages []string, season, episode int) []api.Subtitle {
	var results []api.Subtitle
	for _, sub := range entries {
		lang := betaLangToISO(sub.Language)
		if lang == "" || !slices.Contains(languages, lang) {
			continue
		}
		// Skip seriessub source (dead links).
		if sub.Source == sourceSeriessub {
			continue
		}

		results = append(results, api.Subtitle{
			Provider:    providerName,
			ID:          strconv.Itoa(sub.ID),
			Language:    lang,
			ReleaseName: sub.File,
			DownloadURL: sub.URL,
			MatchedBy:   api.MatchByTVDB,
			Season:      season,
			Episode:     episode,
		})
	}
	return results
}

// betaLangToISO converts BetaSeries language codes to ISO 639-1.
// BetaSeries uses "vo"/"vf" (version originale/française) alongside standard codes.
// Only English and French are mapped because BetaSeries is a French-language service
// and primarily hosts subtitles in these two languages.
func betaLangToISO(code string) string {
	switch strings.ToLower(code) {
	case "vo", "en":
		return "en"
	case "vf", "fr":
		return "fr"
	}
	return ""
}

type episodesResponse struct {
	Episodes []episodeData `json:"episodes"`
	Errors   []any         `json:"errors"`
}

type episodeData struct {
	Subtitles []subtitleEntry `json:"subtitles"`
}

type subtitleEntry struct {
	Language string `json:"language"`
	Source   string `json:"source"`
	File     string `json:"file"`
	URL      string `json:"url"`
	ID       int    `json:"id"`
}
