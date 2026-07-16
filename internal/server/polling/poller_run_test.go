package polling

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
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
	series     map[int]arrapi.Series
	episodes   map[int]arrapi.Episode
	movies     map[int]arrapi.Movie
	excludeIDs map[int]struct{}
	history    []arrapi.HistoryRecord
}

func (m *mockHistoryPoller) GetHistorySince(_ context.Context, _ time.Time, _ ...arrapi.EventType) ([]arrapi.HistoryRecord, error) {
	return m.history, m.historyErr
}

func (m *mockHistoryPoller) GetSeriesByID(_ context.Context, id int) (arrapi.Series, error) {
	return m.series[id], nil
}

func (m *mockHistoryPoller) GetEpisodeByID(_ context.Context, id int) (arrapi.Episode, error) {
	return m.episodes[id], nil
}

func (m *mockHistoryPoller) GetMovieByID(_ context.Context, id int) (arrapi.Movie, error) {
	return m.movies[id], nil
}

func (m *mockHistoryPoller) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	return m.excludeIDs
}
func (m *mockHistoryPoller) RescanSeries(_ context.Context, _ int) error { return nil }
func (m *mockHistoryPoller) RescanMovie(_ context.Context, _ int) error  { return nil }

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

func (m *mockEngine) ProviderTimeouts() (map[api.ProviderID]api.ProviderStatus, bool) {
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

// --- poll-cycle test helpers ---

// ttlProbeDelay is longer than the "short" tag-cache TTLs under test (2ms) and
// far shorter than the "long" ones (>=4m), so an entry's presence after this
// delay deterministically reflects the configured TTL.
const ttlProbeDelay = 5 * time.Millisecond

// noopStore is a stateless (race-free) PollerStore for the concurrent PollOnce
// test where store side effects are not asserted.
type noopStore struct{}

func (noopStore) DeleteStateByPaths(_ context.Context, paths []string) (api.CleanupResult, error) {
	return api.CleanupResult{Paths: paths}, nil
}

// countingExcludeResolver embeds *mockHistoryPoller and counts ResolveExcludeTagIDs
// calls, used to assert the poller's tag-cache TTL behaviour.
type countingExcludeResolver struct {
	*mockHistoryPoller
	result map[int]struct{}
	calls  atomic.Int32
}

func (f *countingExcludeResolver) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	f.calls.Add(1)
	return f.result
}

// histEntry builds a history entry with the given imported path and the current time.
func histEntry(path string) arrapi.HistoryRecord {
	return arrapi.HistoryRecord{Date: time.Now().UTC(), Data: map[string]string{"importedPath": path}}
}

// --- PollOnce smoke tests ---

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
		history: []arrapi.HistoryRecord{
			{
				Date: now,
				Data: map[string]string{"importedPath": "/missing/path/a.mkv"},
			},
			{
				Date: now.Add(time.Second),
				Data: map[string]string{"importedPath": "/missing/path/b.mkv"},
			},
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

// --- NewPoller tag-cache TTL derivation ---

// With a nil Cfg, NewPoller falls back to a default tag-cache TTL
// (2*defaultPollInterval = 4m), so resolved exclude tags stay cached.
func TestNewPoller_defaultTTL_caches_tags(t *testing.T) {
	fake := &countingExcludeResolver{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "default", nil, 0)
	time.Sleep(ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "default", nil, 0)
	if got := fake.calls.Load(); got != 1 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 1 (default-branch ttl=4m must cache)", got)
	}
}

// A configured short PollInterval yields a short tag-cache TTL (2*1ms) that
// expires before the probe delay, forcing a re-fetch.
func TestNewPoller_shortInterval_TTL_expires(t *testing.T) {
	fake := &countingExcludeResolver{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	cfg := &mockCfg{interval: time.Millisecond}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "short", nil, 0)
	time.Sleep(ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "short", nil, 0)
	if got := fake.calls.Load(); got != 2 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 2 (ttl=2ms expires before %v)", got, ttlProbeDelay)
	}
}

// A configured long PollInterval yields a long tag-cache TTL (2*1h) that keeps
// resolved exclude tags cached across the probe delay.
func TestNewPoller_longInterval_TTL_caches(t *testing.T) {
	fake := &countingExcludeResolver{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	cfg := &mockCfg{interval: time.Hour}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "long", nil, 0)
	time.Sleep(ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "long", nil, 0)
	if got := fake.calls.Load(); got != 1 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 1 (ttl=2h must cache)", got)
	}
}

// --- PollOnce cycle observability ---

// Within the poll interval and with no arr clients, PollOnce emits neither the
// "poll cycle error" nor the "poll cycle exceeded interval" WARN.
func TestPollOnce_no_spurious_warns_within_interval(t *testing.T) {
	sink := capture.Default(t)
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg} // nil arrs: g.Wait() returns nil, cycle is fast
	p := &Poller{deps: fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.PollOnce(context.Background())
	if hasRecord(sink, slog.LevelWarn, "poll cycle error") {
		t.Errorf("unexpected WARN 'poll cycle error' for a clean cycle")
	}
	if hasRecord(sink, slog.LevelWarn, "poll cycle exceeded interval") {
		t.Errorf("unexpected WARN 'poll cycle exceeded interval' within the interval")
	}
}

// A cycle whose duration exceeds the (zero) poll interval emits the exceeded WARN.
func TestPollOnce_warns_when_exceeds_interval(t *testing.T) {
	sink := capture.Default(t)
	cfg := &mockCfg{interval: 0, langs: []string{"en"}} // any elapsed dur > 0 exceeds
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.PollOnce(context.Background())
	if !hasRecord(sink, slog.LevelWarn, "poll cycle exceeded interval") {
		t.Errorf("want WARN 'poll cycle exceeded interval' with a 0 interval")
	}
}

// PollOnce returns the sum of imported-history entries seen across Sonarr and Radarr.
func TestPollOnce_returns_sum_of_arr_counts(t *testing.T) {
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry("/nonexistent/s1.mkv"),
		histEntry("/nonexistent/s2.mkv"),
	}}
	radarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry("/nonexistent/r1.mkv"),
	}}
	// noopStore is stateless so the concurrent Sonarr+Radarr goroutines don't race.
	deps := fullDeps(noopStore{})
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Radarr: radarr}
	p := NewPoller(deps, func() *LiveState { return ls })
	if got := p.PollOnce(context.Background()); got != 3 {
		t.Errorf("PollOnce() = %d, want 3 (sonarr 2 + radarr 1)", got)
	}
}

// --- getExcludeTagIDs ---

// getExcludeTagIDs returns the resolved IDs on a successful fetch.
func TestGetExcludeTagIDs_returns_ids_on_success(t *testing.T) {
	fake := &countingExcludeResolver{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{42: {}}}
	cfg := &mockCfg{interval: time.Hour}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ids := p.getExcludeTagIDs(context.Background(), fake, "ok", nil, 0)
	if ids == nil {
		t.Fatalf("getExcludeTagIDs on success returned nil")
	}
	if _, ok := ids[42]; !ok || len(ids) != 1 {
		t.Errorf("getExcludeTagIDs = %v, want map[42:{}]", ids)
	}
}

// --- pollSonarr / pollRadarr entry processing ---

// pollSonarr processes each non-empty imported path; a missing file triggers a
// DeleteStateByPaths cleanup.
func TestPollSonarr_processes_nonEmpty_path(t *testing.T) {
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{histEntry("/nonexistent/one.mkv")}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	p := NewPoller(fullDeps(store), func() *LiveState { return ls })
	p.pollSonarr(context.Background(), ls)
	if len(store.deletedPaths) != 1 {
		t.Fatalf("pollSonarr deletes = %d, want 1", len(store.deletedPaths))
	}
	if store.deletedPaths[0][0] != "/nonexistent/one.mkv" {
		t.Errorf("deleted path = %q, want /nonexistent/one.mkv", store.deletedPaths[0][0])
	}
}

// pollSonarr continues to every entry (it does not stop after the first) when
// the context is not cancelled. Two missing-file entries => two cleanups.
func TestPollSonarr_continues_after_each_entry(t *testing.T) {
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry("/nonexistent/a.mkv"),
		histEntry("/nonexistent/b.mkv"),
	}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	p := NewPoller(fullDeps(store), func() *LiveState { return ls })
	p.pollSonarr(context.Background(), ls)
	if len(store.deletedPaths) != 2 {
		t.Fatalf("pollSonarr deletes = %d, want 2", len(store.deletedPaths))
	}
}

// pollRadarr processes a successful history fetch and returns the entry count.
func TestPollRadarr_processes_on_success(t *testing.T) {
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	radarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{histEntry("/nonexistent/movie.mkv")}}
	ls := &LiveState{Cfg: cfg, Radarr: radarr}
	p := NewPoller(fullDeps(&mockStore{}), func() *LiveState { return ls })
	if got := p.pollRadarr(context.Background(), ls); got != 1 {
		t.Errorf("pollRadarr on success = %d, want 1", got)
	}
}
