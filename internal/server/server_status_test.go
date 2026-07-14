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

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
	"pgregory.net/rapid"
)

func TestHandleGetAlerts_returns_recent_alerts(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Add a recent alert and an old alert.
	s.alerts.Record("sonarr", "recent error")
	s.alerts.Lock()
	s.alerts.AppendAlert(activity.Alert{
		ID: -1, Level: "error", Source: "old", Message: "old error",
		Kind: activity.AlertTransient, Time: time.Now().Add(-48 * time.Hour),
	})
	s.alerts.Unlock()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

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
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

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
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Add 25 entries manually with distinct IDs.
	s.activity.Lock()
	for i := range 25 {
		s.activity.AppendEntry(activity.Entry{
			ID:     time.Now().Format("20060102150405.000") + string(rune('A'+i)),
			Action: "Scan",
			Detail: "scan",
		})
	}
	s.activity.Unlock()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

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

func TestHandleGetActivity_returns_all_when_under_20(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	s.activity.Start("Scan", "scan 1", "scheduled")
	s.activity.Start("Upgrade", "upgrade 1", "manual")

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

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

func TestHandleManualDownload_path_validation_failure(t *testing.T) {
	t.Parallel()

	cfg := &pathValidationErrorConfig{}
	s := &Server{
		db:       &qhMockStore{},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(0),
	}
	s.live.Store(&liveState{cfg: cfg})
	s.manualH = manualops.NewHandler(manualops.HandlerDeps{
		DBFunc:       func() manualops.DownloadStore { return s.db.(manualops.DownloadStore) },
		Activity:     &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:       &serveradapter.AlertAdapter{A: s.alerts},
		Events:       &serveradapter.ManualEventAdapter{E: s.events},
		StateFunc:    func() *manualops.LiveState { return &manualops.LiveState{} },
		BGTracker:    &s.bgWg,
		ServerCtx:    func() context.Context { return context.Background() },
		ValidatePath: s.validateFSPath,
		DecodeJSON:   decodeJSONBodyAny,
	})

	body := `{"provider":"os","subtitle_id":"1","file_path":"/evil/path","language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("handleManualDownload(invalid path) status = %d, want %d",
			rec.Code, http.StatusForbidden)
	}
}

// pathValidationErrorConfig returns an error from ValidatePath.
type pathValidationErrorConfig struct{ qhMockConfig }

func (m *pathValidationErrorConfig) ValidatePath(_ context.Context, _ string) error {
	return errors.New("path not under media roots")
}

func (m *pathValidationErrorConfig) RemoveUnderRoot(_ context.Context, _ string) error {
	return config.ErrPathNotAllowed
}

func TestHandleManualDownload_provider_not_found(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"provider":"nonexistent","subtitle_id":"1","file_path":"/media/movie.mkv","language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualDownload(unknown provider) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "provider not found") {
		t.Errorf("handleManualDownload() body = %q, want to contain %q",
			rec.Body.String(), "provider not found")
	}
}

func TestHandleState_filters_passed_through(t *testing.T) {
	t.Parallel()

	db := &filterTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{query: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state?type=episode&lang=fr&provider=os", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleState() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if db.mediaType != "episode" {
		t.Errorf("GetState mediaType = %q, want %q", db.mediaType, "episode")
	}
	if db.language != "fr" {
		t.Errorf("GetState language = %q, want %q", db.language, "fr")
	}
	if db.provider != "os" {
		t.Errorf("GetState provider = %q, want %q", db.provider, "os")
	}
}

// filterTrackingStore tracks the filter params passed to GetState.
type filterTrackingStore struct {
	mediaType api.MediaType
	language  string
	provider  string
	search    string
	qhMockStore
}

func (m *filterTrackingStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	m.mediaType = q.MediaType
	m.language = q.Language
	m.provider = string(q.Provider)
	m.search = q.Search
	m.stateLimit = q.Limit
	return nil, nil
}

// --- handleGetAlerts persistent alert filtering ---

func TestHandleGetAlerts_rejects_unsupported_method(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleGetAlerts(PUT) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetAlerts_delete_dispatches_to_dismiss(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	s.alerts.Record("sonarr", "test error")

	s.alerts.RLock()
	id := s.alerts.AlertsUnsafe()[0].ID
	s.alerts.RUnlock()

	// DELETE via handleGetAlerts should dispatch to handleDismissAlert.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/alerts?id="+strconv.Itoa(id), http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts(DELETE) status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the alert was dismissed.
	s.alerts.RLock()
	dismissed := s.alerts.AlertsUnsafe()[0].Dismissed
	s.alerts.RUnlock()

	if !dismissed {
		t.Error("alert should be dismissed after DELETE dispatch")
	}
}

// --- handleGetActivity method check ---

// --- asyncAction conflict path ---

func TestHandleGetActivity_empty_returns_empty_array(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// No activities added — entries is nil.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

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
