// Package yifysubtitles implements the YIFY Subtitles provider.
// Movies only, uses IMDB IDs for lookup, scrapes HTML.
package yifysubtitles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/ssrf/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
)

const langBrazilianPT = "Brazilian Portuguese"

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const (
	providerName = api.ProviderNameYifySubtitles
	serverURL    = "https://yifysubtitles.ch"

	// browserUA is a browser-like User-Agent required by YIFY to avoid anti-bot blocking.
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
)

// Factory creates a YIFY Subtitles provider from settings.
func Factory(_ context.Context, _ map[string]any) (api.Provider, error) {
	return &Provider{
		client: provider.NewHTTPClient(provider.HTTPTimeoutStandard),
	}, nil
}

// Provider implements the YIFY Subtitles scraper.
type Provider struct {
	client *http.Client
}

// Name returns the provider identifier for YIFY Subtitles.
func (p *Provider) Name() api.ProviderID { return providerName }

// Search finds movie subtitles by scraping the YIFY Subtitles HTML page for the
// given IMDB ID. Only movie requests are handled; episodes are skipped.
func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.MediaType != api.MediaTypeMovie || req.ImdbID == "" {
		slog.Debug("yifysubtitles: not a movie or no IMDB ID, skipping")
		return nil, nil
	}

	// Validate IMDB ID format to prevent path injection.
	if !isValidImdbID(req.ImdbID) {
		slog.Debug("yifysubtitles: invalid IMDB ID format, skipping",
			"imdb_id", req.ImdbID)
		return nil, nil
	}

	searchURL := fmt.Sprintf("%s/movie-imdb/%s", serverURL, req.ImdbID)

	slog.Debug("yifysubtitles searching", "imdb_id", req.ImdbID)

	body, err := p.fetchPage(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("fetch movie page: %w", err)
	}

	results := p.parseResults(body, req.Languages)

	slog.Info("yifysubtitles search complete", "results", len(results), "media", req.MediaLabel())
	return results, nil
}

// Download fetches the subtitle archive for the given search result by first
// loading the subtitle detail page to extract the real download link.
func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	// Validate subtitle page URL to prevent SSRF.
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("yifysubtitles: %w", err)
	}

	// First fetch the subtitle page to get the download link.
	body, err := p.fetchPage(ctx, sub.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("fetch subtitle page: %w", err)
	}
	if body == "" {
		return nil, nil // page not found (404)
	}

	dlLink := extractDownloadLink(body)
	if dlLink == "" || !strings.HasPrefix(dlLink, "/") {
		return nil, errors.New("no download link found on page")
	}

	fullURL := serverURL + dlLink
	if ssrfErr := ssrf.ValidateURL(fullURL); ssrfErr != nil {
		return nil, fmt.Errorf("yifysubtitles download URL: %w", ssrfErr)
	}
	slog.Debug("yifysubtitles downloading", "url", fullURL)

	data, err := p.fetchDownload(ctx, fullURL, sub.DownloadURL)
	if err != nil {
		return nil, err
	}

	// Extract from archive.
	result, err := provider.ExtractAndValidate(data, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("yifysubtitles: %w", err)
	}
	slog.Debug("yifysubtitles download complete", "id", sub.ID, "bytes", len(result), "archive", len(result) != len(data))
	return result, nil
}

// fetchDownload retrieves raw bytes from a download URL with browser-like
// headers. Body is capped at 10 MB.
func (p *Provider) fetchDownload(ctx context.Context, dlURL, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", referer)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &api.RateLimitError{
			Msg:        "rate limited (429)",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
}

// fetchPage retrieves an HTML page from YIFY Subtitles with browser-like
// headers. Returns empty string (not error) for 404 responses. Body is
// capped at 2 MB.
func (p *Provider) fetchPage(ctx context.Context, pageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", serverURL)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		slog.Debug("yifysubtitles: page not found", "url", pageURL)
		return "", nil
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &api.RateLimitError{
			Msg:        "rate limited (429)",
			RetryAfter: httputil.ParseRetryAfter(resp),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB HTML page limit
	if err != nil {
		return "", err
	}
	return string(data), nil
}

var (
	rowRe      = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	tdRe       = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	hrefRe     = regexp.MustCompile(`href="([^"]+)"`)
	hiRe       = regexp.MustCompile(`class="hi-subtitle"`)
	dlBtnRe    = regexp.MustCompile(`class="[^"]*download-subtitle[^"]*"[^>]*href="([^"]+)"`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
)

func (p *Provider) parseResults(html string, languages []string) []api.Subtitle {
	if html == "" {
		return nil
	}
	var results []api.Subtitle
	for _, row := range rowRe.FindAllStringSubmatch(html, -1) {
		if sub, ok := p.parseRow(row[1], languages); ok {
			results = append(results, sub)
		}
	}
	return results
}

// parseRow extracts a single subtitle result from a table row.
// Returns false if the row should be skipped (wrong language, missing link, etc.).
func (p *Provider) parseRow(rowHTML string, languages []string) (api.Subtitle, bool) {
	tds := tdRe.FindAllStringSubmatch(rowHTML, -1)
	if len(tds) < 5 {
		return api.Subtitle{}, false
	}

	ratingStr := strings.TrimSpace(tagStripRe.ReplaceAllString(tds[0][1], ""))
	var rating int
	if n, atoiErr := strconv.Atoi(ratingStr); atoiErr == nil {
		rating = n
	}
	subLang := strings.TrimSpace(tagStripRe.ReplaceAllString(tds[1][1], ""))
	release := strings.TrimSpace(tagStripRe.ReplaceAllString(tds[2][1], ""))
	release = strings.TrimPrefix(release, "subtitle ")
	hi := hiRe.MatchString(tds[3][1])

	lang := yifyLangToISO(subLang)
	if lang == "" || !slices.Contains(languages, lang) {
		return api.Subtitle{}, false
	}

	linkMatch := hrefRe.FindStringSubmatch(tds[2][1])
	if len(linkMatch) < 2 || !strings.HasPrefix(linkMatch[1], "/") {
		return api.Subtitle{}, false
	}
	pageLink := serverURL + linkMatch[1]

	return api.Subtitle{
		Provider:    providerName,
		ID:          pageLink,
		Language:    lang,
		ReleaseName: release,
		DownloadURL: pageLink,
		Score:       rating,
		HearingImp:  hi,
		MatchedBy:   api.MatchByIMDB,
	}, true
}

func extractDownloadLink(html string) string {
	match := dlBtnRe.FindStringSubmatch(html)
	if match == nil {
		return ""
	}
	return match[1]
}

// yifyLangOverrides contains provider-specific name→code mappings that
// differ from the canonical LangRegistry reverse mapping.
var yifyLangOverrides = map[string]string{
	langBrazilianPT: "pb",
}

func yifyLangToISO(name string) string {
	return classify.LookupLangCode(name, yifyLangOverrides)
}

// imdbIDRe matches valid IMDB IDs (e.g. "tt1234567").
var imdbIDRe = regexp.MustCompile(`^tt\d{1,10}$`)

// isValidImdbID checks that the ID matches the expected IMDB format.
func isValidImdbID(id string) bool {
	return imdbIDRe.MatchString(id)
}
