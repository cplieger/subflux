// Command api-tester exercises each subtitle provider with real API calls
// against well-known media. Tests both search and download.
//
// Usage:
//
//	go run ./cmd/api-tester [-config config.yaml] [-provider name]
//
// Env vars override config: OPENSUBTITLES_API_KEY, OPENSUBTITLES_USERNAME,
// OPENSUBTITLES_PASSWORD, SUBSOURCE_API_KEY, SUBDL_API_KEY,
// BETASERIES_TOKEN, HDBITS_USERNAME, HDBITS_PASSKEY.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"subflux/internal/api"
	"subflux/internal/provider/animetosho"
	"subflux/internal/provider/betaseries"
	"subflux/internal/provider/gestdown"
	"subflux/internal/provider/hdbits"
	"subflux/internal/provider/opensubtitles"
	"subflux/internal/provider/subdl"
	"subflux/internal/provider/subsource"
	"subflux/internal/provider/yifysubtitles"

	"go.yaml.in/yaml/v3"
)

// Test fixture constants for well-known media used across provider tests.
const (
	testTitleBreakingBad = "Breaking Bad"
	testTitleInception   = "Inception"
	testImdbBreakingBad  = "tt0903747"
	testImdbInception    = "tt1375666"
	testNameInception    = "Inception movie"
	testFieldAPIKey      = "api_key"
	testMediaEpisode     = "episode"
	testMediaMovie       = "movie"
)

type testCase struct {
	name         string
	req          api.SearchRequest
	min          int
	skipDLURL    bool
	testDownload bool
}

type providerDef struct {
	factory  func(context.Context, map[string]any) (api.Provider, error)
	name     string
	keyField string
	envKeys  map[string]string
	cases    []testCase
	needsKey bool
}

var providerDefs = []providerDef{
	{
		name: "gestdown", factory: gestdown.Factory,
		cases: []testCase{
			{"Breaking Bad S03E05", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				TvdbID: 81189, Season: 3, Episode: 5,
				Languages: []string{"en"}, Year: 2008,
			}, 1, false, true},
			{"Game of Thrones S04E09", api.SearchRequest{
				MediaType: testMediaEpisode, Title: "Game of Thrones",
				TvdbID: 121361, Season: 4, Episode: 9,
				Languages: []string{"en", "fr"}, Year: 2011,
			}, 1, false, false},
		},
	},
	{
		name: "yifysubtitles", factory: yifysubtitles.Factory,
		cases: []testCase{
			{testNameInception, api.SearchRequest{
				MediaType: testMediaMovie, ImdbID: testImdbInception,
				Title: testTitleInception, Year: 2010,
				Languages: []string{"en"},
			}, 1, false, true},
			{"The Dark Knight movie", api.SearchRequest{
				MediaType: testMediaMovie, ImdbID: "tt0468569",
				Title: "The Dark Knight", Year: 2008,
				Languages: []string{"en", "fr"},
			}, 1, false, false},
		},
	},
	{
		name: "animetosho", factory: animetosho.Factory,
		envKeys: map[string]string{"ANIDB_CLIENT_KEY": "anidb_client_key"},
		cases: []testCase{
			{"One Piece ep 1100", api.SearchRequest{
				MediaType: testMediaEpisode, Title: "One Piece",
				TvdbID: 81797, Season: 1, Episode: 1100,
				Languages: []string{"en"},
			}, 1, false, true},
			{"Attack on Titan S04E12", api.SearchRequest{
				MediaType: testMediaEpisode, Title: "Attack on Titan",
				TvdbID: 267440, Season: 4, Episode: 12,
				Languages: []string{"en"},
			}, 0, false, false},
		},
	},
	{
		name: "opensubtitles", factory: opensubtitles.Factory,
		needsKey: true, keyField: testFieldAPIKey,
		envKeys: map[string]string{
			"OPENSUBTITLES_API_KEY":  testFieldAPIKey,
			"OPENSUBTITLES_USERNAME": "username",
			"OPENSUBTITLES_PASSWORD": "password",
		},
		cases: []testCase{
			{"Breaking Bad S02E10 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				ImdbID: testImdbBreakingBad, TvdbID: 81189,
				Season: 2, Episode: 10, Year: 2008,
				Languages: []string{"en"},
			}, 3, true, true},
			{testNameInception, api.SearchRequest{
				MediaType: testMediaMovie, ImdbID: testImdbInception,
				Title: testTitleInception, Year: 2010,
				Languages: []string{"en"},
			}, 3, true, true},
		},
	},
	{
		name: "betaseries", factory: betaseries.Factory,
		needsKey: true, keyField: "token",
		envKeys: map[string]string{"BETASERIES_TOKEN": "token"},
		cases: []testCase{
			{"Breaking Bad S03E07 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				TvdbID: 81189, Season: 3, Episode: 7,
				ImdbID: testImdbBreakingBad, Year: 2008,
				Languages: []string{"en"},
			}, 0, false, true},
		},
	},
	{
		name: "subsource", factory: subsource.Factory,
		needsKey: true, keyField: testFieldAPIKey,
		envKeys: map[string]string{"SUBSOURCE_API_KEY": testFieldAPIKey},
		cases: []testCase{
			{"Breaking Bad S04E03 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				ImdbID: testImdbBreakingBad, Season: 4, Episode: 3,
				Year: 2008, Languages: []string{"en"},
			}, 1, false, true},
			{testNameInception, api.SearchRequest{
				MediaType: testMediaMovie, ImdbID: testImdbInception,
				Title: testTitleInception, Year: 2010,
				Languages: []string{"en"},
			}, 1, false, true},
		},
	},
	{
		name: "subdl", factory: subdl.Factory,
		needsKey: true, keyField: testFieldAPIKey,
		envKeys: map[string]string{"SUBDL_API_KEY": testFieldAPIKey},
		cases: []testCase{
			{"Breaking Bad S05E03 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				ImdbID: testImdbBreakingBad, Season: 5, Episode: 3,
				Year: 2008, Languages: []string{"en"},
			}, 1, false, true},
			{testNameInception, api.SearchRequest{
				MediaType: testMediaMovie, ImdbID: testImdbInception,
				Title: testTitleInception, Year: 2010,
				Languages: []string{"en"},
			}, 1, false, true},
		},
	},
	{
		name: "hdbits", factory: hdbits.Factory,
		needsKey: true, keyField: "passkey",
		envKeys: map[string]string{
			"HDBITS_USERNAME": "username",
			"HDBITS_PASSKEY":  "passkey",
		},
		cases: []testCase{
			{"Breaking Bad S05E03 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: testTitleBreakingBad,
				ImdbID: testImdbBreakingBad, TvdbID: 81189,
				Season: 5, Episode: 3,
				Year: 2008, Languages: []string{"en"},
			}, 1, true, true},
			{"True Detective S01E04 episode", api.SearchRequest{
				MediaType: testMediaEpisode, Title: "True Detective",
				ImdbID: "tt2356777", TvdbID: 270633,
				Season: 1, Episode: 4,
				Year: 2014, Languages: []string{"en"},
			}, 1, true, false},
		},
	},
}

// --- Config + env loading ---

type configFile struct {
	Providers map[string]struct {
		Settings map[string]any `yaml:"settings"`
		Enabled  bool           `yaml:"enabled"`
	} `yaml:"providers"`
}

const maxConfigSize = 1 << 20

// loadSettings merges config file settings with environment variable
// overrides for each provider's credentials.
func loadSettings(path string) map[string]map[string]any {
	result := make(map[string]map[string]any)
	if path != "" {
		loadConfigFile(path, result)
	}
	for i := range providerDefs {
		pd := &providerDefs[i]
		for envKey, settingsKey := range pd.envKeys {
			if v := os.Getenv(envKey); v != "" {
				if result[pd.name] == nil {
					result[pd.name] = make(map[string]any)
				}
				result[pd.name][settingsKey] = v
			}
		}
	}
	return result
}

// loadConfigFile reads and parses a YAML config file into provider
// settings, logging warnings on missing/invalid/oversized files.
func loadConfigFile(path string, result map[string]map[string]any) {
	f, err := os.Open(path)
	if err != nil {
		slog.Warn("config not found, using env vars only",
			"path", path, "error", err)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		slog.Error("failed to stat config", "error", err)
		return
	}
	if info.Size() > maxConfigSize {
		slog.Error("config file too large",
			"path", path, "bytes", info.Size(),
			"max", maxConfigSize)
		return
	}

	data := make([]byte, info.Size())
	if _, err := io.ReadFull(f, data); err != nil {
		slog.Error("failed to read config", "error", err)
		return
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		slog.Error("failed to parse config", "error", err)
		return
	}

	for name, p := range cfg.Providers {
		if p.Settings != nil {
			result[name] = p.Settings
		}
	}
}

// --- Test runner ---

// hasCredentials reports whether the provider definition has the
// required credentials present in the settings map.
func hasCredentials(pd *providerDef, settings map[string]any) bool {
	if !pd.needsKey {
		return true
	}
	if settings == nil {
		return false
	}
	v, ok := settings[pd.keyField].(string)
	return ok && v != ""
}

// validateResults checks the first 5 search results for quality issues
// (empty language, missing release/ID, missing download URL).
func validateResults(results []api.Subtitle, skipDLURL bool) []string {
	var warnings []string
	limit := min(len(results), 5)
	for i := range limit {
		sub := results[i]
		if sub.Language == "" {
			warnings = append(warnings,
				fmt.Sprintf("[%d] empty language", i))
		}
		if sub.ReleaseName == "" && sub.ID == "" {
			warnings = append(warnings,
				fmt.Sprintf("[%d] no release or ID", i))
		}
		if !skipDLURL && sub.DownloadURL == "" {
			warnings = append(warnings,
				fmt.Sprintf("[%d] no download URL", i))
		}
	}
	return warnings
}

// testDownload attempts to download a subtitle with retry logic,
// waiting 2s before the first attempt and 3s between retries.
func testDownload(ctx context.Context, p api.Provider, result *api.Subtitle, label string) bool {
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
	}

	var data []byte
	var dlErr error
	var dlDur time.Duration
	for attempt := range 3 {
		if attempt > 0 {
			retryTimer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				retryTimer.Stop()
				return false
			case <-retryTimer.C:
			}
		}
		dlStart := time.Now()
		data, dlErr = p.Download(ctx, result)
		dlDur = time.Since(dlStart)
		if dlErr == nil {
			break
		}
		slog.Debug("download retry",
			"test", label, "attempt", attempt+1,
			"error", dlErr)
	}

	switch {
	case dlErr != nil:
		slog.Warn("WARN: download failed",
			"test", label, "error", dlErr,
			"duration", dlDur)
		return false
	case len(data) < 10:
		slog.Warn("WARN: download too small",
			"test", label, "bytes", len(data),
			"duration", dlDur)
		return false
	default:
		slog.Info("PASS: download", "test", label,
			"bytes", len(data), "duration", dlDur)
		return true
	}
}

// runCase executes a single test case against a provider, returning
// the number of passed and failed assertions.
func runCase(ctx context.Context, p api.Provider, pd *providerDef, tc *testCase) (passed, failed int) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	start := time.Now()
	results, searchErr := p.Search(ctx, &tc.req)
	dur := time.Since(start)

	label := fmt.Sprintf("%s/%s", pd.name, tc.name)
	if searchErr != nil {
		slog.Error("FAIL: search error",
			"test", label, "error", searchErr, "duration", dur)
		return 0, 1
	}
	count := len(results)
	if count < tc.min {
		slog.Warn("WARN: fewer results than expected",
			"test", label, "got", count,
			"min", tc.min, "duration", dur)
		return 0, 1
	}

	if warnings := validateResults(results, tc.skipDLURL); len(warnings) > 0 {
		slog.Warn("WARN: quality issues",
			"test", label, "results", count,
			"issues", strings.Join(warnings, "; "),
			"duration", dur)
		return 0, 1
	}

	slog.Info("PASS: search", "test", label,
		"results", count, "duration", dur)
	passed = 1

	if tc.testDownload && count > 0 {
		if testDownload(ctx, p, &results[0], label) {
			passed++
		} else {
			failed++
		}
	}
	return passed, failed
}

// runTests iterates all provider definitions, creates providers from
// settings, and runs search + optional download tests for each case.
func runTests(ctx context.Context, settings map[string]map[string]any, filter string) (passed, failed, skipped int) {
	for i := range providerDefs {
		pd := &providerDefs[i]
		if filter != "" && pd.name != filter {
			continue
		}
		s := settings[pd.name]
		if !hasCredentials(pd, s) {
			slog.Warn("SKIP: missing credentials",
				"provider", pd.name, "field", pd.keyField)
			skipped++
			continue
		}
		p, err := pd.factory(context.Background(), s)
		if err != nil {
			slog.Error("FAIL: factory error",
				"provider", pd.name, "error", err)
			failed++
			continue
		}
		for j := range pd.cases {
			p2, f2 := runCase(ctx, p, pd, &pd.cases[j])
			passed += p2
			failed += f2

			delay := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				delay.Stop()
				return
			case <-delay.C:
			}
		}
	}
	return
}

func run() int {
	configPath := flag.String("config", "",
		"path to config.yaml for provider credentials")
	filterProvider := flag.String("provider", "",
		"test only this provider")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
		&slog.HandlerOptions{Level: slog.LevelDebug})))

	settings := loadSettings(*configPath)

	slog.Info("api-tester starting",
		"config", *configPath,
		"filter", *filterProvider,
		"providers", len(providerDefs))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	passed, failed, skipped := runTests(ctx, settings, *filterProvider)
	fmt.Fprintf(os.Stderr,
		"\n=== Results: %d passed, %d failed, %d skipped ===\n",
		passed, failed, skipped)
	if failed > 0 {
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
