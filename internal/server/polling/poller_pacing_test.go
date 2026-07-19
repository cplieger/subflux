package polling

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// --- request-aware pacing (the inter-entry scan delay keys on provider traffic) ---
// (tempVideo, the stat-passing fixture helper, lives in poller_import_test.go)

// queriedResult builds a SearchResult whose one language group issued n
// provider queries.
func queriedResult(n int) api.SearchResult {
	return api.SearchResult{Langs: []api.LangOutcome{{
		Lang: "en", Kind: api.LangSearched, Searched: 1, Queried: n,
	}}}
}

// processPollImport reports queried=true only when the engine's search
// actually issued provider queries; every skip path reports false.
func TestProcessPollImport_queried_follows_engine(t *testing.T) {
	buildOK := func(req *api.SearchRequest) func() (*ImportResult, error) {
		return func() (*ImportResult, error) {
			return &ImportResult{Req: req, Source: PollSourceSonarr, Label: "x"}, nil
		}
	}

	t.Run("engine queried providers", func(t *testing.T) {
		path := tempVideo(t)
		ls := &LiveState{
			Cfg:    &mockCfg{langs: []string{"en"}},
			Engine: &mockEngine{result: queriedResult(2)},
		}
		p := &Poller{deps: fullDeps(&mockStore{})}
		req := &api.SearchRequest{MediaType: api.MediaTypeEpisode, ImdbID: "tt1"}
		retryable, queried := p.processPollImport(context.Background(), ls, path, buildOK(req), nil)
		if retryable || !queried {
			t.Errorf("processPollImport = (retryable %v, queried %v), want (false, true)", retryable, queried)
		}
	})

	t.Run("search generated no provider traffic", func(t *testing.T) {
		path := tempVideo(t)
		ls := &LiveState{
			Cfg: &mockCfg{langs: []string{"en"}},
			Engine: &mockEngine{result: api.SearchResult{Langs: []api.LangOutcome{{
				Lang: "en", Kind: api.LangSkipped, Skipped: 1,
			}}}},
		}
		p := &Poller{deps: fullDeps(&mockStore{})}
		req := &api.SearchRequest{MediaType: api.MediaTypeEpisode, ImdbID: "tt1"}
		_, queried := p.processPollImport(context.Background(), ls, path, buildOK(req), nil)
		if queried {
			t.Errorf("queried = true for a skipped-group result, want false")
		}
	})

	t.Run("gone file skips without querying", func(t *testing.T) {
		ls := &LiveState{Cfg: &mockCfg{langs: []string{"en"}}, Engine: &mockEngine{}}
		p := &Poller{deps: fullDeps(&mockStore{})}
		retryable, queried := p.processPollImport(context.Background(), ls,
			"/nonexistent/gone.mkv", buildOK(nil), nil)
		if retryable || queried {
			t.Errorf("gone file = (retryable %v, queried %v), want (false, false)", retryable, queried)
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

	if got := p.detectSonarr(context.Background(), ls); got != 2 {
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
// (at least) one inter-entry delay. Lower-bound only, so a loaded runner
// cannot flake it.
func TestExecuteBatch_paces_between_querying_entries(t *testing.T) {
	const delay = 60 * time.Millisecond
	store := &mockStore{}
	cfg := &mockCfg{interval: time.Hour, langs: []string{"en"}, scanDelay: delay}
	sonarr := &mockHistoryPoller{history: []arrapi.HistoryRecord{
		histEntry(tempVideo(t)),
		histEntry(tempVideo(t)),
	}}
	ls := &LiveState{Cfg: cfg, Sonarr: sonarr, Engine: &mockEngine{result: queriedResult(1)}}
	p := NewPoller(fullDeps(store), func() *LiveState { return ls })

	if got := p.detectSonarr(context.Background(), ls); got != 2 {
		t.Fatalf("detectSonarr = %d, want 2", got)
	}
	start := time.Now()
	drainOne(t, p)
	if elapsed := time.Since(start); elapsed < delay {
		t.Errorf("executeBatch of querying entries took %v, want >= %v (one pacing sleep)", elapsed, delay)
	}
}
