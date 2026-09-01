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

	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/ssrf/v4"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

const sourceSeriessub = "seriessub"

const baseURL = "https://api.betaseries.com/"

const (
	providerName = subflux.ProviderNameBetaSeries
)

// Factory creates a BetaSeries provider from settings.
func Factory(_ context.Context, settings map[string]any) (provider.Provider, error) {
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

// Name returns the provider identifier for BetaSeries.
func (p *Provider) Name() subflux.ProviderID { return providerName }

// Search queries BetaSeries for TV episode subtitles using the TVDB ID.
// Only episode requests are handled; movies are skipped.
func (p *Provider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	if req.MediaType != subflux.MediaTypeEpisode {
		slog.Debug("betaseries: not an episode, skipping",
			"media_type", req.MediaType)
		return nil, nil
	}

	// BetaSeries uses TVDB IDs natively; the shows/episodes endpoint accepts
	// thetvdb_id, not imdb_id.
	if req.TvdbID <= 0 {
		slog.Debug("betaseries: no TVDB ID, skipping")
		return nil, nil
	}

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
			"errors", runesafe.SanitizeSingleLineBounded(fmt.Sprint(resp.Errors), 256),
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

// Download fetches the subtitle content for the given search result.
func (p *Provider) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	// Validates against SSRF via a malicious API response supplying an internal URL.
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
		return nil, fmt.Errorf("betaseries: %w: %s", subflux.ErrSubtitleAbsent, sub.ID)
	}
	if err2 := httpwire.CheckHTTPStatus(resp); err2 != nil {
		return nil, err2
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxDownloadBytes))
	if err != nil {
		return nil, err
	}

	result, err := provider.ExtractAndValidate(data, provider.TargetOf(sub))
	if err != nil {
		return nil, fmt.Errorf("betaseries: %w", err)
	}
	slog.Debug("betaseries download complete", "id", sub.ID, "bytes", len(result), "archive", len(result) != len(data))
	return result, nil
}

// doGet returns a synthetic empty episodes response for BetaSeries' 400/4001
// "not found" answer rather than an error, so Search sees zero results.
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
	// BetaSeries returns HTTP 400 for both "not found" (code 4001) and auth
	// errors (code 1001); the error body's code distinguishes them.
	if resp.StatusCode == http.StatusBadRequest {
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxErrorBodyBytes))
		if err != nil {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return classifyBadRequest(data)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, &subflux.RateLimitError{
			Msg:        "rate limited (429)",
			RetryAfter: httpwire.ParseRetryAfter(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, httpwire.MaxJSONResponseBytes), resp.Body}, nil
}

// classifyBadRequest returns a synthetic empty response for code 4001
// ("not found"), an AuthError for code 1001 (invalid API key), or a
// generic HTTP 400 error for any other code.
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
			return nil, &subflux.AuthError{Msg: "invalid API key (1001)"}
		}
	}
	return nil, fmt.Errorf("HTTP %d", http.StatusBadRequest)
}

func filterSubtitleEntries(entries []subtitleEntry, languages []string, season, episode int) []subflux.Subtitle {
	var results []subflux.Subtitle
	for _, sub := range entries {
		lang := betaLangToISO(sub.Language)
		if lang == "" || !slices.Contains(languages, lang) {
			continue
		}
		// seriessub source entries are dead links.
		if sub.Source == sourceSeriessub {
			continue
		}

		results = append(results, subflux.Subtitle{
			Provider:    providerName,
			ID:          strconv.Itoa(sub.ID),
			Language:    lang,
			ReleaseName: sub.File,
			DownloadURL: sub.URL,
			MatchedBy:   subflux.MatchByTVDB,
			Season:      season,
			Episode:     episode,
		})
	}
	return results
}

// betaLangToISO maps only English and French: BetaSeries is a French-language
// service and primarily hosts subtitles in these two languages.
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
