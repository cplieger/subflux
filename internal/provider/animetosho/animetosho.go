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
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/ssrf/v4"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/anidb"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"golang.org/x/sync/errgroup"
)

const (
	providerName     = subflux.ProviderNameAnimeTosho
	feedURL          = "https://feed.animetosho.org/json"
	storageURL       = "https://animetosho.org/storage/attach/"
	maxSearchEntries = 6

	statusComplete     = "complete"
	attachTypeSubtitle = "subtitle"
)

// Factory creates an AnimeTosho provider from settings.
func Factory(_ context.Context, settings map[string]any) (provider.Provider, error) {
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

// Name returns the provider identifier for AnimeTosho.
func (p *Provider) Name() subflux.ProviderID { return providerName }

// Search tries AniDB episode ID lookup first (more precise for anime), then
// falls back to title+season search.
func (p *Provider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	if req.MediaType != subflux.MediaTypeEpisode {
		slog.Debug("animetosho: not an episode, skipping",
			"media_type", req.MediaType)
		return nil, nil
	}
	if req.Title == "" {
		slog.Debug("animetosho: no title, skipping")
		return nil, nil
	}

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

	results, err := p.searchByTitle(ctx, req)
	if err != nil {
		// The provider-sweep boundary logs this with the provider name; the
		// media label and which leg failed are what it cannot reconstruct.
		return nil, fmt.Errorf("title search for %s: %w", req.MediaLabel(), err)
	}
	slog.Info("animetosho search complete",
		"results", len(results), "media", req.MediaLabel())
	return results, nil
}

// Download fetches the subtitle content for the given search result.
func (p *Provider) Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error) {
	// Validate download URL to prevent SSRF via malicious API responses.
	if err := ssrf.ValidateURL(sub.DownloadURL); err != nil {
		return nil, fmt.Errorf("animetosho: %w", err)
	}

	slog.Debug("animetosho downloading", "url", sub.DownloadURL)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, sub.DownloadURL, http.NoBody,
	)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := httpwire.CheckHTTPStatus(resp); err != nil {
		return nil, err
	}

	// cap+1 detect-and-error idiom: a payload at the cap is far more likely
	// truncated than exactly-cap-sized, and a silently truncated archive
	// would fail extraction with a confusing error.
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, httpwire.MaxDownloadBytes+1))
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(data)) > httpwire.MaxDownloadBytes {
		return nil, fmt.Errorf("animetosho: download exceeded %d bytes", httpwire.MaxDownloadBytes)
	}

	slog.Debug("animetosho download complete",
		"id", sub.ID, "bytes", len(data))

	result := archive.Decompress(data)
	if err := subtitlefile.Validate(result); err != nil {
		return nil, fmt.Errorf("animetosho: %w", err)
	}
	return result, nil
}

func (p *Provider) searchByEpisodeID(ctx context.Context, anidbEpID int, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	entries, err := p.searchEntriesByEID(ctx, anidbEpID)
	if err != nil {
		return nil, err
	}
	return p.collectSubtitles(ctx, entries, req), nil
}

func (p *Provider) searchByTitle(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	entries, err := p.searchEntries(ctx, req.Title, req.Season)
	if err != nil {
		return nil, fmt.Errorf("search entries: %w", err)
	}
	return p.collectSubtitles(ctx, entries, req), nil
}

// collectSubtitles fetches entries concurrently, bounded at maxSearchEntries,
// since AnimeTosho has no documented rate limit and entries are independent.
func (p *Provider) collectSubtitles(ctx context.Context, entries []feedEntry, req *subflux.SearchRequest) []subflux.Subtitle {
	type entryResult struct {
		title string
		subs  []subflux.Subtitle
	}

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
			subs, err := p.fetchSubtitlesForEntry(
				gctx, entry.ID, req.Languages, req.Season, req.Episode, req.AbsoluteEpisode,
			)
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

	var out []subflux.Subtitle
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

func (p *Provider) searchEntriesByEID(ctx context.Context, eid int) ([]feedEntry, error) {
	slog.Debug("animetosho searching by anidb eid", "eid", eid)

	var entries []feedEntry
	if err := p.fetchJSON(ctx, fmt.Sprintf("%s?eid=%d", feedURL, eid), &entries); err != nil {
		return nil, err
	}

	filtered := filterCompleteEntries(entries)
	slog.Debug("animetosho eid entries found",
		"eid", eid, "total", len(entries), "complete", len(filtered))
	return filtered, nil
}

// fetchJSON returns typed provider errors from CheckHTTPStatus so callers
// preserve Retry-After hints for 429 responses.
func (p *Provider) fetchJSON(ctx context.Context, reqURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := httpwire.CheckHTTPStatus(resp); err != nil {
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, httpwire.MaxJSONResponseBytes)).Decode(v)
}

type feedEntry struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	ID     int    `json:"id"`
}

// searchEntries uses a season-level query to catch both per-episode entries
// and season packs.
func (p *Provider) searchEntries(ctx context.Context,
	title string, season int,
) ([]feedEntry, error) {
	query := fmt.Sprintf("%s S%02d", title, season)
	slog.Debug("animetosho searching entries", "query", logsafe.Field(query))

	var entries []feedEntry
	if err := p.fetchJSON(ctx,
		fmt.Sprintf("%s?q=%s", feedURL, url.QueryEscape(query)), &entries); err != nil {
		return nil, err
	}

	filtered := filterCompleteEntries(entries)
	slog.Debug("animetosho entries found",
		"total", len(entries), "complete", len(filtered))
	return filtered, nil
}

func (p *Provider) fetchSubtitlesForEntry(ctx context.Context,
	entryID int, languages []string,
	season, episode, absEpisode int,
) ([]subflux.Subtitle, error) {
	slog.Debug("animetosho fetching entry subtitles", "entry_id", entryID)

	var result entryDetail
	if err := p.fetchJSON(ctx,
		fmt.Sprintf("%s?show=torrent&id=%d", feedURL, entryID), &result); err != nil {
		return nil, err
	}

	return filterAttachments(result, languages, season, episode, absEpisode), nil
}

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

func filterAttachments(result entryDetail, languages []string,
	season, episode, absEpisode int,
) []subflux.Subtitle {
	files := matchFiles(result.Files, season, episode, absEpisode)

	var subs []subflux.Subtitle
	for _, file := range files {
		for _, att := range file.Attachments {
			if sub, ok := attachmentToSubtitle(att, languages); ok {
				subs = append(subs, sub)
			}
		}
	}
	return subs
}

func attachmentToSubtitle(att entryAttachment, languages []string) (subflux.Subtitle, bool) {
	if att.Type != attachTypeSubtitle {
		return subflux.Subtitle{}, false
	}
	if att.ID <= 0 {
		return subflux.Subtitle{}, false
	}
	lang := classify.Alpha2FromAlpha3(att.Info.Lang)
	if lang == "" {
		lang = "en" // AnimeTosho defaults to English.
	}
	if lang == "pt" && strings.Contains(
		strings.ToLower(att.Info.Name), "brazil",
	) {
		lang = "pb"
	}
	if !slices.Contains(languages, lang) {
		return subflux.Subtitle{}, false
	}

	hexID := fmt.Sprintf("%08x", att.ID)
	dlURL := fmt.Sprintf("%s%s/%d.xz", storageURL, hexID, att.ID)

	return subflux.Subtitle{
		Provider:    providerName,
		ID:          strconv.Itoa(att.ID),
		Language:    lang,
		DownloadURL: dlURL,
		MatchedBy:   subflux.MatchByTitle,
	}, true
}

// matchFiles returns all files for a single-file entry; for a season pack
// (multiple files) it returns only files whose filename contains the
// matching S##E## pattern.
func matchFiles(files []entryFile, season, episode, absEpisode int) []entryFile {
	if len(files) <= 1 {
		return files
	}

	var matched []entryFile
	for _, f := range files {
		if fileMatchesEpisode(f.Filename, season, episode, absEpisode) {
			matched = append(matched, f)
		}
	}

	if len(matched) > 0 {
		return matched
	}

	// No filename matched the episode pattern (non-standard naming). Skip
	// rather than returning all files.
	slog.Debug("animetosho: no file matched target episode in pack",
		"season", season, "episode", episode,
		"files", len(files))
	return nil
}

// fileMatchesEpisode also matches standalone episode numbers for anime
// (e.g. " - 01 " or " E01"). Only falls back to standalone patterns when no
// S##E## pattern exists in the filename, probing the AIRED number first and
// then the ABSOLUTE number (batch entries dominantly name files by absolute
// number, e.g. "[Group] Show - 26.mkv" for S02E01).
func fileMatchesEpisode(filename string, season, episode, absEpisode int) bool {
	if filename == "" {
		return false
	}
	for _, m := range epmarker.Find(filename) {
		if m.Season == season && m.Episode == episode {
			return true
		}
	}
	// Presence is checked at the marker-shape level, not the parsed markers
	// above: a name with an unreadable marker still numbers its episodes
	// explicitly, so it must not fall through to absolute numbering.
	if epmarker.Present(filename) {
		return false
	}
	if standaloneNumberMatch(filename, episode) {
		return true
	}
	return absEpisode > 0 && absEpisode != episode &&
		standaloneNumberMatch(filename, absEpisode)
}

// standaloneNumberMatch matches "e##" (word boundary before it, so
// "Release01" never matches), " ## ", and " - ##".
func standaloneNumberMatch(filename string, number int) bool {
	lower := strings.ToLower(filename)
	epStr := fmt.Sprintf("e%02d", number)
	padded := fmt.Sprintf(" %02d ", number)
	dashPad := fmt.Sprintf(" - %02d", number)
	idx := strings.Index(lower, epStr)
	if idx >= 0 && (idx == 0 || lower[idx-1] < 'a' || lower[idx-1] > 'z') {
		return true
	}
	return strings.Contains(lower, padded) || strings.Contains(lower, dashPad)
}
