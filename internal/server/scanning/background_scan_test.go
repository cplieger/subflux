package scanning

// Background-scan accept/stop tests (S12): 202 + activity_id at accept time,
// idempotent same-scope starts, the graceful stop signal honoured BETWEEN
// items (the item in flight always completes), the queued-cancel terminal
// fix, request-context detachment vs server-context shutdown, and stop
// registration release on every terminal transition.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/testsupport"
)

// --- fakes ---

// fakeEngine is a controllable api.SearchEngine: SearchTargets counts calls
// and, when the gate channels are set, signals each call's ordinal on
// `started` and blocks until `release` yields — letting tests stop a scan
// while an item is provably IN FLIGHT.
type fakeEngine struct {
	started chan int
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (e *fakeEngine) SearchTargets(_ context.Context, _ *api.SearchRequest, _ string, _ []api.SubtitleTarget) (api.SearchResult, error) {
	e.mu.Lock()
	e.calls++
	n := e.calls
	e.mu.Unlock()
	if e.started != nil {
		e.started <- n
	}
	if e.release != nil {
		<-e.release
	}
	return api.SearchResult{}, nil
}

func (e *fakeEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *fakeEngine) InventoryCoverage(_ context.Context, _ *api.SearchRequest, _ string) bool {
	return false
}

func (e *fakeEngine) ProviderTimeouts() (map[api.ProviderID]api.ProviderStatus, bool) {
	return nil, false
}
func (e *fakeEngine) ResetTimeouts() {}
func (e *fakeEngine) SimulateScore(_ api.MediaType, _, _ string, _ api.MatchMethod) api.ScoreResult {
	return api.ScoreResult{}
}

func (e *fakeEngine) ScoreSubtitles(_ *api.SearchRequest, _ []api.Subtitle) []api.ScoredResult {
	return nil
}

func (e *fakeEngine) SyncAndPostProcess(_ context.Context, data []byte, _, _ string, _ api.Variant) ([]byte, int64) {
	return data, 0
}

func (e *fakeEngine) HashFile(_ context.Context, _ string) (string, int64, error) {
	return "", 0, nil
}

var _ api.SearchEngine = (*fakeEngine)(nil)

// recEvents records the scan SSE publications.
type scanEvt struct {
	action  string
	detail  string
	actID   string
	source  activity.ActivitySource
	outcome activity.Outcome
}

type recEvents struct {
	// onDone, when set BEFORE the scan starts, is invoked inside
	// PublishScanDone — an event barrier for asserting what is observable
	// at the exact instant the terminal event publishes.
	onDone func(actID string)
	mu     sync.Mutex
	starts []scanEvt
	dones  []scanEvt
}

func (r *recEvents) PublishCoverageUpdate(api.MediaType, string) {}

func (r *recEvents) PublishScanStart(action, detail string, source activity.ActivitySource, actID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, scanEvt{action: action, detail: detail, source: source, actID: actID})
}

func (r *recEvents) PublishScanDone(action, detail string, source activity.ActivitySource, actID string, outcome activity.Outcome) {
	if r.onDone != nil {
		r.onDone(actID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dones = append(r.dones, scanEvt{action: action, detail: detail, source: source, actID: actID, outcome: outcome})
}

func (r *recEvents) doneEvents() []scanEvt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scanEvt, len(r.dones))
	copy(out, r.dones)
	return out
}

type nopAlerts struct{}

func (nopAlerts) Record(string, string) {}
func (nopAlerts) RecordInfo(string)     {}

// fakeSonarr satisfies ScanHandlerSonarr.
type fakeSonarr struct {
	seriesErr   error
	episodesErr error
	series      arrapi.Series
	episodes    []arrapi.Episode
}

func (f *fakeSonarr) GetSeriesByID(_ context.Context, _ int) (arrapi.Series, error) {
	return f.series, f.seriesErr
}

func (f *fakeSonarr) GetEpisodes(_ context.Context, _ int) ([]arrapi.Episode, error) {
	return f.episodes, f.episodesErr
}

// fakeRadarr satisfies ScanHandlerRadarr.
type fakeRadarr struct {
	movieErr error
	movie    arrapi.Movie
}

func (f *fakeRadarr) GetMovieByID(_ context.Context, _ int) (arrapi.Movie, error) {
	return f.movie, f.movieErr
}

// epsWithFiles builds n sequential season-1 episodes carrying files.
func epsWithFiles(n int) []arrapi.Episode {
	eps := make([]arrapi.Episode, 0, n)
	for i := range n {
		eps = append(eps, arrapi.Episode{
			ID: 100 + i, SeasonNumber: 1, EpisodeNumber: i + 1,
			HasFile:     true,
			EpisodeFile: &arrapi.EpisodeFile{Path: "/media/e.mkv"},
		})
	}
	return eps
}

// scanRig assembles a Handler over controllable fakes.
type scanRig struct {
	ctx    context.Context
	cancel context.CancelFunc
	engine *fakeEngine
	ev     *recEvents
	log    *activity.Log
	stops  *activity.StopRegistry
	guard  *ScanGuard
	bg     *sync.WaitGroup
	sonarr *fakeSonarr
	radarr *fakeRadarr
	h      *Handler
}

func newScanRig(t *testing.T) *scanRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rig := &scanRig{
		ctx: ctx, cancel: cancel,
		engine: &fakeEngine{},
		ev:     &recEvents{},
		log:    activity.New(50),
		stops:  &activity.StopRegistry{},
		guard:  &ScanGuard{},
		bg:     &sync.WaitGroup{},
		sonarr: &fakeSonarr{series: arrapi.Series{ID: 42, Title: "Test Show"}, episodes: epsWithFiles(1)},
		radarr: &fakeRadarr{movie: arrapi.Movie{ID: 7, Title: "Test Movie", Year: 2020, MovieFile: &arrapi.MovieFile{Path: "/media/m.mkv"}}},
	}
	cfg := &testsupport.NopConfig{}
	rig.h = NewHandler(HandlerDeps{
		StateFunc: func() (*HandlerState, *LiveState) {
			st := &HandlerState{Cfg: cfg, Engine: rig.engine}
			if rig.sonarr != nil {
				st.Sonarr = rig.sonarr
			}
			if rig.radarr != nil {
				st.Radarr = rig.radarr
			}
			return st, &LiveState{Cfg: cfg, Engine: rig.engine}
		},
		CtxFunc:         func() context.Context { return rig.ctx },
		ScanDeps:        func() *Deps { return &Deps{Events: rig.ev, Activity: rig.log, Alerts: nopAlerts{}} },
		Activity:        rig.log,
		Stops:           rig.stops,
		ScanGuard:       rig.guard,
		Alerts:          nopAlerts{},
		Events:          rig.ev,
		InvalidateStats: func() {},
		BGTracker:       rig.bg,
	})
	return rig
}

// post drives one handler invocation and decodes the ScanAccepted body.
func (rig *scanRig) post(t *testing.T, handler http.HandlerFunc, target, body string) (int, ScanAccepted) {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target, rd)
	rec := httptest.NewRecorder()
	handler(rec, req)
	var accepted ScanAccepted
	if rec.Code == http.StatusAccepted {
		if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
			t.Fatalf("decode ScanAccepted: %v", err)
		}
	}
	return rec.Code, accepted
}

// waitFor polls cond until true or the timeout elapses.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- 202 + activity_id at accept, per endpoint ---

func TestScanEndpoints_return_202_with_activity_id(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		invoke    func(rig *scanRig) (int, ScanAccepted)
		wantKind  activity.ScanKind
		wantMedia api.MediaType
	}{
		{
			name: "series",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
			},
			wantKind: activity.ScanKindSeries,
		},
		{
			name: "season",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanSeason, "/api/scan/season/42/1", "")
			},
			wantKind: activity.ScanKindSeason,
		},
		{
			name: "movie",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
			},
			wantKind: activity.ScanKindMovie,
		},
		{
			name: "item episode",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanItem, "/api/scan/item",
					`{"media_type":"episode","media_id":42,"season":1,"episode":1}`)
			},
			wantKind:  activity.ScanKindItem,
			wantMedia: api.MediaTypeEpisode,
		},
		{
			name: "item movie",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanItem, "/api/scan/item",
					`{"media_type":"movie","media_id":7}`)
			},
			wantKind:  activity.ScanKindItem,
			wantMedia: api.MediaTypeMovie,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newScanRig(t)
			code, accepted := tc.invoke(rig)

			if code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", code)
			}
			if accepted.ActivityID == "" {
				t.Fatal("202 body carries no activity_id at accept time")
			}
			entry, ok := rig.log.Get(accepted.ActivityID)
			if !ok {
				t.Fatal("activity entry missing at accept time")
			}
			if entry.Kind != tc.wantKind {
				t.Errorf("entry.Kind = %q, want %q", entry.Kind, tc.wantKind)
			}
			if entry.MediaType != tc.wantMedia {
				t.Errorf("entry.MediaType = %q, want %q", entry.MediaType, tc.wantMedia)
			}
			if entry.RequiredRole != auth.RoleUser {
				t.Errorf("entry.RequiredRole = %q, want user (per-item scans are user-cancellable)", entry.RequiredRole)
			}

			rig.bg.Wait()
			entry, _ = rig.log.Get(accepted.ActivityID)
			if !entry.Done || entry.Failed || entry.Cancelled {
				t.Errorf("terminal entry = done=%v failed=%v cancelled=%v, want clean completion",
					entry.Done, entry.Failed, entry.Cancelled)
			}
			dones := rig.ev.doneEvents()
			if len(dones) != 1 || dones[0].outcome != activity.OutcomeCompleted {
				t.Errorf("scan:done events = %+v, want one completed event", dones)
			}
			if dones[0].actID != accepted.ActivityID {
				t.Errorf("scan:done activity_id = %q, want %q", dones[0].actID, accepted.ActivityID)
			}
		})
	}
}

// --- preflight: arr existence lookup answers BEFORE the 202 ---

func TestHandleScanSeries_preflight_not_found_404(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.sonarr.seriesErr = &arrapi.StatusError{Path: "/series/42", Code: http.StatusNotFound}

	code, _ := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (synchronous preflight)", code)
	}
	if n := len(rig.log.Entries()); n != 0 {
		t.Errorf("activity entries = %d after preflight failure, want 0", n)
	}
}

func TestHandleScanMovie_preflight_arr_error_502(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.radarr.movieErr = &arrapi.StatusError{Path: "/movie/7", Code: http.StatusBadGateway}

	code, _ := rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for a non-404 lookup failure", code)
	}
}

// --- idempotent same-scope start ---

func TestHandleScanSeries_idempotent_same_scope(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.engine.started = make(chan int, 4)
	rig.engine.release = make(chan struct{})

	code1, first := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code1 != http.StatusAccepted {
		t.Fatalf("first start status = %d, want 202", code1)
	}
	<-rig.engine.started // scan is provably running its first item

	code2, second := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code2 != http.StatusAccepted {
		t.Fatalf("second start status = %d, want 202 (idempotent)", code2)
	}
	if second.ActivityID != first.ActivityID {
		t.Errorf("second start activity_id = %q, want the running scan's id %q",
			second.ActivityID, first.ActivityID)
	}
	if n := len(rig.log.Entries()); n != 1 {
		t.Errorf("activity entries = %d, want 1 (no duplicate work queued)", n)
	}

	// A DIFFERENT scope must still start its own scan.
	rig.sonarr.series.ID = 43
	code3, third := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/43", "")
	if code3 != http.StatusAccepted || third.ActivityID == first.ActivityID {
		t.Errorf("different-scope start = %d/%q, want 202 with a fresh id", code3, third.ActivityID)
	}

	close(rig.engine.release) // let both scans drain
	rig.bg.Wait()
}

// --- graceful stop between items ---

func TestHandleScanSeries_stop_between_items(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.sonarr.episodes = epsWithFiles(3)
	rig.engine.started = make(chan int)
	rig.engine.release = make(chan struct{})

	code, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}

	// Item 1 runs to completion.
	if n := <-rig.engine.started; n != 1 {
		t.Fatalf("first started call = %d, want 1", n)
	}
	rig.engine.release <- struct{}{}

	// Stop lands while item 2 is IN FLIGHT: the item must complete, item 3
	// must never start.
	if n := <-rig.engine.started; n != 2 {
		t.Fatalf("second started call = %d, want 2", n)
	}
	if got := rig.stops.RequestStop(accepted.ActivityID); got != activity.StopRequested {
		t.Fatalf("RequestStop = %v, want StopRequested", got)
	}
	rig.engine.release <- struct{}{}

	rig.bg.Wait()

	if got := rig.engine.callCount(); got != 2 {
		t.Errorf("engine calls = %d, want 2 (item 2 completes, item 3 never runs)", got)
	}
	entry, _ := rig.log.Get(accepted.ActivityID)
	if !entry.Done || !entry.Cancelled || entry.EndedAt == nil {
		t.Errorf("entry = done=%v cancelled=%v endedAt=%v, want terminal cancelled state",
			entry.Done, entry.Cancelled, entry.EndedAt)
	}
	if entry.Failed {
		t.Error("entry.Failed = true; a stopped scan is cancelled, not failed")
	}
	dones := rig.ev.doneEvents()
	if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
		t.Errorf("scan:done = %+v, want one cancelled event", dones)
	}
	if rig.stops.Cancellable(accepted.ActivityID) {
		t.Error("stop registration leaked after the cancelled terminal transition")
	}
}

// --- queued-cancel reaches the cancelled terminal state (regression: the
// pre-existing defect ended a queued-then-cancelled scan as SUCCESS) ---

func TestScan_queued_cancel_ends_cancelled_not_success(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)

	// Simulate another manual scan holding the slot so ours queues.
	if !rig.guard.Acquire(context.Background(), nil) {
		t.Fatal("test setup: could not take the scan slot")
	}

	code, accepted := rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	waitFor(t, "entry to queue", func() bool {
		e, ok := rig.log.Get(accepted.ActivityID)
		return ok && e.Queued
	})

	// Dismiss-cancel the QUEUED entry (DELETE /api/activity path semantics).
	// Dismiss fires no stop signal, so the queued runner only notices once
	// the slot frees.
	if !rig.log.Cancel(accepted.ActivityID) {
		t.Fatal("queued Cancel returned false")
	}
	rig.guard.Release()
	rig.bg.Wait()

	entry, _ := rig.log.Get(accepted.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("entry = done=%v cancelled=%v, want terminal cancelled", entry.Done, entry.Cancelled)
	}
	if entry.Failed {
		t.Error("queued-cancel must not end as failed")
	}
	dones := rig.ev.doneEvents()
	if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
		t.Errorf("scan:done = %+v, want cancelled (NOT the old ok=true success)", dones)
	}
	if got := rig.engine.callCount(); got != 0 {
		t.Errorf("engine calls = %d, want 0 (cancelled before running)", got)
	}
	if rig.stops.Cancellable(accepted.ActivityID) {
		t.Error("stop registration leaked after queued-cancel")
	}
}

// --- new-wiring detachment: request ctx never reaches the scan; the server
// ctx is the only hard kill; the stop signal is independent of both ---

func TestScan_request_context_cancellation_never_reaches_scan(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.engine.started = make(chan int)
	rig.engine.release = make(chan struct{})

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(reqCtx, http.MethodPost, "/api/scan/series/42", http.NoBody)
	rec := httptest.NewRecorder()
	rig.h.HandleScanSeries(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var accepted ScanAccepted
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	<-rig.engine.started
	// Simulate the client vanishing right after the 202.
	cancelReq()
	rig.engine.release <- struct{}{}
	rig.bg.Wait()

	entry, _ := rig.log.Get(accepted.ActivityID)
	if !entry.Done || entry.Failed || entry.Cancelled {
		t.Errorf("entry after request-ctx cancel = done=%v failed=%v cancelled=%v, want clean completion (background job survives disconnect)",
			entry.Done, entry.Failed, entry.Cancelled)
	}
}

func TestScan_shutdown_vs_stop_outcomes_are_distinct(t *testing.T) {
	t.Parallel()

	// Shutdown: server ctx cancellation ends the scan with NO user-facing
	// terminal marking and NO scan:done event (the process is exiting).
	t.Run("shutdown", func(t *testing.T) {
		t.Parallel()
		rig := newScanRig(t)
		rig.sonarr.episodes = epsWithFiles(2)
		rig.engine.started = make(chan int)
		rig.engine.release = make(chan struct{})

		_, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
		<-rig.engine.started
		rig.cancel() // server shutdown mid-item-1
		rig.engine.release <- struct{}{}
		rig.bg.Wait()

		if got := rig.engine.callCount(); got != 1 {
			t.Errorf("engine calls = %d, want 1 (shutdown stops before item 2)", got)
		}
		entry, _ := rig.log.Get(accepted.ActivityID)
		if entry.Done || entry.Cancelled || entry.Failed {
			t.Errorf("entry after shutdown = done=%v cancelled=%v failed=%v, want NO user-facing terminal marking",
				entry.Done, entry.Cancelled, entry.Failed)
		}
		if dones := rig.ev.doneEvents(); len(dones) != 0 {
			t.Errorf("scan:done events after shutdown = %+v, want none", dones)
		}
		if rig.stops.Cancellable(accepted.ActivityID) {
			t.Error("stop registration leaked after shutdown")
		}
	})

	// Stop: the graceful signal ends the scan in the terminal cancelled
	// state (covered in depth by the stop-between-items test); the key
	// distinction is cancelled-vs-unmarked.
	t.Run("stop", func(t *testing.T) {
		t.Parallel()
		rig := newScanRig(t)
		rig.sonarr.episodes = epsWithFiles(2)
		rig.engine.started = make(chan int)
		rig.engine.release = make(chan struct{})

		_, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
		<-rig.engine.started
		rig.stops.RequestStop(accepted.ActivityID)
		rig.engine.release <- struct{}{}
		rig.bg.Wait()

		entry, _ := rig.log.Get(accepted.ActivityID)
		if !entry.Done || !entry.Cancelled {
			t.Errorf("entry after stop = done=%v cancelled=%v, want terminal cancelled", entry.Done, entry.Cancelled)
		}
		dones := rig.ev.doneEvents()
		if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
			t.Errorf("scan:done = %+v, want cancelled", dones)
		}
	})
}

// --- registry lifecycle: released on EVERY terminal transition ---

func TestScan_registration_released_on_every_terminal_path(t *testing.T) {
	t.Parallel()

	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		rig := newScanRig(t)
		_, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
		rig.bg.Wait()
		if rig.stops.Cancellable(accepted.ActivityID) {
			t.Error("registration leaked after End")
		}
	})

	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		rig := newScanRig(t)
		rig.sonarr.episodesErr = &arrapi.StatusError{Path: "/episode", Code: http.StatusInternalServerError}
		_, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
		rig.bg.Wait()

		entry, _ := rig.log.Get(accepted.ActivityID)
		if !entry.Done || !entry.Failed {
			t.Errorf("entry = done=%v failed=%v, want terminal failed", entry.Done, entry.Failed)
		}
		dones := rig.ev.doneEvents()
		if len(dones) != 1 || dones[0].outcome != activity.OutcomeFailed {
			t.Errorf("scan:done = %+v, want failed", dones)
		}
		if rig.stops.Cancellable(accepted.ActivityID) {
			t.Error("registration leaked after Fail — the entry would stay cancellable forever")
		}
	})

	// The cancelled path is asserted in TestHandleScanSeries_stop_between_items.
}

// --- full-scan loop: stop between items ---

func TestProcessItems_stop_between_items(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{started: make(chan int), release: make(chan struct{})}
	ev := &recEvents{}
	log := activity.New(10)
	cfg := &testsupport.NopConfig{}
	deps := &Deps{Events: ev, Activity: log, Alerts: nopAlerts{}}
	ls := &LiveState{Cfg: cfg, Engine: engine}
	movie := func(id int, title string) ScanItem {
		return ScanItem{Movie: &arrapi.Movie{ID: id, Title: title, MovieFile: &arrapi.MovieFile{Path: "/m.mkv"}}}
	}
	queue := []ScanItem{movie(1, "A"), movie(2, "B"), movie(3, "C")}
	stop := make(chan struct{})
	var stats api.ScanStats

	type result struct {
		resumed int
		outcome activity.Outcome
	}
	res := make(chan result, 1)
	go func() {
		resumed, outcome := processItems(context.Background(), stop, deps, ls,
			queue, nil, &stats, "act1", 0)
		res <- result{resumed, outcome}
	}()

	<-engine.started // item 1
	engine.release <- struct{}{}
	<-engine.started // item 2 in flight
	close(stop)
	engine.release <- struct{}{} // item 2 completes

	got := <-res
	if got.outcome != activity.OutcomeCancelled {
		t.Errorf("processItems outcome = %q, want cancelled", got.outcome)
	}
	if calls := engine.callCount(); calls != 2 {
		t.Errorf("engine calls = %d, want 2 (item 3 never runs)", calls)
	}
}

// --- RunFullScan outcome mapping ---

func TestRunFullScan_outcomes(t *testing.T) {
	t.Parallel()

	newDeps := func(db *fakeScanStore, ev *recEvents, log *activity.Log) *Deps {
		return &Deps{
			DB: db, Metrics: nopScanMetrics{}, Events: ev, Activity: log,
			Alerts: nopAlerts{}, ClearCaches: func([]api.Provider) {},
		}
	}

	t.Run("completed clears the cycle mark", func(t *testing.T) {
		t.Parallel()
		db := &fakeScanStore{}
		log := activity.New(10)
		deps := newDeps(db, &recEvents{}, log)
		ls := &LiveState{Cfg: &testsupport.NopConfig{}, Engine: &fakeEngine{}}
		actID := log.Start("Full Scan", "d", activity.SourceManual)

		outcome := RunFullScan(context.Background(), make(chan struct{}), deps, ls, actID)
		if outcome != activity.OutcomeCompleted {
			t.Fatalf("outcome = %q, want completed", outcome)
		}
		if db.cleared != 1 {
			t.Errorf("cycle mark cleared %d times, want 1", db.cleared)
		}
	})

	t.Run("server ctx cancellation maps to shutdown and keeps the mark", func(t *testing.T) {
		t.Parallel()
		db := &fakeScanStore{}
		log := activity.New(10)
		deps := newDeps(db, &recEvents{}, log)
		ls := &LiveState{Cfg: &testsupport.NopConfig{}, Engine: &fakeEngine{}}
		actID := log.Start("Full Scan", "d", activity.SourceManual)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		outcome := RunFullScan(ctx, make(chan struct{}), deps, ls, actID)
		if outcome != activity.OutcomeShutdown {
			t.Fatalf("outcome = %q, want shutdown", outcome)
		}
		if db.cleared != 0 {
			t.Errorf("cycle mark cleared %d times on shutdown, want 0 (resume signal stays)", db.cleared)
		}
	})
}

// nopScanMetrics satisfies ScanMetrics.
type nopScanMetrics struct{}

func (nopScanMetrics) RecordScan(int, int, time.Duration) {}
func (nopScanMetrics) AdaptiveSkip()                      {}

// --- graceful stop during the FINAL in-flight item (regression: stop was
// checked before items and during delays but never after the last item
// returned, so a stop during the final item published completed) ---

func TestHandleScanSeries_stop_during_final_item(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.sonarr.episodes = epsWithFiles(3)
	rig.engine.started = make(chan int)
	rig.engine.release = make(chan struct{})

	code, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}

	// Items 1 and 2 run to completion.
	<-rig.engine.started
	rig.engine.release <- struct{}{}
	<-rig.engine.started
	rig.engine.release <- struct{}{}

	// Stop lands while the FINAL item is in flight: it completes its work
	// (graceful stop), but the terminal outcome must be cancelled.
	if n := <-rig.engine.started; n != 3 {
		t.Fatalf("third started call = %d, want 3", n)
	}
	if got := rig.stops.RequestStop(accepted.ActivityID); got != activity.StopRequested {
		t.Fatalf("RequestStop = %v, want StopRequested", got)
	}
	rig.engine.release <- struct{}{}
	rig.bg.Wait()

	if got := rig.engine.callCount(); got != 3 {
		t.Errorf("engine calls = %d, want 3 (the in-flight final item completes)", got)
	}
	entry, _ := rig.log.Get(accepted.ActivityID)
	if !entry.Done || !entry.Cancelled || entry.Failed {
		t.Errorf("entry = done=%v cancelled=%v failed=%v, want terminal cancelled",
			entry.Done, entry.Cancelled, entry.Failed)
	}
	dones := rig.ev.doneEvents()
	if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
		t.Errorf("scan:done = %+v, want cancelled (NOT completed)", dones)
	}
}

func TestScan_single_item_stop_during_item_is_cancelled(t *testing.T) {
	t.Parallel()
	// For single-item scopes the in-flight item IS the whole scan: a stop
	// during it must still terminate as cancelled, never as completed.
	cases := []struct {
		invoke func(rig *scanRig) (int, ScanAccepted)
		name   string
	}{
		{
			name: "single episode",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanItem, "/api/scan/item",
					`{"media_type":"episode","media_id":42,"season":1,"episode":1}`)
			},
		},
		{
			name: "movie",
			invoke: func(rig *scanRig) (int, ScanAccepted) {
				return rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newScanRig(t)
			rig.engine.started = make(chan int)
			rig.engine.release = make(chan struct{})

			code, accepted := tc.invoke(rig)
			if code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", code)
			}

			<-rig.engine.started // the single item is in flight
			if got := rig.stops.RequestStop(accepted.ActivityID); got != activity.StopRequested {
				t.Fatalf("RequestStop = %v, want StopRequested", got)
			}
			rig.engine.release <- struct{}{}
			rig.bg.Wait()

			if got := rig.engine.callCount(); got != 1 {
				t.Errorf("engine calls = %d, want 1 (the item completes its work)", got)
			}
			entry, _ := rig.log.Get(accepted.ActivityID)
			if !entry.Done || !entry.Cancelled || entry.Failed {
				t.Errorf("entry = done=%v cancelled=%v failed=%v, want terminal cancelled",
					entry.Done, entry.Cancelled, entry.Failed)
			}
			dones := rig.ev.doneEvents()
			if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
				t.Errorf("scan:done = %+v, want cancelled", dones)
			}
		})
	}
}

func TestProcessItems_stop_during_final_item(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{started: make(chan int), release: make(chan struct{})}
	ev := &recEvents{}
	log := activity.New(10)
	cfg := &testsupport.NopConfig{}
	deps := &Deps{Events: ev, Activity: log, Alerts: nopAlerts{}}
	ls := &LiveState{Cfg: cfg, Engine: engine}
	movie := func(id int, title string) ScanItem {
		return ScanItem{Movie: &arrapi.Movie{ID: id, Title: title, MovieFile: &arrapi.MovieFile{Path: "/m.mkv"}}}
	}
	queue := []ScanItem{movie(1, "A"), movie(2, "B"), movie(3, "C")}
	stop := make(chan struct{})
	var stats api.ScanStats

	res := make(chan activity.Outcome, 1)
	go func() {
		_, outcome := processItems(context.Background(), stop, deps, ls,
			queue, nil, &stats, "act1", 0)
		res <- outcome
	}()

	<-engine.started // item 1
	engine.release <- struct{}{}
	<-engine.started // item 2
	engine.release <- struct{}{}
	<-engine.started // FINAL item in flight
	close(stop)
	engine.release <- struct{}{} // it completes its work

	if got := <-res; got != activity.OutcomeCancelled {
		t.Errorf("processItems outcome = %q, want cancelled (stop during the final item)", got)
	}
	if calls := engine.callCount(); calls != 3 {
		t.Errorf("engine calls = %d, want 3 (the in-flight final item completes)", calls)
	}
}

// --- interruptible slot acquisition: a queued scan's stop (or shutdown)
// must not wait for the running scan to release the slot ---

func TestScan_queued_stop_cancels_without_slot_release(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)

	// Scan A holds the slot for the WHOLE test — B must reach its terminal
	// state without A ever releasing.
	if !rig.guard.Acquire(context.Background(), nil) {
		t.Fatal("test setup: could not take the scan slot")
	}
	defer rig.guard.Release()

	code, accepted := rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	waitFor(t, "entry to queue", func() bool {
		e, ok := rig.log.Get(accepted.ActivityID)
		return ok && e.Queued
	})

	if got := rig.stops.RequestStop(accepted.ActivityID); got != activity.StopRequested {
		t.Fatalf("RequestStop = %v, want StopRequested", got)
	}
	rig.bg.Wait() // returns while A still holds the slot

	entry, _ := rig.log.Get(accepted.ActivityID)
	if !entry.Done || !entry.Cancelled {
		t.Errorf("entry = done=%v cancelled=%v, want terminal cancelled without the slot ever releasing",
			entry.Done, entry.Cancelled)
	}
	if got := rig.engine.callCount(); got != 0 {
		t.Errorf("engine calls = %d, want 0 (cancelled while queued)", got)
	}
	dones := rig.ev.doneEvents()
	if len(dones) != 1 || dones[0].outcome != activity.OutcomeCancelled {
		t.Errorf("scan:done = %+v, want cancelled", dones)
	}
	if rig.stops.Cancellable(accepted.ActivityID) {
		t.Error("stop registration leaked after queued stop")
	}
}

func TestScan_queued_shutdown_unblocks_without_slot_release(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)

	if !rig.guard.Acquire(context.Background(), nil) {
		t.Fatal("test setup: could not take the scan slot")
	}
	defer rig.guard.Release()

	_, accepted := rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
	waitFor(t, "entry to queue", func() bool {
		e, ok := rig.log.Get(accepted.ActivityID)
		return ok && e.Queued
	})

	rig.cancel()  // server shutdown while queued
	rig.bg.Wait() // returns while the slot is still held

	entry, _ := rig.log.Get(accepted.ActivityID)
	if entry.Done || entry.Cancelled || entry.Failed {
		t.Errorf("entry after queued shutdown = done=%v cancelled=%v failed=%v, want no user-facing marking",
			entry.Done, entry.Cancelled, entry.Failed)
	}
	if dones := rig.ev.doneEvents(); len(dones) != 0 {
		t.Errorf("scan:done events after shutdown = %+v, want none", dones)
	}
	if rig.stops.Cancellable(accepted.ActivityID) {
		t.Error("stop registration leaked after queued shutdown")
	}
}

// --- one immutable state snapshot per operation: a hot reload between
// accept and execution must not mix generations, and no state callback is
// consulted again after admission ---

func TestScan_operation_uses_one_state_snapshot(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg := &testsupport.NopConfig{}
	engineA := &fakeEngine{}
	engineB := &fakeEngine{}
	sonarrA := &fakeSonarr{series: arrapi.Series{ID: 42, Title: "Gen A Show"}, episodes: epsWithFiles(2)}
	// Generation B is poisoned: consulting it fails the scan (episodes
	// error) or records engine calls — a mixed-generation operation cannot
	// complete cleanly.
	sonarrB := &fakeSonarr{episodesErr: &arrapi.StatusError{Path: "/episodes", Code: http.StatusInternalServerError}}

	ev := &recEvents{}
	log := activity.New(50)
	stops := &activity.StopRegistry{}
	guard := &ScanGuard{}
	bg := &sync.WaitGroup{}

	var mu sync.Mutex
	curState := &HandlerState{Cfg: cfg, Engine: engineA, Sonarr: sonarrA}
	curLS := &LiveState{Cfg: cfg, Engine: engineA}
	var stateCalls, depsCalls int
	callCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return stateCalls + depsCalls
	}

	h := NewHandler(HandlerDeps{
		StateFunc: func() (*HandlerState, *LiveState) {
			mu.Lock()
			defer mu.Unlock()
			stateCalls++
			return curState, curLS
		},
		CtxFunc: func() context.Context { return ctx },
		ScanDeps: func() *Deps {
			mu.Lock()
			defer mu.Unlock()
			depsCalls++
			return &Deps{Events: ev, Activity: log, Alerts: nopAlerts{}}
		},
		Activity:        log,
		Stops:           stops,
		ScanGuard:       guard,
		Alerts:          nopAlerts{},
		Events:          ev,
		InvalidateStats: func() {},
		BGTracker:       bg,
	})

	// Hold the slot so the accepted scan QUEUES: the reload lands between
	// queue admission and execution.
	if !guard.Acquire(context.Background(), nil) {
		t.Fatal("test setup: could not take the scan slot")
	}

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/42", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var accepted ScanAccepted
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}

	callsAtAccept := callCount()
	if callsAtAccept != 2 {
		t.Fatalf("state callbacks consulted %d times at accept, want exactly 2 (one combined state read + one deps read)", callsAtAccept)
	}

	// Hot reload while the operation is queued: swap every generation callback.
	mu.Lock()
	curState = &HandlerState{Cfg: cfg, Engine: engineB, Sonarr: sonarrB}
	curLS = &LiveState{Cfg: cfg, Engine: engineB}
	mu.Unlock()

	guard.Release()
	bg.Wait()

	if got := callCount(); got != callsAtAccept {
		t.Errorf("state callbacks consulted %d times total, want %d — an operation must never re-read live state after admission",
			got, callsAtAccept)
	}
	entry, _ := log.Get(accepted.ActivityID)
	if !entry.Done || entry.Failed || entry.Cancelled {
		t.Errorf("entry = done=%v failed=%v cancelled=%v, want clean completion on generation A (a generation-B read would have failed the scan)",
			entry.Done, entry.Failed, entry.Cancelled)
	}
	if got := engineA.callCount(); got != 2 {
		t.Errorf("generation A engine calls = %d, want 2", got)
	}
	if got := engineB.callCount(); got != 0 {
		t.Errorf("generation B engine calls = %d, want 0 (mixed generation)", got)
	}
}

// --- terminal-visibility barrier: the stop registration is released BEFORE
// the terminal transition publishes, so the instant a done entry is
// observable a cancel answers 409 (not a 204 for completed work) ---

func TestFinishScanActivity_releases_registration_before_terminal_transition(t *testing.T) {
	t.Parallel()
	outcomes := []activity.Outcome{
		activity.OutcomeCompleted,
		activity.OutcomeFailed,
		activity.OutcomeCancelled,
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			t.Parallel()
			log := activity.New(10)
			stops := &activity.StopRegistry{}
			id, _ := log.StartScan("Scan", "d", activity.SourceManual,
				activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 1}, auth.RoleUser)
			unregister := stops.RegisterStop(id, func() {})

			type probe struct{ cancellable, done bool }
			probeCh := make(chan probe, 1)
			ev := &recEvents{onDone: func(actID string) {
				e, _ := log.Get(actID)
				probeCh <- probe{cancellable: stops.Cancellable(actID), done: e.Done}
			}}

			FinishScanActivity(unregister, log, ev, id, "Scan", "d",
				activity.SourceManual, outcome)

			p := <-probeCh
			if p.cancellable {
				t.Error("registration still live at scan:done publication — a done entry would answer cancel 204")
			}
			if !p.done {
				t.Error("entry not terminal at scan:done publication")
			}
		})
	}

	t.Run("shutdown", func(t *testing.T) {
		t.Parallel()
		log := activity.New(10)
		stops := &activity.StopRegistry{}
		id, _ := log.StartScan("Scan", "d", activity.SourceManual,
			activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 1}, auth.RoleUser)
		unregister := stops.RegisterStop(id, func() {})
		ev := &recEvents{}

		FinishScanActivity(unregister, log, ev, id, "Scan", "d",
			activity.SourceManual, activity.OutcomeShutdown)

		if stops.Cancellable(id) {
			t.Error("registration leaked on shutdown outcome")
		}
		if dones := ev.doneEvents(); len(dones) != 0 {
			t.Errorf("scan:done published on shutdown = %+v, want none", dones)
		}
	})
}

func TestScan_registration_released_before_scan_done_event(t *testing.T) {
	t.Parallel()
	// End-to-end through startBackgroundScan: at the instant scan:done
	// publishes, the registration must already be gone and the entry
	// terminal.
	rig := newScanRig(t)
	type probe struct{ cancellable, done bool }
	probeCh := make(chan probe, 1)
	rig.ev.onDone = func(actID string) {
		e, _ := rig.log.Get(actID)
		probeCh <- probe{cancellable: rig.stops.Cancellable(actID), done: e.Done}
	}

	code, _ := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", code)
	}
	rig.bg.Wait()

	p := <-probeCh
	if p.cancellable {
		t.Error("registration still live at scan:done publication")
	}
	if !p.done {
		t.Error("entry not terminal at scan:done publication")
	}
}
