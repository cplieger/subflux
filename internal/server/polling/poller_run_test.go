package polling

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/server/events"
)

// --- Mock implementations ---

type mockMetrics struct {
	imports []string
}

func (m *mockMetrics) RecordImport(source api.PollKey) { m.imports = append(m.imports, string(source)) }

type mockEvents struct {
	published []events.Event
}

func (m *mockEvents) Publish(e events.Event) { m.published = append(m.published, e) }

type mockStatsCache struct {
	invalidated int
}

func (m *mockStatsCache) Invalidate() { m.invalidated++ }

type mockAlerts struct {
	warns []string
}

func (m *mockAlerts) RecordWarn(source, msg string) {
	m.warns = append(m.warns, source+": "+msg)
}

type mockStore struct {
	deletedPaths [][]string
}

func (m *mockStore) DeleteStateByPaths(_ context.Context, paths []string) (api.CleanupResult, error) {
	m.deletedPaths = append(m.deletedPaths, paths)
	return api.CleanupResult{Paths: paths}, nil
}

type mockHistoryPoller struct {
	historyErr error
	series     map[int]*api.Series
	episodes   map[int]*api.Episode
	movies     map[int]*api.Movie
	excludeIDs map[int]struct{}
	history    []api.HistoryEntry
}

func (m *mockHistoryPoller) GetHistorySince(_ context.Context, _ time.Time, _ api.HistoryEventType) ([]api.HistoryEntry, error) {
	return m.history, m.historyErr
}
func (m *mockHistoryPoller) GetSeriesByID(_ context.Context, id int) (*api.Series, error) {
	return m.series[id], nil
}
func (m *mockHistoryPoller) GetEpisodeByID(_ context.Context, id int) (*api.Episode, error) {
	return m.episodes[id], nil
}
func (m *mockHistoryPoller) GetMovieByID(_ context.Context, id int) (*api.Movie, error) {
	return m.movies[id], nil
}
func (m *mockHistoryPoller) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return m.excludeIDs
}
func (m *mockHistoryPoller) RefreshSeries(_ context.Context, _ int) error { return nil }
func (m *mockHistoryPoller) RefreshMovie(_ context.Context, _ int) error  { return nil }

type mockCfg struct {
	targets  []api.SubtitleTarget
	langs    []string
	interval time.Duration
}

func (m *mockCfg) PollInterval() time.Duration                    { return m.interval }
func (m *mockCfg) Search() api.SearchConfig                       { return api.SearchConfig{ScanDelay: time.Millisecond} }
func (m *mockCfg) ValidatePath(_ context.Context, _ string) error { return nil }
func (m *mockCfg) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return m.targets
}
func (m *mockCfg) LanguageCodes() []string { return m.langs }

type mockEngine struct {
	err    error
	result api.SearchResult
}

func (m *mockEngine) SearchTargets(_ context.Context, _ *api.SearchRequest, _ string, _ []api.SubtitleTarget) (api.SearchResult, error) {
	return m.result, m.err
}
func (m *mockEngine) ProviderTimeouts() (map[api.ProviderID]api.TimeoutStatus, bool) {
	return nil, false
}
func (m *mockEngine) ResetTimeouts() {}
func (m *mockEngine) SimulateScore(_ api.MediaType, _, _ string, _ api.MatchMethod) api.ScoreResult {
	return api.ScoreResult{}
}
func (m *mockEngine) ScoreSubtitles(_ *api.SearchRequest, _ []api.Subtitle) []api.ScoredResult {
	return nil
}
func (m *mockEngine) SyncAndPostProcess(_ context.Context, data []byte, _, _ string, _ api.Variant) ([]byte, int64) {
	return data, 0
}
func (m *mockEngine) HashFile(_ context.Context, _ string) (string, int64, error) {
	return "", 0, nil
}

func newTestPollCache() *PollCache {
	return NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
}

// --- Tests ---

func TestPollOnce_sonarr_nil_radarr_nil(t *testing.T) {
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      &mockStore{},
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
	// Should not panic with nil sonarr/radarr.
	p.PollOnce(context.Background())
}

func TestPollOnce_sonarr_no_events(t *testing.T) {
	sonarr := &mockHistoryPoller{history: nil}
	metrics := &mockMetrics{}
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      &mockStore{},
		Metrics:    metrics,
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	p := &Poller{
		deps:      deps,
		stateFunc: func() *LiveState { return ls },
	}
	p.PollOnce(context.Background())
	if len(metrics.imports) != 0 {
		t.Errorf("expected 0 imports, got %d", len(metrics.imports))
	}
}

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

func TestProcessPollImport_search_success(t *testing.T) {
	tmp := t.TempDir()
	videoPath := filepath.Join(tmp, "video.mkv")
	if err := os.WriteFile(videoPath, []byte("fake"), 0644); err != nil {
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
	engine := &mockEngine{result: api.SearchResult{Paths: []string{"/sub.srt"}, CoverageChanged: true}}
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

// --- Adaptive-poll burst tests ---

func TestPollOnce_returns_zero_when_no_events(t *testing.T) {
	sonarr := &mockHistoryPoller{history: nil}
	radarr := &mockHistoryPoller{history: nil}
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      &mockStore{},
		Metrics:    &mockMetrics{},
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Radarr: radarr}
	p := &Poller{
		deps:      deps,
		stateFunc: func() *LiveState { return ls },
	}

	if n := p.PollOnce(context.Background()); n != 0 {
		t.Errorf("PollOnce with no events: got %d, want 0", n)
	}
}

func TestPollOnce_returns_entry_count_on_activity(t *testing.T) {
	now := time.Now().UTC()
	sonarr := &mockHistoryPoller{
		history: []api.HistoryEntry{
			{Date: now,
				Data: map[string]string{"importedPath": "/missing/path/a.mkv"}},
			{Date: now.Add(time.Second),
				Data: map[string]string{"importedPath": "/missing/path/b.mkv"}},
		},
	}
	deps := Deps{
		PollCache:  newTestPollCache(),
		Store:      &mockStore{},
		Metrics:    &mockMetrics{},
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	// Use NewPoller (rather than &Poller{...}) so the internal tagCache
	// is initialized; the entries reach getExcludeTagIDs which dereferences
	// tagCache.
	p := NewPoller(deps, func() *LiveState { return ls })

	// Both entries' paths are missing on disk and will skip out of
	// processPollImport; the count we care about is the entries-observed
	// count from the GetHistorySince response, not the imports-applied
	// count. Adaptive burst keys off the former.
	if n := p.PollOnce(context.Background()); n != 2 {
		t.Errorf("PollOnce with 2 sonarr entries: got %d, want 2", n)
	}
}

func TestBurstPollConstants_in_canonical_relationship(t *testing.T) {
	// Locks the burst constants into the doc-claimed relationship: the
	// burst interval is shorter than the burst window (otherwise burst
	// would expire during a single accelerated cycle), and the window is
	// long enough to span at least a few burst cycles.
	if burstPollInterval >= burstPollWindow {
		t.Errorf("burstPollInterval (%v) must be less than burstPollWindow (%v)",
			burstPollInterval, burstPollWindow)
	}
	if cycles := burstPollWindow / burstPollInterval; cycles < 4 {
		t.Errorf("burst window should span >=4 burst cycles, got %d", cycles)
	}
}
