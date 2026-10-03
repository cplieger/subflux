package manualops

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/subflux"
)

// ParseSearchQuery extracts search parameters from the request URL. The
// optional media_id parameter is the ARR internal ID (Radarr movie ID /
// Sonarr series ID): with season/episode it forms the MediaRef the handler
// resolves server-side for hash computation.
func ParseSearchQuery(r *http.Request) (req subflux.SearchRequest, lang string, mediaType subflux.MediaType, arrID int) {
	q := r.URL.Query()
	lang = q.Get("lang")
	if lang == "" {
		lang = "en"
	}
	mediaType = subflux.MediaType(q.Get("type"))
	if mediaType == "" {
		if q.Get("season") != "" && q.Get("episode") != "" {
			mediaType = subflux.MediaTypeEpisode
		} else {
			mediaType = subflux.MediaTypeMovie
		}
	}

	req = subflux.SearchRequest{
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

	return req, lang, mediaType, QueryInt(q, "media_id")
}

// QueryInt parses a non-negative integer query parameter, returning 0 when absent or invalid.
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

// TryComputeHash computes and sets the request's video hash, best-effort, when a file path is available.
func TryComputeHash(ctx context.Context, ls *LiveState, req *subflux.SearchRequest, filePath string) {
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

// BuildSearchResults converts scored results to API response format. sc
// supplies the server-computed tier label per score; a nil scorer leaves
// tiers empty.
func BuildSearchResults(scored []subflux.ScoredResult, refs []subflux.DownloadedRef, sc tierLabeller) []SearchResult {
	if len(scored) > MaxResults {
		scored = scored[:MaxResults]
	}
	onDiskSet := make(map[subflux.DownloadedRef]struct{}, len(refs))
	for _, r := range refs {
		onDiskSet[r] = struct{}{}
	}
	results := make([]SearchResult, len(scored))
	for i := range scored {
		sr := &scored[i]
		_, onDisk := onDiskSet[subflux.DownloadedRef{
			ReleaseName: sr.Sub.ReleaseName,
			Provider:    sr.Sub.Provider,
		}]
		var tier subflux.ScoreTier
		if sc != nil {
			tier = sc.ScoreToTier(sr.Score)
		}
		results[i] = SearchResult{
			Provider:    sr.Sub.Provider,
			Language:    sr.Sub.Language,
			ReleaseName: sr.Sub.ReleaseName,
			Score:       sr.Score,
			Tier:        tier,
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

// ManualSearchResponse is the typed response from RunSearch. It
// deliberately carries no lock state: manual locks are invisible
// infrastructure, not a user-facing concept, so the popup has nothing to
// display about them. Providers lists each provider that returned nothing
// because it was skipped or failed.
type ManualSearchResponse struct {
	Results   []SearchResult         `json:"results"`
	Providers []ManualProviderNotice `json:"providers,omitempty"`
}

// ManualProviderNotice says why one provider contributed no results.
type ManualProviderNotice struct {
	Provider subflux.ProviderID `json:"provider"`
	// Kind is "gated" when the provider gate skipped the provider and
	// "error" when its search failed.
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Manual provider notice kinds.
const (
	NoticeGated = "gated"
	NoticeError = "error"
)

const maxNoticeBytes = 256

// RunSearch queries every configured provider through the engine and returns
// the scored manual-search response.
func RunSearch(ctx context.Context, deps *SearchDeps, ls *LiveState,
	req *subflux.SearchRequest, lang string, mediaType subflux.MediaType, filePath string,
) ManualSearchResponse {
	mediaID := mediaid.Build(req)
	TryComputeHash(ctx, ls, req, filePath)

	// The per-provider timeout is shared with the CLI search path via
	// subflux.DefaultManualProviderTimeout to prevent silent divergence.
	allResults, swept := ls.Engine.SweepProviders(ctx, req, subflux.DefaultManualProviderTimeout)
	notices := providerNotices(swept)

	var scored []subflux.ScoredResult
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
			"title", logsafe.Field(req.Title), "lang", lang, "media_type", mediaType)
	}

	var refs []subflux.DownloadedRef
	if len(scored) > 0 {
		var refsErr error
		refs, refsErr = deps.DB.DownloadedRefs(ctx, mediaType, mediaID, lang)
		if refsErr != nil {
			slog.Warn("manual search: refs lookup failed", "error", refsErr)
		}
	}

	return ManualSearchResponse{
		Results:   BuildSearchResults(scored, refs, ls.Scorer),
		Providers: notices,
	}
}

// providerNotices turns the engine's per-provider sweep notices into the
// response's, logging each failed provider.
func providerNotices(swept []search.SweepNotice) []ManualProviderNotice {
	out := make([]ManualProviderNotice, 0, len(swept))
	for _, n := range swept {
		if n.Gated {
			out = append(out, ManualProviderNotice{Provider: n.Provider, Kind: NoticeGated, Message: n.Reason})
			continue
		}
		msg := runesafe.SanitizeSingleLineBounded(n.Err.Error(), maxNoticeBytes)
		slog.Warn("manual search: provider failed", "provider", n.Provider, "error", msg)
		out = append(out, ManualProviderNotice{Provider: n.Provider, Kind: NoticeError, Message: msg})
	}
	return out
}
