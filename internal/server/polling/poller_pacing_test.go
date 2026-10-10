package polling

import (
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- request-aware pacing (the inter-entry scan delay keys on provider traffic) ---
// (tempVideo, the stat-passing fixture helper, lives in poller_import_test.go)

// queriedResult builds a SearchResult whose one language group issued n
// provider queries.
func queriedResult(n int) subflux.SearchResult {
	return subflux.SearchResult{Langs: []subflux.LangOutcome{{
		Lang: "en", Kind: subflux.LangSearched, Searched: 1, Queried: n,
	}}}
}

// An import reports queried=true only when the engine's search
// actually issued provider queries; every skip path reports false.
func TestImport_queried_follows_engine(t *testing.T) {
	buildOK := func(req *subflux.SearchRequest) func() (*resolvedImport, error) {
		return func() (*resolvedImport, error) {
			return &resolvedImport{Req: req, Source: pollSourceSonarr, Label: "x"}, nil
		}
	}

	t.Run("engine queried providers", func(t *testing.T) {
		path := tempVideo(t)
		ls := &LiveState{
			Cfg:    &mockCfg{langs: []string{"en"}},
			Engine: &mockEngine{result: queriedResult(2)},
		}
		p := &Poller{deps: fullDeps(&mockStore{})}
		req := &subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, ImdbID: "tt1"}
		if got := importOne(t.Context(), p, ls, path, buildOK(req), nil); got != (importResult{queried: true}) {
			t.Errorf("importOne() = %+v, want only queried", got)
		}
	})

	t.Run("search generated no provider traffic", func(t *testing.T) {
		path := tempVideo(t)
		ls := &LiveState{
			Cfg: &mockCfg{langs: []string{"en"}},
			Engine: &mockEngine{result: subflux.SearchResult{Langs: []subflux.LangOutcome{{
				Lang: "en", Kind: subflux.LangSkipped,
			}}}},
		}
		p := &Poller{deps: fullDeps(&mockStore{})}
		req := &subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, ImdbID: "tt1"}
		if importOne(t.Context(), p, ls, path, buildOK(req), nil).queried {
			t.Errorf("queried = true for a skipped-group result, want false")
		}
	})

	t.Run("gone file skips without querying", func(t *testing.T) {
		ls := &LiveState{Cfg: &mockCfg{langs: []string{"en"}}, Engine: &mockEngine{}}
		p := &Poller{deps: fullDeps(&mockStore{})}
		if got := importOne(t.Context(), p, ls,
			"/nonexistent/gone.mkv", buildOK(nil), nil); got != (importResult{}) {
			t.Errorf("gone file = %+v, want nothing set", got)
		}
	})
}

// Skip entries (gone files) pay no inter-entry delay: a batch of pure skips
// executes without sleeping. Before request-aware pacing this batch slept
// scanDelay after EVERY entry (here 2x400ms); the generous sub-delay bound
// only fails if a sleep actually happens.
func TestExecuteBatch_skip_entries_pay_no_delay(t *testing.T) {
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}, scanDelay: 400 * time.Millisecond}
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry("/nonexistent/a.mkv"),
		histEntry("/nonexistent/b.mkv"),
	}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Engine: &mockEngine{}}
	p := NewPoller(fullDeps(store), func() *LiveState { return ls })

	if got := p.detectSonarr(t.Context(), ls); got != 2 {
		t.Fatalf("detectSonarr = %d, want 2", got)
	}
	start := time.Now()
	drainOne(t, p)
	if elapsed := time.Since(start); elapsed >= cfg.scanDelay {
		t.Errorf("executeBatch of skip entries took %v, want < %v (no pacing sleep)", elapsed, cfg.scanDelay)
	}
	if len(store.deletedPaths) != 2 {
		t.Fatalf("executeBatch deletes = %d, want 2", len(store.deletedPaths))
	}
}

// Entries that actually query providers ARE paced: two working entries incur
// (at least) one inter-entry delay, and the pacing sleep must not cost the
// batch its remaining entries. Lower-bound on the delay only, so a loaded
// runner cannot flake it.
func TestExecuteBatch_paces_between_querying_entries(t *testing.T) {
	const delay = 60 * time.Millisecond
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}, scanDelay: delay}
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry(tempVideo(t)),
		histEntry(tempVideo(t)),
	}}
	engine := &mockEngine{result: queriedResult(1)}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Engine: engine}
	p := NewPoller(fullDeps(store), func() *LiveState { return ls })

	if got := p.detectSonarr(t.Context(), ls); got != 2 {
		t.Fatalf("detectSonarr = %d, want 2", got)
	}
	start := time.Now()
	drainOne(t, p)
	if elapsed := time.Since(start); elapsed < delay {
		t.Errorf("executeBatch of querying entries took %v, want >= %v (one pacing sleep)", elapsed, delay)
	}
	if got := engine.searches.Load(); got != 2 {
		t.Errorf("engine searches = %d, want 2 (the batch must survive the pacing sleep)", got)
	}
}
