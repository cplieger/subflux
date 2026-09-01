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

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/subflux"
)

// numbering represents one (season, episode) pair to search.
type numbering struct {
	scheme  string
	season  int
	episode int
}

// episodeNumberings returns the unique (season, episode) pairs to search.
// For movies or episodes without alternate numbering, returns a single entry.
func episodeNumberings(req *subflux.SearchRequest) []numbering {
	if req.MediaType != subflux.MediaTypeEpisode {
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
			s = req.Season
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
	// Skip for specials (season 0): absolute numbers span the full series
	// and would map onto a regular season (e.g. special 1 → S01E06).
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
func (p *Provider) searchNumbering(ctx context.Context, req *subflux.SearchRequest,
	season, episode int,
) ([]subflux.Subtitle, error) {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 60
	}

	params := p.buildSearchParams(req, season, episode)
	idSent := params.Has("imdb_id") || params.Has("parent_imdb_id") ||
		params.Has("tmdb_id")
	var results []subflux.Subtitle
	if idSent {
		r, err := p.paginatedSearch(ctx, params, req.Languages, maxResults)
		if err != nil {
			return r, err
		}
		// ID-based results are trusted by the identity filter as IMDB matches.
		for i := range r {
			if r[i].MatchedBy == matchByTitle {
				r[i].MatchedBy = matchByImdb
			}
		}
		results = r
	}

	if len(results) > 0 || req.Title == "" {
		return results, nil
	}

	// Fallback: some Sonarr/Radarr IMDB IDs don't match OpenSubtitles'
	// catalog entries (e.g. anime with multiple IMDB entries per show).
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
) ([]subflux.Subtitle, error) {
	const maxPages = 3
	var allResults []subflux.Subtitle
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
			httpx.DrainClose(body)
			warnPartial(page, err)
			return allResults, fmt.Errorf("decode page %d: %w", page, err)
		}
		httpx.DrainClose(body)

		slog.Debug("opensubtitles page results",
			"page", page, "total_pages", resp.TotalPages,
			"total_count", resp.TotalCount, "raw", len(resp.Data))

		allResults = append(allResults,
			filterSearchResults(resp.Data, languages, p.includeAI, p.includeMT)...)

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
// query-based searches. The AI/machine-translation flags send only their
// non-default direction (API defaults are asymmetric), and
// filterSearchResults re-checks both on the returned attributes.
func (p *Provider) commonSearchParams(req *subflux.SearchRequest,
	season, episode int,
) url.Values {
	params := url.Values{}
	params.Set("languages", joinOSLangs(req.Languages))
	if req.MediaType == subflux.MediaTypeEpisode {
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
	if p.includeMT {
		params.Set("machine_translated", "include")
	}
	return params
}

func (p *Provider) buildSearchParams(req *subflux.SearchRequest,
	season, episode int,
) url.Values {
	params := p.commonSearchParams(req, season, episode)

	if p.useHash && req.VideoHash != "" {
		params.Set("moviehash", req.VideoHash)
	}

	// Skip an ID that sanitizes to empty (e.g. "tt0"): an empty
	// parent_imdb_id=/imdb_id= wastes an API round trip for zero results.
	sanitized := ""
	if req.ImdbID != "" {
		sanitized = classify.SanitizeImdbID(req.ImdbID)
	}
	switch {
	case req.MediaType == subflux.MediaTypeMovie && req.TmdbID != 0:
		params.Set("tmdb_id", strconv.Itoa(req.TmdbID))
	case req.MediaType == subflux.MediaTypeEpisode && sanitized != "":
		params.Set("parent_imdb_id", sanitized)
	case sanitized != "":
		params.Set("imdb_id", sanitized)
	}

	return params
}

// buildQueryParams builds search parameters using the title as a text query
// instead of an ID, used as a fallback when ID-based search returns none.
func (p *Provider) buildQueryParams(req *subflux.SearchRequest,
	season, episode int,
) url.Values {
	params := p.commonSearchParams(req, season, episode)
	params.Set("query", req.Title)
	return params
}

// --- Result Filtering ---

// filterSearchResults converts raw API search results into Subtitle values,
// applying language and provenance filters. Both provenance flags default to
// off, so an auto-generated subtitle is dropped unless the user asked for its
// kind. Pure function.
func filterSearchResults(data []searchResult, languages []string,
	includeAI, includeMT bool,
) []subflux.Subtitle {
	var results []subflux.Subtitle
	for _, item := range data {
		if !includeAI && item.Attributes.AITranslated {
			continue
		}
		if !includeMT && item.Attributes.MachineTranslated {
			continue
		}
		if len(item.Attributes.Files) == 0 {
			continue
		}

		lang := fromOSLang(item.Attributes.Language)
		if !slices.Contains(languages, lang) {
			continue
		}

		sub := subflux.Subtitle{
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
			sub.MatchedBy = subflux.MatchByHash
		}

		results = append(results, sub)
	}
	return results
}
