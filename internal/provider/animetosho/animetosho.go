// Package animetosho implements the AnimeTosho subtitle provider.
// Anime episodes only, uses AniDB episode IDs.
package animetosho

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"subflux/internal/api"
	"subflux/internal/httputil"
	"subflux/internal/provider"
	"subflux/internal/provider/anidb"
	"subflux/internal/provider/archive"
	"subflux/internal/provider/classify"
	"github.com/cplieger/ssrf"
)

// Compile-time assertion that Provider implements api.Provider.
var _ api.Provider = (*Provider)(nil)

const (
	providerName     = api.ProviderNameAnimeTosho
	feedURL          = "https://feed.animetosho.org/json"
	storageURL       = "https://animetosho.org/storage/attach/"
	maxSearchEntries = 6 // Max torrent entries to check for subtitles.

	statusComplete     = "complete"
	attachTypeSubtitle = "subtitle"
)

// Factory creates an AnimeTosho provider from settings.
func Factory(_ context.Context, settings map[string]any) (api.Provider, error) {
	ps := provider.FromMap(settings)
	anidbKey, _ := ps.Custom[string(provider.KeyAniDBClientKey)].(string)
	if anidbKey == "" {
		slog.Debug("animetosho: no anidb_client_key, episode ID resolution disabled")
	}
	return &Provider{
		client:      provider.NewHTTPClient(provider.HTTPTimeoutStandard),
		anidbMapper: anidb.NewMapper(anidbKey),
	}, nil
}

// Provider implements the AnimeTosho subtitle API.
type Provider struct {
	client      *http.Client
	anidbMapper *anidb.Mapper
}

func (p *Provider) Name() api.ProviderID { return providerName }

func (p *Provider) Search(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	if req.MediaType != api.MediaTypeEpisode {
		slog.Debug("animetosho: not an episode, skipping",
			"media_type", req.MediaType)
		return nil, nil
	}
	if req.Title == "" {
		slog.Debug("animetosho: no title, skipping")
		return nil, nil
	}

	// Try AniDB episode ID search first (more precise for anime).
	if req.TvdbID > 0 {
		result := p.anidbMapper.Resolve(ctx, req.TvdbID, req.Season, req.Episode)
		if result != nil && result.AniDBEpisodeID > 0 {
			slog.Debug("animetosho: using AniDB episode ID",
				"tvdb_id", req.TvdbID, "anidb_ep_id", result.AniDBEpisodeID)
			subs, err := p.searchByEpisodeID(ctx, result.AniDBEpisodeID, req)
			if err == nil && len(subs) > 0 {
				slog.Info("animetosho search complete (anidb)",
					"results", len(subs), "media", req.MediaLabel())
				return subs, nil
			}
			if err != nil {
				slog.Warn("animetosho: AniDB search failed, falling back to title",
					"error", err)
			} else {
				slog.Debug("animetosho: AniDB search returned no results, falling back to title",
					"anidb_ep_id", result.AniDBEpisodeID)
			}
		}
	}

	// Fallback: title + season search.
	results, err := p.searchByTitle(ctx, req)
	if err != nil {
		slog.Warn("animetosho title search failed",
			"error", err, "media", req.MediaLabel())
		return nil, err
	}
	slog.Info("animetosho search complete",
		"results", len(results), "media", req.MediaLabel())
	return results, nil
}

func (p *Provider) Download(ctx context.Context, sub *api.Subtitle) ([]byte, error) {
	// Validate download URL to prevent SSRF via malicious API responses.
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("animetosho: %w", err)
	}

	slog.Debug("animetosho downloading", "url", sub.DownloadURL)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, sub.DownloadURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return nil, err
	}

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, httputil.MaxDownloadBytes))
	if readErr != nil {
		return nil, readErr
	}

	slog.Debug("animetosho download complete",
		"id", sub.ID, "bytes", len(data))

	result := archive.Decompress(data)
	if err := api.ValidateSubtitleData(result); err != nil {
		return nil, fmt.Errorf("animetosho: %w", err)
	}
	return result, nil
}

func (p *Provider) searchByEpisodeID(ctx context.Context, anidbEpID int, req *api.SearchRequest) ([]api.Subtitle, error) {
	entries, err := p.searchEntriesByEID(ctx, anidbEpID)
	if err != nil {
		return nil, err
	}
	return p.collectSubtitles(ctx, entries, req), nil
}

func (p *Provider) searchByTitle(ctx context.Context, req *api.SearchRequest) ([]api.Subtitle, error) {
	entries, err := p.searchEntries(ctx, req.Title, req.Season)
	if err != nil {
		return nil, fmt.Errorf("search entries: %w", err)
	}
	return p.collectSubtitles(ctx, entries, req), nil
}

// collectSubtitles fetches and deduplicates subtitles from a set of feed
// entries. Shared by searchByEpisodeID and searchByTitle.
// Fetches entry details concurrently (bounded at maxSearchEntries) since
// AnimeTosho has no documented rate limit and entries are independent.
func (p *Provider) collectSubtitles(ctx context.Context, entries []feedEntry, req *api.SearchRequest) []api.Subtitle {
	type entryResult struct {
		title string
		subs  []api.Subtitle
	}

	// Fetch all entries concurrently with bounded concurrency.
	results := make([]entryResult, len(entries))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxSearchEntries)

	for idx, entry := range entries {
		if entry.ID <= 0 {
			slog.Debug("animetosho: skipping entry with invalid ID",
				"entry_id", entry.ID, "title", entry.Title)
			continue
		}
		g.Go(func() error {
			subs, err := p.getSubtitlesForEntry(
				gctx, entry.ID, req.Languages, req.Season, req.Episode)
			if err != nil {
				slog.Warn("animetosho: failed to get subs for entry",
					"entry_id", entry.ID, "error", err)
				return nil // non-fatal: skip this entry
			}
			results[idx] = entryResult{subs: subs, title: entry.Title}
			return nil
		})
	}
	_ = g.Wait()

	// Collect and deduplicate results in original order.
	var out []api.Subtitle
	seen := make(map[string]bool)
	for _, r := range results {
		for i := range r.subs {
			r.subs[i].ReleaseName = r.title
			r.subs[i].Season = req.Season
			r.subs[i].Episode = req.Episode
			if !seen[r.subs[i].ID] {
				seen[r.subs[i].ID] = true
				out = append(out, r.subs[i])
			}
		}
	}
	slog.Debug("animetosho: collected subtitles from entries",
		"entries_checked", len(entries), "results", len(out))
	return out
}

// searchEntriesByEID queries AnimeTosho by AniDB episode ID.
func (p *Provider) searchEntriesByEID(ctx context.Context, eid int) ([]feedEntry, error) {
	slog.Debug("animetosho searching by anidb eid", "eid", eid)

	var entries []feedEntry
	if err := p.getJSON(ctx, fmt.Sprintf("%s?eid=%d", feedURL, eid), &entries); err != nil {
		return nil, err
	}

	filtered := filterCompleteEntries(entries)
	slog.Debug("animetosho eid entries found",
		"eid", eid, "total", len(entries), "complete", len(filtered))
	return filtered, nil
}

// getJSON performs a GET request and decodes the response body (capped at
// 5 MB) into v. Returns typed provider errors from CheckHTTPStatus so
// callers preserve Retry-After hints for 429 responses. Callers own any
// structured debug logging around the request.
func (p *Provider) getJSON(ctx context.Context, reqURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := httputil.CheckHTTPStatus(resp); err != nil {
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, httputil.MaxJSONResponseBytes)).Decode(v)
}

type feedEntry struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	ID     int    `json:"id"`
}

// searchEntries queries the AnimeTosho feed API for torrent entries matching
// the given title and season. Uses a season-level query to catch both
// per-episode entries and season packs. Returns only complete entries,
// capped at maxSearchEntries.
func (p *Provider) searchEntries(ctx context.Context,
	title string, season int) ([]feedEntry, error) {

	query := fmt.Sprintf("%s S%02d", title, season)
	slog.Debug("animetosho searching entries", "query", query)

	var entries []feedEntry
	if err := p.getJSON(ctx,
		fmt.Sprintf("%s?q=%s", feedURL, url.QueryEscape(query)), &entries); err != nil {
		return nil, err
	}

	filtered := filterCompleteEntries(entries)
	slog.Debug("animetosho entries found",
		"total", len(entries), "complete", len(filtered))
	return filtered, nil
}

// getSubtitlesForEntry fetches the detail page for a single torrent entry
// and extracts subtitle attachments matching the requested languages.
// For season packs (multiple files), only the file matching the target
// episode is used.
func (p *Provider) getSubtitlesForEntry(ctx context.Context,
	entryID int, languages []string,
	season, episode int) ([]api.Subtitle, error) {

	slog.Debug("animetosho fetching entry subtitles", "entry_id", entryID)

	var result entryDetail
	if err := p.getJSON(ctx,
		fmt.Sprintf("%s?show=torrent&id=%d", feedURL, entryID), &result); err != nil {
		return nil, err
	}

	return filterAttachments(result, languages, season, episode), nil
}

// entryDetail holds the JSON structure returned by the AnimeTosho entry API.
type entryDetail struct {
	Files []entryFile `json:"files"`
}

type entryFile struct {
	Filename    string            `json:"filename"`
	Attachments []entryAttachment `json:"attachments"`
}

type entryAttachment struct {
	Info attachmentInfo `json:"info"`
	Type string         `json:"type"`
	ID   int            `json:"id"`
}

type attachmentInfo struct {
	Lang string `json:"lang"`
	Name string `json:"name"`
}

// episodeRe matches S01E01 patterns in filenames (case-insensitive).
var episodeRe = regexp.MustCompile(`(?i)S(\d+)E(\d+)`)

// filterCompleteEntries returns entries with status "complete", capped at
// maxSearchEntries. Pure function extracted from searchEntries for
// testability.
func filterCompleteEntries(entries []feedEntry) []feedEntry {
	var filtered []feedEntry
	for _, e := range entries {
		if e.Status == statusComplete {
			filtered = append(filtered, e)
			if len(filtered) >= maxSearchEntries {
				break
			}
		}
	}
	return filtered
}

// filterAttachments converts raw AnimeTosho entry data into Subtitle values,
// applying type, ID, language, and episode filters. For entries with multiple
// files (season packs), only the file matching the target season+episode is
// used. For single-file entries, all subtitle attachments are returned.
// Pure function extracted from getSubtitlesForEntry for testability.
func filterAttachments(result entryDetail, languages []string,
	season, episode int) []api.Subtitle {

	files := matchFiles(result.Files, season, episode)

	var subs []api.Subtitle
	for _, file := range files {
		for _, att := range file.Attachments {
			if att.Type != attachTypeSubtitle {
				continue
			}
			if att.ID <= 0 {
				continue
			}
			lang := classify.Alpha2FromAlpha3(att.Info.Lang)
			if lang == "" {
				lang = "en" // AnimeTosho defaults to English.
			}
			// Detect Brazilian Portuguese from subtitle name.
			if lang == "pt" && strings.Contains(
				strings.ToLower(att.Info.Name), "brazil") {
				lang = "pb"
			}
			if !slices.Contains(languages, lang) {
				continue
			}

			hexID := fmt.Sprintf("%08x", att.ID)
			dlURL := fmt.Sprintf(
				"%s%s/%d.xz", storageURL, hexID, att.ID)

			subs = append(subs, api.Subtitle{
				Provider:    providerName,
				ID:          strconv.Itoa(att.ID),
				Language:    lang,
				DownloadURL: dlURL,
				MatchedBy:   api.MatchByTitle,
			})
		}
	}
	return subs
}

// matchFiles returns the files from an entry that match the target episode.
// For single-file entries (per-episode releases), returns all files.
// For multi-file entries (season packs), returns only files whose filename
// contains the matching S##E## pattern.
func matchFiles(files []entryFile, season, episode int) []entryFile {
	if len(files) <= 1 {
		return files
	}

	// Multi-file entry: filter to the matching episode.
	var matched []entryFile
	for _, f := range files {
		if fileMatchesEpisode(f.Filename, season, episode) {
			matched = append(matched, f)
		}
	}

	if len(matched) > 0 {
		return matched
	}

	// No filename matched the episode pattern. This can happen with
	// non-standard naming. Skip rather than returning all files.
	slog.Debug("animetosho: no file matched target episode in pack",
		"season", season, "episode", episode,
		"files", len(files))
	return nil
}

// fileMatchesEpisode checks if a filename contains a S##E## pattern
// matching the target season and episode. Also matches standalone episode
// numbers for anime (e.g. " - 01 " or " E01"). The e## pattern requires
// a non-letter character before it to avoid false positives inside words
// like "Release01". Only falls back to absolute patterns when no S##E##
// pattern exists in the filename.
func fileMatchesEpisode(filename string, season, episode int) bool {
	if filename == "" {
		return false
	}
	matches := episodeRe.FindAllStringSubmatch(filename, -1)
	for _, m := range matches {
		s, sErr := strconv.Atoi(m[1])
		e, eErr := strconv.Atoi(m[2])
		if sErr != nil || eErr != nil {
			continue
		}
		if s == season && e == episode {
			return true
		}
	}
	// Also try matching " - EP " or " - NN " patterns common in anime.
	// Only match if the entry has no S##E## pattern at all (pure absolute).
	if len(matches) == 0 {
		lower := strings.ToLower(filename)
		epStr := fmt.Sprintf("e%02d", episode)
		padded := fmt.Sprintf(" %02d ", episode)
		dashPad := fmt.Sprintf(" - %02d", episode)
		// Require word boundary before "e##" to avoid matching inside words
		// like "Release01" or "Premiere08".
		idx := strings.Index(lower, epStr)
		epMatch := idx >= 0 && (idx == 0 || lower[idx-1] < 'a' || lower[idx-1] > 'z')
		if epMatch ||
			strings.Contains(lower, padded) ||
			strings.Contains(lower, dashPad) {
			return true
		}
	}
	return false
}
