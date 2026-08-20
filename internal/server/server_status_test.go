package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/scanning"
	"pgregory.net/rapid"
)

func TestHandleGetAlerts_returns_recent_alerts(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	// Add a recent alert and an already-expired one (a negative TTL override
	// is elapsed the moment it is recorded, so no backdating is needed).
	s.alerts.Record("sonarr", "recent error")
	s.alerts.AddAlert("old", "old error",
		activity.AlertTransient, activity.LevelError, -time.Second)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var alerts []activity.Alert
	if err := json.NewDecoder(rec.Body).Decode(&alerts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Only the recent alert (within 1h default TTL) should be returned.
	if len(alerts) != 1 {
		t.Errorf("handleGetAlerts() returned %d alerts, want 1 (only recent)", len(alerts))
	}
	if len(alerts) > 0 && alerts[0].Source != "sonarr" {
		t.Errorf("alerts[0].Source = %q, want %q", alerts[0].Source, "sonarr")
	}
}

func TestHandleGetAlerts_empty_when_no_alerts(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should return an empty JSON array, not null.
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleGetAlerts() body = %q, want %q", body, "[]")
	}
}

func TestHandleGetActivity_returns_last_20(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	// Add 25 COMPLETED entries (the page cap applies to completed rows;
	// running rows always survive it). The log's capacity is 50, so none
	// are ring-evicted.
	for range 25 {
		id := s.activity.Start("Scan", "scan", "scheduled")
		s.activity.End(id)
	}

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []activity.Entry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 20 {
		t.Errorf("handleGetActivity() returned %d entries, want 20 (capped)", len(entries))
	}
}

func TestHandleGetActivity_running_entries_survive_page_cap(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	// One RUNNING entry older than the 20-entry page window, buried under
	// 25 completed rows: it must still be included (restoration and the
	// stop control depend on seeing it), with its cancellable flag merged
	// from the stop registry.
	runningID := s.activity.Start("Series Search", "old running scan", "manual")
	for range 25 {
		id := s.activity.Start("Scan", "scan", "scheduled")
		s.activity.End(id)
	}
	unregister := s.stops.RegisterStop(runningID, func() {})
	defer unregister()

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}
	var entries []activity.Entry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 21 {
		t.Errorf("handleGetActivity() returned %d entries, want 21 (20-page + surviving running row)", len(entries))
	}
	var running *activity.Entry
	for i := range entries {
		if entries[i].ID == runningID {
			running = &entries[i]
			break
		}
	}
	if running == nil {
		t.Fatal("running entry was dropped by the page cap")
	}
	if !running.Cancellable {
		t.Error("running entry should carry cancellable=true while its stop is registered")
	}
}

func TestHandleGetActivity_returns_all_when_under_20(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	s.activity.Start("Scan", "scan 1", "scheduled")
	s.activity.Start("Upgrade", "upgrade 1", "manual")

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []activity.Entry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("handleGetActivity() returned %d entries, want 2", len(entries))
	}
}

// newManualDownloadServer builds a Server whose manual handler resolves
// MediaRefs against cfg (containment validator) and radarr, with one stub
// provider "os" so the provider check passes and requests reach resolution.
func newManualDownloadServer(cfg *config.Config, radarr resolve.RadarrMovie) *Server {
	s := &Server{
		db:       &qhMockStore{},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(0),
	}
	s.live.Store(&liveState{cfg: cfg})
	resolver := &resolve.Resolver{
		Store: s.db,
		State: func() *resolve.State { return &resolve.State{Cfg: cfg, Radarr: radarr} },
	}
	s.manualH = manualops.NewHandler(manualops.HandlerDeps{
		DBFunc:   func() manualops.DownloadStore { return s.db.(manualops.DownloadStore) },
		Activity: s.activity,
		Alerts:   s.alerts,
		Events:   s.events,
		StateFunc: func() *manualops.LiveState {
			return &manualops.LiveState{Providers: []provider.Provider{&stubProvider{name: "os"}}}
		},
		BGTracker: &s.bgWg,
		// context.Background(): no *testing.T in scope, and ServerCtx is the server's long-lived context, not a request or test one.
		ServerCtx:  func() context.Context { return context.Background() },
		Resolve:    resolver,
		DecodeJSON: decodeJSONBodyAny,
	})
	return s
}

// statusFakeRadarr resolves movie 42 to a fixed file; anything else is
// unknown.
type statusFakeRadarr struct{ path string }

func (f statusFakeRadarr) GetMovieByID(_ context.Context, id int) (arrapi.Movie, error) {
	if id != 42 {
		return arrapi.Movie{}, errors.New("movie not found")
	}
	return arrapi.Movie{ID: 42, MovieFile: &arrapi.MovieFile{Path: f.path}}, nil
}

func TestHandleManualDownload_unknown_media_returns_404(t *testing.T) {
	t.Parallel()
	s := newManualDownloadServer(testConfig(t), statusFakeRadarr{path: "/media/movie.mkv"})

	body := `{"provider":"os","subtitle_id":"1","media_id":9999,"language":"en"}`
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.manualH.HandleManualDownload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("HandleManualDownload(unknown media) status = %d, want %d",
			rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "media_not_found") {
		t.Errorf("body = %q, want machine code media_not_found", rec.Body.String())
	}
}

func TestHandleManualDownload_containment_invariant_returns_500(t *testing.T) {
	t.Parallel()
	// The arr resolves the movie, but the resolved path fails the
	// containment check: a server-derived-path invariant breach -> 500,
	// never a 4xx (there is no client path to blame).
	s := newManualDownloadServer(testConfig(t), statusFakeRadarr{path: "/evil/path.mkv"})

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"language":"en"}`
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.manualH.HandleManualDownload(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleManualDownload(containment breach) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// The provider-not-found download test formerly here moved to
// internal/server/manualops/handler_http_test.go with the rest of the
// manual download HTTP surface.

// --- handleGetAlerts persistent alert filtering ---

func TestHandleDismissAlert_dismisses_by_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	s.alerts.Record("sonarr", "test error")

	id := s.alerts.VisibleAlerts()[0].ID

	// Method dispatch lives in routes.go ("DELETE /api/alerts" binds
	// handleDismissAlert directly); the handler owns only the dismissal.
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodDelete, "/api/alerts?id="+strconv.Itoa(id), http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleDismissAlert(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleDismissAlert() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the alert was dismissed: a dismissed alert leaves the visible set.
	if n := len(s.alerts.VisibleAlerts()); n != 0 {
		t.Errorf("visible alerts after DELETE = %d, want 0 (the alert was dismissed)", n)
	}
}

// --- handleGetActivity ---

func TestHandleGetActivity_empty_returns_empty_array(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})

	// No activities added — entries is nil.
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	activityH(s).HandleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should return an empty JSON array, not null.
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleGetActivity() body = %q, want %q (empty array, not null)", body, "[]")
	}
}

// --- scanning.SortByTitle ---

func TestSortByTitle_mixed(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &arrapi.Series{Title: "Zorro"}},
		{Series: &arrapi.Series{Title: "Archer"}},
	}
	movies := []scanning.ScanItem{
		{Movie: &arrapi.Movie{Title: "Batman"}},
		{Movie: &arrapi.Movie{Title: "Alien"}},
	}
	got := scanning.SortByTitle(episodes, movies)
	want := []string{"Alien", "Archer", "Batman", "Zorro"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if scanning.ScanItemTitle(got[i]) != w {
			t.Errorf("[%d] = %q, want %q",
				i, scanning.ScanItemTitle(got[i]), w)
		}
	}
}

func TestSortByTitle_case_insensitive(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &arrapi.Series{Title: "the Office"}},
	}
	movies := []scanning.ScanItem{
		{Movie: &arrapi.Movie{Title: "The Matrix"}},
	}
	got := scanning.SortByTitle(episodes, movies)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if scanning.ScanItemTitle(got[0]) != "The Matrix" {
		t.Errorf("[0] = %q, want %q",
			scanning.ScanItemTitle(got[0]), "The Matrix")
	}
}

func TestSortByTitle_both_empty(t *testing.T) {
	t.Parallel()
	got := scanning.SortByTitle(nil, nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestSortByTitle_one_empty(t *testing.T) {
	t.Parallel()
	movies := []scanning.ScanItem{
		{Movie: &arrapi.Movie{Title: "Zulu"}},
		{Movie: &arrapi.Movie{Title: "Alpha"}},
	}
	got := scanning.SortByTitle(nil, movies)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if scanning.ScanItemTitle(got[0]) != "Alpha" {
		t.Errorf("[0] = %q, want %q",
			scanning.ScanItemTitle(got[0]), "Alpha")
	}
}

func TestSortByTitle_secondary_sort_by_season_episode(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 2, EpisodeNumber: 1}},
		{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 3}},
		{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 1}},
	}
	got := scanning.SortByTitle(episodes, nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	s0, e0 := scanning.ScanItemSeasonEp(got[0])
	s1, e1 := scanning.ScanItemSeasonEp(got[1])
	s2, e2 := scanning.ScanItemSeasonEp(got[2])
	if s0 != 1 || e0 != 1 {
		t.Errorf("[0] = S%02dE%02d, want S01E01", s0, e0)
	}
	if s1 != 1 || e1 != 3 {
		t.Errorf("[1] = S%02dE%02d, want S01E03", s1, e1)
	}
	if s2 != 2 || e2 != 1 {
		t.Errorf("[2] = S%02dE%02d, want S02E01", s2, e2)
	}
}

// --- Property-based tests ---

func TestExtractAltTitles_property_no_primary_no_dupes(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		primary := rapid.String().Draw(t, "primary")
		n := rapid.IntRange(0, 10).Draw(t, "n")
		alts := make([]arrapi.AlternateTitle, n)
		for i := range n {
			alts[i] = arrapi.AlternateTitle{
				Title: rapid.String().Draw(t, fmt.Sprintf("alt_%d", i)),
			}
		}

		got := scanning.ExtractAltTitles(alts, primary)

		// Invariant 1: primary title never in output.
		for _, title := range got {
			if strings.EqualFold(title, primary) {
				t.Errorf("ExtractAltTitles output contains primary %q", primary)
			}
		}

		// Invariant 2: no case-insensitive duplicates.
		seen := make(map[string]bool)
		for _, title := range got {
			lower := strings.ToLower(title)
			if seen[lower] {
				t.Errorf("ExtractAltTitles output has duplicate %q", title)
			}
			seen[lower] = true
		}
	})
}

func TestSortByTitle_property_output_is_sorted(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nEp := rapid.IntRange(0, 8).Draw(t, "nEp")
		nMov := rapid.IntRange(0, 8).Draw(t, "nMov")

		episodes := make([]scanning.ScanItem, nEp)
		for i := range nEp {
			episodes[i] = scanning.ScanItem{
				Series: &arrapi.Series{
					Title: rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(t, fmt.Sprintf("ep_title_%d", i)),
				},
				Ep: &arrapi.Episode{
					SeasonNumber:  rapid.IntRange(0, 10).Draw(t, fmt.Sprintf("ep_s_%d", i)),
					EpisodeNumber: rapid.IntRange(1, 50).Draw(t, fmt.Sprintf("ep_e_%d", i)),
				},
			}
		}
		movies := make([]scanning.ScanItem, nMov)
		for i := range nMov {
			movies[i] = scanning.ScanItem{
				Movie: &arrapi.Movie{
					Title: rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(t, fmt.Sprintf("mov_title_%d", i)),
				},
			}
		}

		got := scanning.SortByTitle(episodes, movies)

		// Invariant: output length equals input length.
		if len(got) != nEp+nMov {
			t.Errorf("scanning.SortByTitle returned %d items, want %d", len(got), nEp+nMov)
		}

		// Invariant: output is sorted by (title, season, episode).
		for i := 1; i < len(got); i++ {
			prevTitle := strings.ToLower(scanning.ScanItemTitle(got[i-1]))
			currTitle := strings.ToLower(scanning.ScanItemTitle(got[i]))
			if prevTitle > currTitle {
				t.Errorf("scanning.SortByTitle not sorted at [%d]: %q > %q", i, prevTitle, currTitle)
			}
			if prevTitle == currTitle {
				ps, pe := scanning.ScanItemSeasonEp(got[i-1])
				cs, ce := scanning.ScanItemSeasonEp(got[i])
				if ps > cs || (ps == cs && pe > ce) {
					t.Errorf("scanning.SortByTitle not sorted at [%d]: S%02dE%02d > S%02dE%02d",
						i, ps, pe, cs, ce)
				}
			}
		}
	})
}
