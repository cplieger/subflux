package polling

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/api"
)

// errStore is a PollerStore whose DeleteStateByPaths always fails, used to
// exercise the cleanup-error WARN branch.
type errStore struct{}

func (errStore) DeleteStateByPaths(_ context.Context, _ []string) (api.CleanupResult, error) {
	return api.CleanupResult{}, errors.New("delete boom")
}

// tempVideo writes a throwaway video file in a fresh temp dir and returns its path.
func tempVideo(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) = %v", p, err)
	}
	return p
}

// importPoller builds a Poller (with mock deps) and a LiveState wired to the
// given search engine, for processPollImport tests.
func importPoller(engine api.SearchEngine) (*Poller, *LiveState) {
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Engine: engine}
	return &Poller{deps: fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}, ls
}

// movieImportResult is a minimal Radarr ImportResult builder for refresh-path tests.
func movieImportResult() (*ImportResult, error) {
	return &ImportResult{
		Req:       &api.SearchRequest{MediaType: api.MediaTypeMovie, Title: "T"},
		Label:     "T (2024)",
		Source:    PollSourceRadarr,
		RefreshID: 7,
	}, nil
}

// --- file-existence / path-validation gate ---

func TestProcessPollImport_file_gone(t *testing.T) {
	store := &mockStore{}
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      store,
		Metrics:    &mockMetrics{},
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg}
	p := &Poller{
		deps:      deps,
		stateFunc: func() *LiveState { return ls },
	}

	p.processPollImport(context.Background(), ls, "/nonexistent/video.mkv",
		func() (*ImportResult, error) {
			t.Fatal("buildFn should not be called when file is gone")
			return nil, nil
		},
		nil,
	)

	if len(store.deletedPaths) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(store.deletedPaths))
	}
	if store.deletedPaths[0][0] != "/nonexistent/video.mkv" {
		t.Errorf("unexpected path: %s", store.deletedPaths[0][0])
	}
}

// A failed DeleteStateByPaths cleanup (video file gone) must be WARN-logged.
func TestProcessPollImport_warns_when_cleanup_errors(t *testing.T) {
	sink := capture.Default(t)
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: fullDeps(errStore{}), stateFunc: func() *LiveState { return ls }}
	p.processPollImport(context.Background(), ls, "/nonexistent/cleanup-err.mkv",
		func() (*ImportResult, error) { t.Fatal("buildFn must not run for a missing file"); return nil, nil },
		nil)
	if sink.CountLevel(slog.LevelWarn, "poll: cleanup failed") == 0 {
		t.Errorf("cleanup error: want WARN 'poll: cleanup failed'")
	}
}

// A successful cleanup must not emit the cleanup-failed WARN.
func TestProcessPollImport_silent_when_cleanup_ok(t *testing.T) {
	sink := capture.Default(t)
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.processPollImport(context.Background(), ls, "/nonexistent/cleanup-ok.mkv",
		func() (*ImportResult, error) { t.Fatal("buildFn must not run for a missing file"); return nil, nil },
		nil)
	if sink.CountLevel(slog.LevelWarn, "poll: cleanup failed") > 0 {
		t.Errorf("cleanup ok: unexpected WARN 'poll: cleanup failed'")
	}
}

// --- search + arr-notify path ---

func TestProcessPollImport_search_success(t *testing.T) {
	tmp := t.TempDir()
	videoPath := filepath.Join(tmp, "video.mkv")
	if err := os.WriteFile(videoPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	evts := &mockEvents{}
	metrics := &mockMetrics{}
	statsCache := &mockStatsCache{}
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      &mockStore{},
		Metrics:    metrics,
		Alerts:     &mockAlerts{},
		Events:     evts,
		StatsCache: statsCache,
	}
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/sub.srt"}}}, CoverageChanged: true}}
	ls := &LiveState{Cfg: cfg, Engine: engine}
	p := &Poller{
		deps:      deps,
		stateFunc: func() *LiveState { return ls },
	}

	req := &api.SearchRequest{MediaType: api.MediaTypeMovie, Title: "Test"}
	p.processPollImport(context.Background(), ls, videoPath,
		func() (*ImportResult, error) {
			return &ImportResult{
				Req:       req,
				Targets:   []api.SubtitleTarget{{Code: "en"}},
				Label:     "Test (2024)",
				Source:    PollSourceRadarr,
				RefreshID: 1,
			}, nil
		},
		func(_ context.Context, _ int) error { return nil },
	)

	if len(metrics.imports) != 1 || metrics.imports[0] != "radarr" {
		t.Errorf("expected 1 radarr import, got %v", metrics.imports)
	}
	if len(evts.published) != 1 {
		t.Errorf("expected 1 event, got %d", len(evts.published))
	}
	if statsCache.invalidated != 1 {
		t.Errorf("expected stats cache invalidated, got %d", statsCache.invalidated)
	}
}

// refreshFn (arr rescan notify) runs only when subtitle paths were downloaded.
func TestProcessPollImport_calls_refreshFn_when_paths_present(t *testing.T) {
	video := tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/x.srt"}}}, CoverageChanged: false}}
	p, ls := importPoller(engine)
	calls := 0
	p.processPollImport(context.Background(), ls, video,
		movieImportResult,
		func(_ context.Context, _ int) error { calls++; return nil })
	if calls != 1 {
		t.Errorf("refreshFn calls = %d, want 1 (downloaded paths must trigger an arr refresh)", calls)
	}
}

// A coverage-only change with no downloaded paths must not notify arr.
func TestProcessPollImport_skips_refreshFn_when_no_paths(t *testing.T) {
	video := tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{CoverageChanged: true}}
	p, ls := importPoller(engine)
	calls := 0
	p.processPollImport(context.Background(), ls, video,
		movieImportResult,
		func(_ context.Context, _ int) error { calls++; return nil })
	if calls != 0 {
		t.Errorf("refreshFn calls = %d, want 0 (no downloaded paths must not refresh even when coverage changed)", calls)
	}
}

// A failed arr refresh notification must be WARN-logged.
func TestProcessPollImport_warns_when_refresh_errors(t *testing.T) {
	sink := capture.Default(t)
	video := tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/x.srt"}}}, CoverageChanged: false}}
	p, ls := importPoller(engine)
	p.processPollImport(context.Background(), ls, video,
		movieImportResult,
		func(_ context.Context, _ int) error { return errors.New("notify boom") })
	if sink.CountLevel(slog.LevelWarn, "failed to notify arr") == 0 {
		t.Errorf("refreshFn error: want WARN 'failed to notify arr'")
	}
}

// A successful arr refresh must not emit the notify-failed WARN.
func TestProcessPollImport_silent_when_refresh_ok(t *testing.T) {
	sink := capture.Default(t)
	video := tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/x.srt"}}}, CoverageChanged: false}}
	p, ls := importPoller(engine)
	p.processPollImport(context.Background(), ls, video,
		movieImportResult,
		func(_ context.Context, _ int) error { return nil })
	if sink.CountLevel(slog.LevelWarn, "failed to notify arr") > 0 {
		t.Errorf("refreshFn ok: unexpected WARN 'failed to notify arr'")
	}
}

// --- processSonarrImport / processRadarrImport wiring + exclude-tag gating ---

// processSonarrImport fetches series+episode by the entry's IDs, applies
// exclude-tag gating, and only reaches the search phase (recording a "sonarr"
// import metric) for a non-excluded item.
func TestProcessSonarrImport_excludeTag_gates_search(t *testing.T) {
	tests := []struct {
		name       string
		excludeIDs map[int]struct{}
		tags       []int
		want       []string
	}{
		{name: "non-excluded series searches", tags: nil, excludeIDs: nil, want: []string{"sonarr"}},
		{name: "excluded series is skipped", tags: []int{7}, excludeIDs: map[int]struct{}{7: {}}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			video := tempVideo(t)
			metrics := &mockMetrics{}
			sonarr := &mockHistoryPoller{
				series:   map[int]arrapi.Series{10: {ID: 10, Title: "Show", Year: 2020, Tags: tt.tags}},
				episodes: map[int]arrapi.Episode{20: {ID: 20, SeasonNumber: 1, EpisodeNumber: 2}},
			}
			deps := fullDeps(&mockStore{})
			deps.Metrics = metrics
			cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
			engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/sub.srt"}}}, CoverageChanged: true}}
			ls := &LiveState{Cfg: cfg, Engine: engine, Sonarr: sonarr}
			p := &Poller{deps: deps, stateFunc: func() *LiveState { return ls }}

			entry := arrapi.HistoryRecord{SeriesID: 10, EpisodeID: 20, Data: map[string]string{"importedPath": video}}
			p.processSonarrImport(context.Background(), ls, &entry, tt.excludeIDs)

			if !slices.Equal(metrics.imports, tt.want) {
				t.Errorf("metrics.imports = %v, want %v", metrics.imports, tt.want)
			}
		})
	}
}

// processRadarrImport fetches the movie by the entry's ID, applies exclude-tag
// gating, and only reaches the search phase (recording a "radarr" import
// metric) for a non-excluded item.
func TestProcessRadarrImport_excludeTag_gates_search(t *testing.T) {
	tests := []struct {
		name       string
		excludeIDs map[int]struct{}
		tags       []int
		want       []string
	}{
		{name: "non-excluded movie searches", tags: nil, excludeIDs: nil, want: []string{"radarr"}},
		{name: "excluded movie is skipped", tags: []int{9}, excludeIDs: map[int]struct{}{9: {}}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			video := tempVideo(t)
			metrics := &mockMetrics{}
			radarr := &mockHistoryPoller{
				movies: map[int]arrapi.Movie{30: {ID: 30, Title: "Film", Year: 2021, Tags: tt.tags}},
			}
			deps := fullDeps(&mockStore{})
			deps.Metrics = metrics
			cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
			engine := &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{Lang: "en", Kind: api.LangSearched, Searched: 1, Paths: []string{"/sub.srt"}}}, CoverageChanged: true}}
			ls := &LiveState{Cfg: cfg, Engine: engine, Radarr: radarr}
			p := &Poller{deps: deps, stateFunc: func() *LiveState { return ls }}

			entry := arrapi.HistoryRecord{MovieID: 30, Data: map[string]string{"importedPath": video}}
			p.processRadarrImport(context.Background(), ls, &entry, tt.excludeIDs)

			if !slices.Equal(metrics.imports, tt.want) {
				t.Errorf("metrics.imports = %v, want %v", metrics.imports, tt.want)
			}
		})
	}
}
