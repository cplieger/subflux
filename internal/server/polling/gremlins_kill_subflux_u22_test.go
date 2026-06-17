package polling

// Mutation-killing tests for unit subflux-u22 (package internal/server/polling).
// Tests only; all new identifiers are prefixed gk_subflux_u22_. Existing mocks
// from poller_run_test.go (mockCfg/mockEngine/mockStore/mockHistoryPoller/
// mockMetrics/mockEvents/mockStatsCache/mockAlerts/newTestPollCache) are reused.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// gk_subflux_u22_ttlProbeDelay is longer than the "short" TTLs under test
// (2ms) and far shorter than the "long" TTLs (>=4m), so a cache entry's
// presence after this delay deterministically reflects the configured TTL.
const gk_subflux_u22_ttlProbeDelay = 5 * time.Millisecond

// --- log capture (for slog.Warn-only observable branches) ---

type gk_subflux_u22_logLine struct {
	msg   string
	level slog.Level
}

type gk_subflux_u22_logSink struct {
	lines []gk_subflux_u22_logLine
	mu    sync.Mutex
}

func (s *gk_subflux_u22_logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *gk_subflux_u22_logSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, gk_subflux_u22_logLine{level: r.Level, msg: r.Message})
	return nil
}

func (s *gk_subflux_u22_logSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *gk_subflux_u22_logSink) WithGroup(string) slog.Handler      { return s }

func (s *gk_subflux_u22_logSink) gk_subflux_u22_has(level slog.Level, msg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if l.level == level && l.msg == msg {
			return true
		}
	}
	return false
}

// gk_subflux_u22_captureLogs installs a recording slog default for the test and
// restores the previous default on cleanup. Callers must NOT use t.Parallel()
// (the default logger is process-global).
func gk_subflux_u22_captureLogs(t *testing.T) *gk_subflux_u22_logSink {
	t.Helper()
	sink := &gk_subflux_u22_logSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return sink
}

// --- fakes ---

// gk_subflux_u22_errStore is a PollerStore whose DeleteStateByPaths always errs.
type gk_subflux_u22_errStore struct{}

func (gk_subflux_u22_errStore) DeleteStateByPaths(_ context.Context, _ []string) (api.CleanupResult, error) {
	return api.CleanupResult{}, errors.New("delete boom")
}

// gk_subflux_u22_noopStore is a stateless (race-free) PollerStore for concurrent
// PollOnce tests where store side effects are not asserted.
type gk_subflux_u22_noopStore struct{}

func (gk_subflux_u22_noopStore) DeleteStateByPaths(_ context.Context, paths []string) (api.CleanupResult, error) {
	return api.CleanupResult{Paths: paths}, nil
}

// gk_subflux_u22_fakePoller counts ResolveExcludeTagIDs calls and returns a
// configurable result. It embeds *mockHistoryPoller for the other 6 methods.
type gk_subflux_u22_fakePoller struct {
	*mockHistoryPoller
	result map[int]struct{}
	calls  atomic.Int32
}

func (f *gk_subflux_u22_fakePoller) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	f.calls.Add(1)
	return f.result
}

// --- small builders ---

func gk_subflux_u22_entry(path string) api.HistoryEntry {
	return api.HistoryEntry{Date: time.Now().UTC(), Data: map[string]string{"importedPath": path}}
}

func gk_subflux_u22_tempVideo(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) = %v", p, err)
	}
	return p
}

func gk_subflux_u22_fullDeps(store PollerStore) Deps {
	return Deps{
		PollCache:  newTestPollCache(),
		Store:      store,
		Metrics:    &mockMetrics{},
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
}

func gk_subflux_u22_importPoller(engine api.SearchEngine) (*Poller, *LiveState) {
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Engine: engine}
	return &Poller{deps: gk_subflux_u22_fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}, ls
}

func gk_subflux_u22_movieResult() (*ImportResult, error) {
	return &ImportResult{
		Req:       &api.SearchRequest{MediaType: api.MediaTypeMovie, Title: "T"},
		Label:     "T (2024)",
		Source:    PollSourceRadarr,
		RefreshID: 7,
	}, nil
}

// ============================================================================
// pollcache.go:76:38 CONDITIONALS_NEGATION  (err != nil in PollCache.Set)
// Observable: slog.Warn "PollCache: write failed" only fires when setFn errs.
// ============================================================================

func Test_gk_subflux_u22_PollCacheSet_warns_when_setFn_errors(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return errors.New("db boom") },
	)
	pc.Set(context.Background(), api.PollKeySonarr, time.Now())
	if !sink.gk_subflux_u22_has(slog.LevelWarn, "PollCache: write failed") {
		t.Errorf("Set with failing setFn: want WARN 'PollCache: write failed' (mutant '==nil' suppresses it)")
	}
}

func Test_gk_subflux_u22_PollCacheSet_silent_when_setFn_ok(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
	pc.Set(context.Background(), api.PollKeySonarr, time.Now())
	if sink.gk_subflux_u22_has(slog.LevelWarn, "PollCache: write failed") {
		t.Errorf("Set with ok setFn: unexpected WARN 'PollCache: write failed' (mutant '==nil' emits it)")
	}
}

// ============================================================================
// poller_import.go:22:80 CONDITIONALS_NEGATION  (delErr != nil in cleanup path)
// Observable: slog.Warn "poll: cleanup failed" only fires when delete errs.
// ============================================================================

func Test_gk_subflux_u22_processPollImport_warns_when_cleanup_errors(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: gk_subflux_u22_fullDeps(gk_subflux_u22_errStore{}), stateFunc: func() *LiveState { return ls }}
	p.processPollImport(context.Background(), ls, "/nonexistent/u22-22.mkv",
		func() (*ImportResult, error) { t.Fatal("buildFn must not run for a missing file"); return nil, nil },
		nil)
	if !sink.gk_subflux_u22_has(slog.LevelWarn, "poll: cleanup failed") {
		t.Errorf("cleanup error: want WARN 'poll: cleanup failed' (mutant '==nil' suppresses it)")
	}
}

func Test_gk_subflux_u22_processPollImport_silent_when_cleanup_ok(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	cfg := &mockCfg{interval: time.Second, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: gk_subflux_u22_fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.processPollImport(context.Background(), ls, "/nonexistent/u22-22b.mkv",
		func() (*ImportResult, error) { t.Fatal("buildFn must not run for a missing file"); return nil, nil },
		nil)
	if sink.gk_subflux_u22_has(slog.LevelWarn, "poll: cleanup failed") {
		t.Errorf("cleanup ok: unexpected WARN 'poll: cleanup failed' (mutant '==nil' emits it)")
	}
}

// ============================================================================
// poller_import.go:66:30 (BOUNDARY + NEGATION) and 66:47 (NEGATION)
//   if len(searchResult.Paths) > 0 && refreshFn != nil { ... }
// Observable: whether refreshFn is invoked.
//   - paths present + refreshFn!=nil  -> called  (kills 66:30 NEG, 66:47 NEG)
//   - paths absent  + coverage changed -> NOT called (kills 66:30 BOUNDARY)
// ============================================================================

func Test_gk_subflux_u22_processPollImport_calls_refreshFn_when_paths_present(t *testing.T) {
	video := gk_subflux_u22_tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Paths: []string{"/x.srt"}, CoverageChanged: false}}
	p, ls := gk_subflux_u22_importPoller(engine)
	calls := 0
	p.processPollImport(context.Background(), ls, video,
		gk_subflux_u22_movieResult,
		func(_ context.Context, _ int) error { calls++; return nil })
	if calls != 1 {
		t.Errorf("refreshFn calls = %d, want 1 (paths present + refreshFn!=nil must invoke it)", calls)
	}
}

func Test_gk_subflux_u22_processPollImport_skips_refreshFn_when_no_paths(t *testing.T) {
	video := gk_subflux_u22_tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Paths: nil, CoverageChanged: true}}
	p, ls := gk_subflux_u22_importPoller(engine)
	calls := 0
	p.processPollImport(context.Background(), ls, video,
		gk_subflux_u22_movieResult,
		func(_ context.Context, _ int) error { calls++; return nil })
	if calls != 0 {
		t.Errorf("refreshFn calls = %d, want 0 (no downloaded paths must not refresh even when coverage changed)", calls)
	}
}

// ============================================================================
// poller_import.go:67:52 CONDITIONALS_NEGATION  (err != nil after refreshFn)
// Observable: slog.Warn "failed to notify arr" only fires when refreshFn errs.
// ============================================================================

func Test_gk_subflux_u22_processPollImport_warns_when_refresh_errors(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	video := gk_subflux_u22_tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Paths: []string{"/x.srt"}, CoverageChanged: false}}
	p, ls := gk_subflux_u22_importPoller(engine)
	p.processPollImport(context.Background(), ls, video,
		gk_subflux_u22_movieResult,
		func(_ context.Context, _ int) error { return errors.New("notify boom") })
	if !sink.gk_subflux_u22_has(slog.LevelWarn, "failed to notify arr") {
		t.Errorf("refreshFn error: want WARN 'failed to notify arr' (mutant '==nil' suppresses it)")
	}
}

func Test_gk_subflux_u22_processPollImport_silent_when_refresh_ok(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	video := gk_subflux_u22_tempVideo(t)
	engine := &mockEngine{result: api.SearchResult{Paths: []string{"/x.srt"}, CoverageChanged: false}}
	p, ls := gk_subflux_u22_importPoller(engine)
	p.processPollImport(context.Background(), ls, video,
		gk_subflux_u22_movieResult,
		func(_ context.Context, _ int) error { return nil })
	if sink.gk_subflux_u22_has(slog.LevelWarn, "failed to notify arr") {
		t.Errorf("refreshFn ok: unexpected WARN 'failed to notify arr' (mutant '==nil' emits it)")
	}
}

// ============================================================================
// poller_run.go NewPoller TTL computation:
//   72:32 ARITHMETIC_BASE  (2 * time.Minute in defaultPollInterval)
//   73:11 ARITHMETIC_BASE  (2 * defaultPollInterval)
//   74:27 CONDITIONALS_NEGATION (ls != nil)
//   74:44 CONDITIONALS_NEGATION (ls.Cfg != nil)
//   75:11 ARITHMETIC_BASE  (2 * ls.Cfg.PollInterval())
// Observable: the tagCache TTL, via getExcludeTagIDs re-fetch behaviour after a
// fixed probe delay. ttl==0 -> entry expires immediately -> 2 fetches; a TTL
// larger than the probe delay -> cached -> 1 fetch.
// ============================================================================

// Default branch (nil Cfg): original ttl = 2*defaultPollInterval = 4m (cached).
// Mutants 72:32 / 73:11 -> ttl 0 -> refetch. Asserting 1 fetch kills both.
func Test_gk_subflux_u22_NewPoller_defaultBranch_caches_tags(t *testing.T) {
	fake := &gk_subflux_u22_fakePoller{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "u22-default", nil, 0)
	time.Sleep(gk_subflux_u22_ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "u22-default", nil, 0)
	if got := fake.calls.Load(); got != 1 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 1 (default-branch ttl=4m must cache; mutant ttl=0 refetches)", got)
	}
}

// Configured branch with a tiny interval: original ttl = 2*1ms = 2ms (expires).
// Mutants 74:27 / 74:44 divert to the default branch (ttl=4m, cached -> 1).
// Asserting 2 fetches kills both negations.
func Test_gk_subflux_u22_NewPoller_configuredBranch_shortTTL_expires(t *testing.T) {
	fake := &gk_subflux_u22_fakePoller{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	cfg := &mockCfg{interval: time.Millisecond}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "u22-short", nil, 0)
	time.Sleep(gk_subflux_u22_ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "u22-short", nil, 0)
	if got := fake.calls.Load(); got != 2 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 2 (configured ttl=2ms expires before %v; mutant default-branch ttl=4m caches)", got, gk_subflux_u22_ttlProbeDelay)
	}
}

// Configured branch with a large interval: original ttl = 2*1h = 2h (cached).
// Mutant 75:11 (2/PollInterval = 0) -> ttl 0 -> refetch. Asserting 1 fetch kills it.
func Test_gk_subflux_u22_NewPoller_configuredBranch_longTTL_caches(t *testing.T) {
	fake := &gk_subflux_u22_fakePoller{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{}}
	cfg := &mockCfg{interval: time.Hour}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ctx := context.Background()
	p.getExcludeTagIDs(ctx, fake, "u22-long", nil, 0)
	time.Sleep(gk_subflux_u22_ttlProbeDelay)
	p.getExcludeTagIDs(ctx, fake, "u22-long", nil, 0)
	if got := fake.calls.Load(); got != 1 {
		t.Errorf("ResolveExcludeTagIDs calls = %d, want 1 (configured ttl=2h must cache; mutant ttl=0 refetches)", got)
	}
}

// ============================================================================
// poller_run.go:151:26 CONDITIONALS_NEGATION  (err != nil after g.Wait)
// poller_run.go:155:35 CONDITIONALS_NEGATION  (dur > PollInterval)
// Observable: presence/absence of the two PollOnce cycle warnings.
// ============================================================================

func Test_gk_subflux_u22_PollOnce_no_spurious_warns_within_interval(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg} // nil arrs: g.Wait() returns nil, cycle is fast
	p := &Poller{deps: gk_subflux_u22_fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.PollOnce(context.Background())
	if sink.gk_subflux_u22_has(slog.LevelWarn, "poll cycle error") {
		t.Errorf("unexpected WARN 'poll cycle error' (mutant '==nil' on g.Wait makes it always fire)")
	}
	if sink.gk_subflux_u22_has(slog.LevelWarn, "poll cycle exceeded interval") {
		t.Errorf("unexpected WARN 'poll cycle exceeded interval' (mutant 'dur <= interval' fires within interval)")
	}
}

func Test_gk_subflux_u22_PollOnce_warns_when_exceeds_interval(t *testing.T) {
	sink := gk_subflux_u22_captureLogs(t)
	cfg := &mockCfg{interval: 0, langs: []string{"en"}} // any elapsed dur > 0 exceeds
	ls := &LiveState{Cfg: cfg}
	p := &Poller{deps: gk_subflux_u22_fullDeps(&mockStore{}), stateFunc: func() *LiveState { return ls }}
	p.PollOnce(context.Background())
	if !sink.gk_subflux_u22_has(slog.LevelWarn, "poll cycle exceeded interval") {
		t.Errorf("want WARN 'poll cycle exceeded interval' with 0 interval (mutant 'dur <= 0' suppresses it)")
	}
}

// ============================================================================
// poller_run.go:161:33 ARITHMETIC_BASE  (sonarrCount + radarrCount)
// Observable: PollOnce return value. 2 + 1 = 3; mutant 2 - 1 = 1.
// ============================================================================

func Test_gk_subflux_u22_PollOnce_returns_sum_of_arr_counts(t *testing.T) {
	sonarr := &mockHistoryPoller{history: []api.HistoryEntry{
		gk_subflux_u22_entry("/nonexistent/u22-s1.mkv"),
		gk_subflux_u22_entry("/nonexistent/u22-s2.mkv"),
	}}
	radarr := &mockHistoryPoller{history: []api.HistoryEntry{
		gk_subflux_u22_entry("/nonexistent/u22-r1.mkv"),
	}}
	// noopStore is stateless so the concurrent Sonarr+Radarr goroutines don't race.
	deps := gk_subflux_u22_fullDeps(gk_subflux_u22_noopStore{})
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Radarr: radarr}
	p := NewPoller(deps, func() *LiveState { return ls })
	if got := p.PollOnce(context.Background()); got != 3 {
		t.Errorf("PollOnce() = %d, want 3 (sonarr 2 + radarr 1; mutant '-' gives 1)", got)
	}
}

// ============================================================================
// poller_run.go:172:9 CONDITIONALS_NEGATION  (err != nil in getExcludeTagIDs)
// Observable: a successful fetch returns the resolved ids; mutant returns nil.
// ============================================================================

func Test_gk_subflux_u22_getExcludeTagIDs_returns_ids_on_success(t *testing.T) {
	fake := &gk_subflux_u22_fakePoller{mockHistoryPoller: &mockHistoryPoller{}, result: map[int]struct{}{42: {}}}
	cfg := &mockCfg{interval: time.Hour}
	p := NewPoller(Deps{}, func() *LiveState { return &LiveState{Cfg: cfg} })
	ids := p.getExcludeTagIDs(context.Background(), fake, "u22-172", nil, 0)
	if ids == nil {
		t.Fatalf("getExcludeTagIDs on success returned nil (mutant '==nil' returns nil instead of the ids)")
	}
	if _, ok := ids[42]; !ok || len(ids) != 1 {
		t.Errorf("getExcludeTagIDs = %v, want map[42:{}]", ids)
	}
}

// ============================================================================
// poller_run.go:208:11 CONDITIONALS_NEGATION  (path == "" in pollSonarr loop)
// Observable: a non-empty path is processed (DeleteStateByPaths for the missing
// file); mutant '!=' skips non-empty paths so nothing is processed.
// ============================================================================

func Test_gk_subflux_u22_pollSonarr_processes_nonEmpty_path(t *testing.T) {
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	sonarr := &mockHistoryPoller{history: []api.HistoryEntry{gk_subflux_u22_entry("/nonexistent/u22-208.mkv")}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	p := NewPoller(gk_subflux_u22_fullDeps(store), func() *LiveState { return ls })
	p.pollSonarr(context.Background(), ls)
	if len(store.deletedPaths) != 1 {
		t.Fatalf("pollSonarr deletes = %d, want 1 (mutant '!=\"\"' skips non-empty paths -> 0)", len(store.deletedPaths))
	}
	if store.deletedPaths[0][0] != "/nonexistent/u22-208.mkv" {
		t.Errorf("deleted path = %q, want /nonexistent/u22-208.mkv", store.deletedPaths[0][0])
	}
}

// ============================================================================
// poller_run.go:215:52 CONDITIONALS_NEGATION  (err != nil after SleepCtx)
// Observable: with a non-cancelled context (SleepCtx returns nil) the loop
// continues to every entry; mutant '== nil' returns after the first entry.
// Two missing-file entries => 2 deletes; mutant => 1.
// ============================================================================

func Test_gk_subflux_u22_pollSonarr_continues_after_each_entry(t *testing.T) {
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	sonarr := &mockHistoryPoller{history: []api.HistoryEntry{
		gk_subflux_u22_entry("/nonexistent/u22-215a.mkv"),
		gk_subflux_u22_entry("/nonexistent/u22-215b.mkv"),
	}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr}
	p := NewPoller(gk_subflux_u22_fullDeps(store), func() *LiveState { return ls })
	p.pollSonarr(context.Background(), ls)
	if len(store.deletedPaths) != 2 {
		t.Fatalf("pollSonarr deletes = %d, want 2 (mutant '== nil' returns after the first entry -> 1)", len(store.deletedPaths))
	}
}

// ============================================================================
// poller_run.go:232:9 CONDITIONALS_NEGATION  (err != nil after GetHistorySince
// in pollRadarr). Observable: a successful history fetch is processed and the
// entry count is returned; mutant '== nil' treats success as failure -> 0.
// ============================================================================

func Test_gk_subflux_u22_pollRadarr_processes_on_success(t *testing.T) {
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}}
	radarr := &mockHistoryPoller{history: []api.HistoryEntry{gk_subflux_u22_entry("/nonexistent/u22-232.mkv")}}
	ls := &LiveState{Cfg: cfg, Radarr: radarr}
	p := NewPoller(gk_subflux_u22_fullDeps(&mockStore{}), func() *LiveState { return ls })
	if got := p.pollRadarr(context.Background(), ls); got != 1 {
		t.Errorf("pollRadarr on success = %d, want 1 (mutant '== nil' treats success as error -> early return 0)", got)
	}
}
