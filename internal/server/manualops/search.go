package manualops

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/sync/errgroup"
)

// ParseSearchQuery extracts search parameters from the request URL.
func ParseSearchQuery(r *http.Request) (req api.SearchRequest, lang string, mediaType api.MediaType, filePath string) {
	q := r.URL.Query()
	lang = q.Get("lang")
	if lang == "" {
		lang = "en"
	}
	mediaType = api.MediaType(q.Get("type"))
	if mediaType == "" {
		if q.Get("season") != "" && q.Get("episode") != "" {
			mediaType = api.MediaTypeEpisode
		} else {
			mediaType = api.MediaTypeMovie
		}
	}

	req = api.SearchRequest{
		Title:           q.Get("title"),
		EpisodeTitle:    q.Get("episode_title"),
		ImdbID:          q.Get("imdb"),
		TmdbID:          QueryInt(q, "tmdb"),
		ReleaseName:     q.Get("release"),
		Languages:       []string{lang},
		MediaType:       mediaType,
		Year:            QueryInt(q, "year"),
		Season:          QueryInt(q, "season"),
		Episode:         QueryInt(q, "episode"),
		SceneSeason:     QueryInt(q, "scene_season"),
		SceneEpisode:    QueryInt(q, "scene_episode"),
		AbsoluteEpisode: QueryInt(q, "absolute_episode"),
		TvdbID:          QueryInt(q, "tvdb"),
	}

	filePath = q.Get("file")
	if filePath != "" && req.ReleaseName == "" {
		req.ReleaseName = filePath
	}
	return req, lang, mediaType, filePath
}

// QueryInt parses a URL query parameter as a non-negative integer,
// returning 0 on missing, invalid, or negative values.
func QueryInt(q interface{ Get(string) string }, key string) int {
	v := q.Get(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// TryComputeHash attempts to compute the video hash for the search request.
// Logs warnings on validation or hash failures; updates req in place on success.
func TryComputeHash(ctx context.Context, ls *LiveState, req *api.SearchRequest, filePath string) {
	if filePath == "" || req.VideoHash != "" {
		return
	}
	if err := ls.Cfg.ValidatePath(ctx, filePath); err != nil {
		slog.Warn("manual search: path validation failed",
			"path", filePath, "error", err)
		return
	}
	hash, size, err := ls.Engine.HashFile(ctx, filePath)
	if err != nil {
		slog.Warn("manual search: hash computation failed",
			"path", filePath, "error", err)
		return
	}
	req.VideoHash = hash
	req.VideoSize = size
	slog.Debug("manual search: video hash computed",
		"path", filePath, "hash", hash, "size", size)
}

// BuildSearchResults converts scored results to API response format.
func BuildSearchResults(scored []api.ScoredResult, refs []api.DownloadedRef) []SearchResult {
	if len(scored) > MaxResults {
		scored = scored[:MaxResults]
	}
	onDiskSet := make(map[api.DownloadedRef]struct{}, len(refs))
	for _, r := range refs {
		onDiskSet[r] = struct{}{}
	}
	results := make([]SearchResult, len(scored))
	for i := range scored {
		sr := &scored[i]
		_, onDisk := onDiskSet[api.DownloadedRef{
			ReleaseName: sr.Sub.ReleaseName,
			Provider:    sr.Sub.Provider,
		}]
		results[i] = SearchResult{
			Provider:    sr.Sub.Provider,
			Language:    sr.Sub.Language,
			ReleaseName: sr.Sub.ReleaseName,
			Score:       sr.Score,
			Matches:     sr.Matches,
			MatchedBy:   string(sr.Sub.MatchedBy),
			HearingImp:  sr.Sub.HearingImp,
			Forced:      sr.Sub.Forced,
			SubtitleID:  sr.Sub.ID,
			OnDisk:      onDisk,
		}
	}
	return results
}

// ManualSearchResponse is the typed response from RunSearch. It deliberately
// carries no lock state: manual locks are invisible infrastructure ("a manual
// pick is never overwritten"), not a user-facing concept, so the popup has
// nothing to display about them.
type ManualSearchResponse struct {
	Results []SearchResult `json:"results"`
}

// RunSearch executes the manual search against all providers and returns
// the JSON-ready response payload.
func RunSearch(ctx context.Context, deps *SearchDeps, ls *LiveState,
	req *api.SearchRequest, lang string, mediaType api.MediaType, filePath string,
) ManualSearchResponse {
	mediaID := api.BuildMediaID(req)
	TryComputeHash(ctx, ls, req, filePath)

	// Search all providers in parallel (skip embedded — those are already on disk).
	// Each provider gets its own 15s timeout so a slow provider doesn't block others.
	const perProviderTimeout = 15 * time.Second
	type provResult struct {
		subs []api.Subtitle
	}
	results := make([]provResult, len(ls.Providers))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range ls.Providers {
		if p.Name() == api.ProviderNameEmbedded {
			continue
		}
		g.Go(func() error {
			pctx, cancel := context.WithTimeout(gctx, perProviderTimeout)
			defer cancel()
			subs, err := p.Search(pctx, req)
			if err != nil {
				slog.Warn("manual search: provider failed",
					"provider", p.Name(), "error", err)
				return nil
			}
			results[i] = provResult{subs: subs}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Warn("manual search: provider error", "error", err)
	}

	var allResults []api.Subtitle
	for _, r := range results {
		allResults = append(allResults, r.subs...)
	}

	// Score and rank.
	var scored []api.ScoredResult
	if len(allResults) > 0 {
		scored = ls.Engine.ScoreSubtitles(req, allResults)
	}

	if len(scored) > 0 {
		slog.Debug("manual search: scored results",
			"total", len(allResults), "scored", len(scored),
			"top_score", scored[0].Score,
			"top_provider", scored[0].Sub.Provider)
	} else {
		slog.Info("manual search: no results",
			"title", req.Title, "lang", lang, "media_type", mediaType)
	}

	// Check which results have files on disk via download history.
	var refs []api.DownloadedRef
	if len(scored) > 0 {
		var refsErr error
		refs, refsErr = deps.DB.DownloadedRefs(ctx, mediaType, mediaID, lang)
		if refsErr != nil {
			slog.Warn("manual search: refs lookup failed", "error", refsErr)
		}
	}

	return ManualSearchResponse{
		Results: BuildSearchResults(scored, refs),
	}
}
