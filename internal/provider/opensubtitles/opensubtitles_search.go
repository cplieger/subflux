package opensubtitles

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/provider/classify"
)

// numbering represents one (season, episode) pair to search.
type numbering struct {
	scheme  string // for logging: "aired", "scene", "absolute"
	season  int
	episode int
}

// episodeNumberings returns the unique (season, episode) pairs to search.
// For movies or episodes without alternate numbering, returns a single entry.
func episodeNumberings(req *api.SearchRequest) []numbering {
	if req.MediaType != api.MediaTypeEpisode {
		return []numbering{{scheme: schemeAired, season: req.Season, episode: req.Episode}}
	}

	type pair struct{ s, e int }
	seen := make(map[pair]bool)
	var out []numbering

	add := func(s, e int, scheme string) {
		if e <= 0 {
			return
		}
		if s <= 0 {
			s = req.Season // inherit aired season when provider doesn't specify
		}
		p := pair{s, e}
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, numbering{scheme: scheme, season: s, episode: e})
	}

	add(req.Season, req.Episode, schemeAired)
	add(req.SceneSeason, req.SceneEpisode, "scene")
	// Absolute episode uses season 1 if no scene season is set.
	// Skip for specials (season 0): absolute numbers span the full series
	// and map to regular-season episodes on OpenSubtitles, producing
	// wrong matches (e.g. special 1 with absolute 6 → S01E06).
	if req.Season != 0 {
		absSeason := req.SceneSeason
		if absSeason <= 0 {
			absSeason = 1
		}
		add(absSeason, req.AbsoluteEpisode, "absolute")
	}

	return out
}

// searchNumbering runs a paginated search for a specific (season, episode) pair.
func (p *Provider) searchNumbering(ctx context.Context, req *api.SearchRequest,
	season, episode int,
) ([]api.Subtitle, error) {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 60
	}

	// Try ID-based search first.
	params := p.buildSearchParams(req, season, episode)
	idSent := params.Has("imdb_id") || params.Has("parent_imdb_id") ||
		params.Has("tmdb_id")
	var results []api.Subtitle
	if idSent {
		r, err := p.paginatedSearch(ctx, params, req.Languages, maxResults)
		if err != nil {
			return r, err
		}
		// Mark ID-based results so the identity filter trusts them.
		for i := range r {
			if r[i].MatchedBy == matchByTitle {
				r[i].MatchedBy = matchByImdb
			}
		}
		results = r
	}

	// If ID-based search returned results, or we have no title to fall back
	// to, return what we have.
	if len(results) > 0 || req.Title == "" {
		return results, nil
	}

	// Fallback: query-based search using title. This handles cases where
	// Sonarr/Radarr IMDB IDs don't match OpenSubtitles' catalog entries
	// (e.g. anime with multiple IMDB entries for the same show).
	slog.Info("opensubtitles: ID search returned 0 results, retrying with query",
		"title", req.Title, "season", season, "episode", episode)
	params = p.buildQueryParams(req, season, episode)
	qResults, err := p.paginatedSearch(ctx, params, req.Languages, maxResults)
	if err != nil {
		return qResults, err
	}
	slog.Debug("opensubtitles query fallback results",
		"title", req.Title, "season", season, "episode", episode,
		"results", len(qResults))
	return qResults, nil
}

// paginatedSearch runs a paginated search with the given parameters.
func (p *Provider) paginatedSearch(ctx context.Context, params url.Values,
	languages []string, maxResults int,
) ([]api.Subtitle, error) {
	const maxPages = 3
	var allResults []api.Subtitle
	warnPartial := func(page int, err error) {
		if len(allResults) > 0 {
			slog.Warn("opensubtitles: returning partial results",
				"page", page, "results_so_far", len(allResults),
				"error", err)
		}
	}
	for page := 1; page <= maxPages; page++ {
		if page > 1 {
			params.Set("page", strconv.Itoa(page))
		}

		slog.Debug("opensubtitles search",
			"languages", params.Get("languages"),
			"imdb_id", params.Get("imdb_id"),
			"parent_imdb_id", params.Get("parent_imdb_id"),
			"tmdb_id", params.Get("tmdb_id"),
			"season", params.Get("season_number"),
			"episode", params.Get("episode_number"),
			"query", params.Get("query"),
			"page", page)

		body, err := p.doGet(ctx, "/subtitles", params)
		if err != nil {
			warnPartial(page, err)
			return allResults, fmt.Errorf("search page %d: %w", page, err)
		}

		var resp searchResponse
		if err := json.NewDecoder(body).Decode(&resp); err != nil {
			httputil.DrainClose(body)
			warnPartial(page, err)
			return allResults, fmt.Errorf("decode page %d: %w", page, err)
		}
		httputil.DrainClose(body)

		slog.Debug("opensubtitles page results",
			"page", page, "total_pages", resp.TotalPages,
			"total_count", resp.TotalCount, "raw", len(resp.Data))

		allResults = append(allResults,
			filterSearchResults(resp.Data, languages, p.includeAI)...)

		if len(allResults) >= maxResults || page >= resp.TotalPages {
			break
		}
	}
	return allResults, nil
}

// joinOSLangs maps language codes to OpenSubtitles format, sorts, and joins.
func joinOSLangs(langs []string) string {
	mapped := make([]string, len(langs))
	for i, l := range langs {
		mapped[i] = toOSLang(l)
	}
	slices.Sort(mapped)
	return strings.Join(mapped, ",")
}

// commonSearchParams returns the parameters shared by both ID-based and
// query-based searches: languages, season/episode numbers, and AI filter.
func (p *Provider) commonSearchParams(req *api.SearchRequest,
	season, episode int,
) url.Values {
	params := url.Values{}
	params.Set("languages", joinOSLangs(req.Languages))
	if req.MediaType == api.MediaTypeEpisode {
		if episode > 0 {
			params.Set("episode_number", strconv.Itoa(episode))
		}
		if season > 0 {
			params.Set("season_number", strconv.Itoa(season))
		}
	}
	if !p.includeAI {
		params.Set("ai_translated", "exclude")
	}
	return params
}

func (p *Provider) buildSearchParams(req *api.SearchRequest,
	season, episode int,
) url.Values {
	params := p.commonSearchParams(req, season, episode)

	// Hash.
	if p.useHash && req.VideoHash != "" {
		params.Set("moviehash", req.VideoHash)
	}

	// Prefer TMDB ID for movies, IMDB ID as fallback. Sanitize once and
	// skip the ID entirely when the input normalizes to an empty string
	// (e.g. "tt0", "tt00000") — sending an empty parent_imdb_id= or
	// imdb_id= costs an API round trip that always returns zero results.
	sanitized := ""
	if req.ImdbID != "" {
		sanitized = classify.SanitizeImdbID(req.ImdbID)
	}
	switch {
	case req.MediaType == api.MediaTypeMovie && req.TmdbID != 0:
		params.Set("tmdb_id", strconv.Itoa(req.TmdbID))
	case req.MediaType == api.MediaTypeEpisode && sanitized != "":
		// For episodes, use parent_imdb_id (series IMDB ID).
		params.Set("parent_imdb_id", sanitized)
	case sanitized != "":
		// For movies without a TMDB ID, use imdb_id.
		params.Set("imdb_id", sanitized)
	}

	return params
}

// buildQueryParams builds search parameters using the title as a text query
// instead of an ID. Used as a fallback when ID-based search returns no results
// (common when Sonarr/Radarr metadata IDs don't match OpenSubtitles' catalog).
func (p *Provider) buildQueryParams(req *api.SearchRequest,
	season, episode int,
) url.Values {
	params := p.commonSearchParams(req, season, episode)
	params.Set("query", req.Title)
	return params
}

// --- Result Filtering ---

// filterSearchResults converts raw API search results into Subtitle values,
// applying language, AI, and machine-translation filters. Pure function.
func filterSearchResults(data []searchResult, languages []string, includeAI bool) []api.Subtitle {
	var results []api.Subtitle
	for _, item := range data {
		if !includeAI && item.Attributes.AITranslated {
			continue
		}
		if item.Attributes.MachineTranslated {
			continue
		}
		if len(item.Attributes.Files) == 0 {
			continue
		}

		lang := fromOSLang(item.Attributes.Language)
		if !slices.Contains(languages, lang) {
			continue
		}

		sub := api.Subtitle{
			Provider:    providerName,
			ID:          strconv.Itoa(item.Attributes.Files[0].FileID),
			Language:    lang,
			ReleaseName: item.Attributes.Release,
			HearingImp:  item.Attributes.HearingImpaired,
			Forced:      item.Attributes.ForeignPartsOnly && !item.Attributes.HearingImpaired,
			MatchedBy:   matchByTitle,
			Title:       item.Attributes.FeatureDetails.Title,
			Year:        item.Attributes.FeatureDetails.Year,
			Season:      item.Attributes.FeatureDetails.SeasonNumber,
			Episode:     item.Attributes.FeatureDetails.EpisodeNumber,
		}

		if item.Attributes.MoviehashMatch {
			sub.MatchedBy = api.MatchByHash
		}

		results = append(results, sub)
	}
	return results
}
