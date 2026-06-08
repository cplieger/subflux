// Package clisearch implements CLI subtitle search, resolution, and download.
package clisearch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/embedded"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"golang.org/x/sync/errgroup"
)

// defaultProviderTimeout is the per-provider search timeout for CLI searches.
const defaultProviderTimeout = api.DefaultManualProviderTimeout

// flagDownload is the CLI flag for triggering subtitle download.
const flagDownload = "--download"

// searchItem holds the metadata for a single media item resolved from
// Sonarr or Radarr, used to drive provider searches and subtitle downloads.
type searchItem struct {
	Title     string
	ImdbID    string
	MediaType api.MediaType
	SceneName string
	FilePath  string
	Year      int
	Season    int
	Episode   int
	TvdbID    int
	TmdbID    int
}

// Label returns a human-readable description of the item for CLI output.
func (s *searchItem) label() string {
	if s.MediaType == api.MediaTypeMovie {
		return fmt.Sprintf("%s (%d)", s.Title, s.Year)
	}
	if s.Episode > 0 {
		return fmt.Sprintf("%s S%02dE%02d", s.Title, s.Season, s.Episode)
	}
	if s.Season > 0 {
		return fmt.Sprintf("%s Season %d", s.Title, s.Season)
	}
	return s.Title
}

// parseArgs parses --key value pairs from the given argument slice.
// Returns the params map and whether --download was specified.
func parseArgs(args []string) (params map[string]string, download bool) {
	params = make(map[string]string)
	for i := 0; i < len(args); i++ {
		if args[i] == flagDownload {
			download = true
			continue
		}
		if name, ok := cutPrefix(args[i], "--"); ok && name != "" && i+1 < len(args) {
			params[name] = args[i+1]
			i++
		}
	}
	return params, download
}

// cutPrefix is strings.CutPrefix inlined to avoid an import for one call.
func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

// errUsage is returned when no search criteria are provided.
var errUsage = errors.New("no search criteria provided (need --imdb, --tmdb, or --title)")

// parseSearchParams extracts and validates the core search parameters.
// Returns an error if no search criteria are provided.
//
//nolint:gocritic // tooManyResultsChecker: 6 returns is acceptable for this internal parser
func parseSearchParams(params map[string]string) (lang, imdbID, tmdbID, title string, pickN int, err error) {
	lang = params["lang"]
	if lang == "" {
		lang = "fr"
	}
	imdbID = params["imdb"]
	tmdbID = params["tmdb"]
	title = params["title"]

	pickN = 1
	if params["pick"] != "" {
		if n, parseErr := strconv.Atoi(params["pick"]); parseErr == nil && n > 0 {
			pickN = n
		}
	}

	if imdbID == "" && tmdbID == "" && title == "" {
		return "", "", "", "", 0, errUsage
	}
	return lang, imdbID, tmdbID, title, pickN, nil
}

// ProviderLoader is the narrow interface for loading providers from config.
// Satisfied by *provider.Registry via structural typing.
type ProviderLoader interface {
	LoadAll(ctx context.Context, providers map[api.ProviderID]api.ProviderCfg) ([]api.Provider, error)
}

// DownloadRecorder persists download records. Satisfied by *store.DB.
type DownloadRecorder interface {
	SaveDownload(ctx context.Context, rec *api.DownloadRecord) error
}

// Deps holds the external dependencies needed by RunSearch.
type Deps struct {
	Cfg             api.ConfigProvider
	Registry        ProviderLoader
	Store           DownloadRecorder // nil = downloads not recorded
	ProviderTimeout time.Duration    // per-provider timeout; zero = defaultProviderTimeout
}

// RunSearch performs a manual subtitle search from the command line.
// Returns an error instead of calling os.Exit, allowing the caller to
// decide how to handle failures.
func RunSearch(ctx context.Context, args []string, deps Deps) error {
	params, download := parseArgs(args)
	lang, imdbID, tmdbID, title, pickN, err := parseSearchParams(params)
	if err != nil {
		return err
	}

	var seasonFilter, episodeFilter int
	if v := params["season"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			seasonFilter = n
		}
	}
	if v := params["episode"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			episodeFilter = n
		}
	}

	providers, err := deps.Registry.LoadAll(ctx, deps.Cfg.ProviderConfigs())
	if err != nil {
		return fmt.Errorf("provider error: %w", err)
	}
	providers = provider.WrapRetryAll(providers, provider.DownloadRetryAttempts, provider.DownloadRetryInitBackoff)

	items := resolveItems(ctx, deps.Cfg, imdbID, tmdbID, title, seasonFilter, episodeFilter)
	if len(items) == 0 {
		return errors.New("not found in Sonarr or Radarr; add the media first")
	}

	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	fmt.Printf("Found %d item(s) to search for %s subtitles\n\n", len(items), lang)
	scores := deps.Cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(providers,
		search.WithConfig(deps.Cfg), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{MinConfidence: deps.Cfg.SyncConfig().SyncMinConfidence}),
		search.WithTracks(embedded.ProviderDirect{}))
	for i := range items {
		runItemSearch(searchCtx, engine, sc, providers, &items[i], lang, download, pickN, deps.Store, deps.providerTimeoutOrDefault())
	}
	return nil
}

// providerTimeoutOrDefault returns the configured timeout or the default.
func (d Deps) providerTimeoutOrDefault() time.Duration {
	if d.ProviderTimeout > 0 {
		return d.ProviderTimeout
	}
	return defaultProviderTimeout
}

// runItemSearch searches all providers for a single media item, scores the
// results, and optionally downloads the best match.
func runItemSearch(ctx context.Context, engine api.SearchEngine, sc api.Scorer,
	providers []api.Provider, item *searchItem,
	lang string, download bool, pickN int, recorder DownloadRecorder, timeout time.Duration,
) {
	fmt.Printf("--- %s ---\n", item.label())

	req := &api.SearchRequest{
		Title: item.Title, Year: item.Year, ImdbID: item.ImdbID,
		TmdbID: item.TmdbID, TvdbID: item.TvdbID,
		Season: item.Season, Episode: item.Episode,
		Languages: []string{lang}, MediaType: item.MediaType,
		ReleaseName: item.SceneName,
	}

	var (
		mu         sync.Mutex
		allResults []api.Subtitle
	)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(len(providers))
	for _, p := range providers {
		g.Go(func() error {
			pCtx, pCancel := context.WithTimeout(gCtx, timeout)
			defer pCancel()
			subs, searchErr := p.Search(pCtx, req)
			if searchErr != nil {
				fmt.Printf("  %s: error: %v\n", p.Name(), searchErr)
				return nil
			}
			mu.Lock()
			if len(subs) > 0 {
				fmt.Printf("  %s: %d results\n", p.Name(), len(subs))
			}
			allResults = append(allResults, subs...)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	if len(allResults) == 0 {
		fmt.Println("  No results found.")
		fmt.Println()
		return
	}

	scored := engine.ScoreSubtitles(req, allResults)
	if len(scored) == 0 {
		fmt.Println("  No results after scoring.")
		fmt.Println()
		return
	}

	printScoredResults(scored, sc, item.MediaType)

	if download && item.FilePath != "" {
		downloadSubtitle(ctx, engine, sc, providers, req, item, scored, lang, pickN, recorder)
	}
	fmt.Println()
}

// printScoredResults displays the ranked subtitle results.
func printScoredResults(scored []api.ScoredResult, sc api.Scorer, mediaType api.MediaType) {
	fmt.Printf("  %d result(s) ranked by release quality:\n", len(scored))
	for i := range scored {
		hi := ""
		if scored[i].Sub.HearingImp {
			hi = " [HI]"
		}
		tier := sc.ScoreToTier(scored[i].Score, mediaType)
		release := scored[i].Sub.ReleaseName
		if len(release) > 45 {
			release = release[:42] + "..."
		}
		fmt.Printf("  %2d. [%3d %-10s] %-14s %-6s %s%s\n",
			i+1, scored[i].Score, tier, scored[i].Sub.Provider,
			scored[i].Sub.MatchedBy, release, hi)
	}
}

// downloadSubtitle fetches the chosen subtitle, syncs timing, writes the file,
// and records the download in the database.
func downloadSubtitle(ctx context.Context, engine api.SearchEngine, sc api.Scorer,
	providers []api.Provider,
	req *api.SearchRequest, item *searchItem,
	results []api.ScoredResult, lang string, pickN int, recorder DownloadRecorder,
) {
	idx := pickN - 1
	if idx >= len(results) {
		fmt.Printf("  --pick %d but only %d result(s)\n", pickN, len(results))
		return
	}
	chosen := results[idx]
	fmt.Printf("  Downloading #%d from %s (score=%d, %s)...\n",
		pickN, chosen.Sub.Provider, chosen.Score,
		sc.ScoreToTier(chosen.Score, item.MediaType))

	if req.VideoHash == "" {
		if hash, size, hashErr := engine.HashFile(ctx, item.FilePath); hashErr == nil {
			req.VideoHash = hash
			req.VideoSize = size
		} else {
			slog.Debug("hash computation failed", "path", item.FilePath, "error", hashErr)
		}
	}

	for _, p := range providers {
		if p.Name() != chosen.Sub.Provider {
			continue
		}
		data, dlErr := p.Download(ctx, &chosen.Sub)
		if dlErr != nil {
			fmt.Printf("  Download failed: %v\n", dlErr)
			return
		}
		data, _ = engine.SyncAndPostProcess(ctx, data, item.FilePath, lang,
			api.VariantFromFlags(chosen.Sub.HearingImp, chosen.Sub.Forced))
		if len(data) == 0 {
			fmt.Println("  Post-processing produced empty subtitle, skipping write")
			return
		}

		subPath := api.SubtitlePath(item.FilePath, lang, chosen.Sub.HearingImp, chosen.Sub.Forced)
		if err := writeSubtitleFile(ctx, subPath, data); err != nil {
			fmt.Printf("  Write failed: %v\n", err)
			return
		}
		fmt.Printf("  Saved: %s\n", subPath)

		recordDownload(ctx, req, item, &chosen, subPath, lang, pickN, recorder)
		return
	}
	fmt.Printf("  Provider %s not found in loaded providers\n", chosen.Sub.Provider)
}
