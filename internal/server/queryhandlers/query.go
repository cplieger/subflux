package queryhandlers

import (
	"cmp"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// providerTimeoutResponse is an alias for the canonical wire type.
type providerTimeoutResponse = api.ProvidersResponse

// HandleState returns subtitle state with optional filters.
// GET /api/state?type=episode&lang=fr&provider=opensubtitles&limit=50
func (h *Handler) HandleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !httphelpers.RequireGET(w, r) {
		return
	}
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 10000 {
		slog.Debug("handleState: limit capped", "requested", limit)
		limit = 10000
	}
	var offset int
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	searchParam := q.Get("search")
	if len(searchParam) > 200 {
		searchParam = searchParam[:200]
	}
	entries, err := h.queryDB.GetState(ctx, &api.StateQuery{
		MediaType: api.MediaType(q.Get("type")),
		Language:  q.Get("lang"),
		Provider:  api.ProviderID(q.Get("provider")),
		Search:    searchParam,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "state")
		return
	}
	slog.Debug("handleState", "results", len(entries),
		"type", q.Get("type"), "lang", q.Get("lang"),
		"provider", q.Get("provider"), "search", searchParam,
		"limit", limit)
	api.WriteJSON(w, entries)
}

// HandleBackoff returns items currently in adaptive search backoff.
// GET /api/backoff
func (h *Handler) HandleBackoff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !httphelpers.RequireGET(w, r) {
		return
	}
	entries, err := h.queryDB.GetBackoffItems(ctx)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "backoff")
		return
	}
	api.WriteJSON(w, entries)
}

// HandleBackoffByPrefix returns backoff entries for media IDs matching a prefix.
// GET /api/backoff/prefix?type=episode&prefix=tvdb-81189-
func (h *Handler) HandleBackoffByPrefix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	handleTypePrefixQuery(w, r, "backoff prefix",
		func(mt, p string) (any, error) { return h.queryDB.GetBackoffByPrefix(ctx, api.MediaType(mt), p) })
}

// HandleLocks returns all manually locked media+language pairs.
// GET /api/locks
func (h *Handler) HandleLocks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !httphelpers.RequireGET(w, r) {
		return
	}
	entries, err := h.queryDB.GetManualLocks(ctx)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "locks")
		return
	}
	api.WriteJSON(w, entries)
}

// HandleProviders returns registered providers with enabled status.
// GET /api/providers
func (h *Handler) HandleProviders(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequireGET(w, r) {
		return
	}
	ls := h.state()
	cfgProviders := ls.Cfg.ProviderConfigs()
	type providerInfo struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Loaded  bool   `json:"loaded"`
	}
	out := make([]providerInfo, 0, len(cfgProviders))
	for name, cfg := range cfgProviders {
		if name == api.ProviderNameMock {
			continue
		}
		out = append(out, providerInfo{
			Name:    string(name),
			Enabled: cfg.Enabled,
			Loaded: slices.ContainsFunc(ls.Providers, func(p api.Provider) bool {
				return p.Name() == name
			}),
		})
	}
	slices.SortFunc(out, func(a, b providerInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})
	api.WriteJSON(w, out)
}

// HandleConfigParsed returns the config as structured JSON.
// GET /api/config/parsed
func (h *Handler) HandleConfigParsed(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequireGET(w, r) {
		return
	}
	type parsedConfig struct {
		Adaptive       any                   `json:"adaptive"`
		Search         any                   `json:"search"`
		Providers      map[string]bool       `json:"providers"`
		SonarrURL      string                `json:"sonarr_url,omitempty"`
		RadarrURL      string                `json:"radarr_url,omitempty"`
		LanguageRules  api.LanguageRulesJSON `json:"language_rules"`
		IgnoredCodecs  []string              `json:"ignored_codecs,omitempty"`
		Languages      []string              `json:"languages"`
		Scores         api.Scores            `json:"scores"`
		PostProcessing api.PostProcessConfig `json:"post_processing"`
		Configured     bool                  `json:"configured"`
		Sonarr         bool                  `json:"sonarr_configured"`
		Radarr         bool                  `json:"radarr_configured"`
	}
	if !h.configured() {
		api.WriteJSON(w, parsedConfig{
			Configured: false,
			Languages:  []string{},
			Providers:  map[string]bool{},
		})
		return
	}
	ls := h.state()
	provMap := make(map[string]bool)
	for name, cfg := range ls.Cfg.ProviderConfigs() {
		provMap[string(name)] = cfg.Enabled
	}

	ignoredMap := search.IgnoredCodecsFromConfig(ls.Cfg)
	var ignoredCodecs []string
	for _, codec := range []string{"pgs", "vobsub", "ass", "ssa"} {
		if ignoredMap[codec] {
			ignoredCodecs = append(ignoredCodecs, codec)
		}
	}

	api.WriteJSON(w, parsedConfig{
		Configured:     true,
		Languages:      ls.Cfg.LanguageCodes(),
		LanguageRules:  ls.Cfg.LanguageRulesForUI(),
		Providers:      provMap,
		Search:         ls.Cfg.Search(),
		Adaptive:       ls.Cfg.Adaptive(),
		PostProcessing: ls.Cfg.PostProcessConfig(),
		Scores:         ls.Cfg.Scores(),
		SonarrURL:      ls.Cfg.SonarrConfig().PublicURL,
		RadarrURL:      ls.Cfg.RadarrConfig().PublicURL,
		Sonarr:         ls.Cfg.SonarrConfig().URL != "",
		Radarr:         ls.Cfg.RadarrConfig().URL != "",
		IgnoredCodecs:  ignoredCodecs,
	})
}

// HandleScore simulates scoring a subtitle against a video.
// POST /api/score with JSON body.
func (h *Handler) HandleScore(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequirePOST(w, r) {
		return
	}
	ls := h.state()
	var req struct {
		MediaType   api.MediaType `json:"media_type"`
		ReleaseName string        `json:"release_name"`
		SubRelease  string        `json:"sub_release"`
		MatchedBy   string        `json:"matched_by"`
	}
	if !httphelpers.DecodeJSONBody(w, r, &req, 1<<20) {
		return
	}
	if req.MediaType == "" {
		req.MediaType = api.MediaTypeEpisode
	}

	result := ls.Engine.SimulateScore(
		req.MediaType, req.ReleaseName, req.SubRelease, api.MatchMethod(req.MatchedBy))

	slog.Debug("handleScore",
		"media_type", req.MediaType,
		"video_release", req.ReleaseName,
		"sub_release", req.SubRelease,
		"matched_by", req.MatchedBy,
		"score", result.Score, "score_no_hash", result.ScoreNoHash)

	api.WriteJSON(w, api.ScorePreviewResponse(result))
}

// HandleSearchTargets resolves subtitle targets for a media item
// without actually searching. Useful for debugging language rules.
// GET /api/search/targets?orig_lang=en&audio_langs=en,fr
func (h *Handler) HandleSearchTargets(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequireGET(w, r) {
		return
	}
	ls := h.state()
	q := r.URL.Query()
	origLang := q.Get("orig_lang")
	var audioLangs []string
	if v := q.Get("audio_langs"); v != "" {
		for l := range strings.SplitSeq(v, ",") {
			if t := strings.TrimSpace(l); t != "" {
				audioLangs = append(audioLangs, t)
			}
		}
	}
	targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)
	slog.Debug("handleSearchTargets",
		"orig_lang", origLang, "audio_langs", audioLangs,
		"targets", len(targets))
	var out []api.SearchTarget
	for _, t := range targets {
		providers := make([]string, len(t.Providers))
		for i, p := range t.Providers {
			providers[i] = string(p)
		}
		exclude := make([]string, len(t.Exclude))
		for i, e := range t.Exclude {
			exclude[i] = string(e)
		}
		out = append(out, api.SearchTarget{
			Code:      t.Code,
			Variant:   string(t.EffectiveVariant()),
			Providers: providers,
			Exclude:   exclude,
			MinScore:  t.MinScore,
		})
	}
	api.WriteJSON(w, api.SearchTargetsResponse{
		OrigLang:   origLang,
		AudioLangs: audioLangs,
		Targets:    out,
	})
}

// HandleProviderTimeout returns provider timeout state for all providers.
// GET /api/providers/timeout
func (h *Handler) HandleProviderTimeout(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequireGET(w, r) {
		return
	}
	ls := h.state()
	status, enabled := ls.Engine.ProviderTimeouts()
	if !enabled {
		api.WriteJSON(w, providerTimeoutResponse{})
		return
	}
	api.WriteJSON(w, providerTimeoutResponse{Enabled: true, Providers: status})
}

// HandleProviderTimeoutReset clears all provider timeout state and re-enables all providers.
// POST /api/providers/timeout/reset
func (h *Handler) HandleProviderTimeoutReset(w http.ResponseWriter, r *http.Request) {
	if !httphelpers.RequirePOST(w, r) {
		return
	}
	ls := h.state()
	_, enabled := ls.Engine.ProviderTimeouts()
	if !enabled {
		api.WriteJSON(w, providerTimeoutResponse{})
		return
	}
	ls.Engine.ResetTimeouts()
	api.Ok(w)
}

// handleTypePrefixQuery is a shared handler for GET endpoints that
// accept ?type=...&prefix=... and delegate to a DB query function.
func handleTypePrefixQuery(w http.ResponseWriter, r *http.Request,
	label string, queryFn func(string, string) (any, error),
) {
	if !httphelpers.RequireGET(w, r) {
		return
	}
	q := r.URL.Query()
	mediaType := q.Get("type")
	if mediaType == "" {
		mediaType = string(api.MediaTypeEpisode)
	}
	if mediaType != string(api.MediaTypeEpisode) && mediaType != string(api.MediaTypeMovie) {
		api.BadRequestC(w, r, api.CodeQueryInvalidFilter, "invalid type parameter")
		return
	}
	prefix := q.Get("prefix")
	if prefix != "" && !api.IsValidMediaPrefix(prefix) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid prefix format")
		return
	}
	result, err := queryFn(mediaType, prefix)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", label)
		return
	}
	slog.Debug(label+" query completed", "media_type", mediaType, "prefix", prefix)
	api.WriteJSON(w, result)
}
