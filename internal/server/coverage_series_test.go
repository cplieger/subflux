package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- handleCoverageSeries ---

func TestHandleCoverageSeries_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleCoverageSeries(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCoverageSeries_no_sonarr_returns_empty(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleCoverageSeries(no sonarr) body = %q, want %q", body, "[]")
	}
}

// --- handleCoverageSeries full path tests ---

// coverageSeriesArrClient returns canned series data for coverage tests.
type coverageSeriesArrClient struct{ dummyArrClient }

func (c coverageSeriesArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return []api.Series{
		{
			ID:               1,
			Title:            "Test Show",
			Year:             2024,
			TvdbID:           81189,
			ImdbID:           "tt1234567",
			FirstAired:       "2024-01-01",
			OriginalLanguage: &api.LanguageInfo{Name: "English"},
			Statistics:       &api.SeriesStatistics{EpisodeFileCount: 3},
			Tags:             []int{1},
		},
		{
			ID:         2,
			Title:      "No Episodes",
			TvdbID:     99999,
			Statistics: &api.SeriesStatistics{EpisodeFileCount: 0},
		},
	}, nil
}

// coverageSeriesStore returns subtitle files for coverage series tests.
type coverageSeriesStore struct{ qhMockStore }

func (m *coverageSeriesStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return []api.SubtitleEntry{
		{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
	}, nil
}

func TestHandleCoverageSeries_returns_series_with_coverage(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
		providers: map[api.ProviderID]api.ProviderCfg{
			"embedded": {Enabled: true, Settings: map[string]any{"ignore_pgs": true}},
		},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Series with 0 episodes should be skipped.
	if len(result) != 1 {
		t.Fatalf("handleCoverageSeries() returned %d series, want 1", len(result))
	}

	s0 := result[0]
	if s0.Title != "Test Show" {
		t.Errorf("series title = %q, want %q", s0.Title, "Test Show")
	}
	if s0.TvdbID != 81189 {
		t.Errorf("series tvdb_id = %d, want %d", s0.TvdbID, 81189)
	}
	if s0.Episodes != 3 {
		t.Errorf("series episodes = %d, want %d", s0.Episodes, 3)
	}
	if s0.AudioLang != "en" {
		t.Errorf("series audio_lang = %q, want %q", s0.AudioLang, "en")
	}
	if s0.Rule != "en" {
		t.Errorf("series rule = %q, want %q", s0.Rule, "en")
	}

	if len(s0.Targets) != 1 {
		t.Fatalf("series targets count = %d, want 1", len(s0.Targets))
	}
	tc := s0.Targets[0]
	if tc.Language != "fr" {
		t.Errorf("target language = %q, want %q", tc.Language, "fr")
	}
	if tc.Have != 1 {
		t.Errorf("target have = %d, want 1 (one external srt)", tc.Have)
	}
	if tc.HaveIgnored != 1 {
		t.Errorf("target have_ignored = %d, want 1 (one ignored pgs)", tc.HaveIgnored)
	}
	if tc.Total != 3 {
		t.Errorf("target total = %d, want 3", tc.Total)
	}
}

// coverageSeriesErrorArrClient returns an error from GetSeries.
type coverageSeriesErrorArrClient struct{ dummyArrClient }

func (c coverageSeriesErrorArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_get_series_error_returns_502(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesErrorArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("handleCoverageSeries(GetSeries error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// coverageSeriesDBErrorStore returns an error from GetSubtitleFiles.
type coverageSeriesDBErrorStore struct{ qhMockStore }

func (m *coverageSeriesDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesDBErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{
		cfg:    &qhMockConfig{},
		sonarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleCoverageSeries(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleCoverageSeries_no_targets_sets_rule_no_targets(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: nil,
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != ruleNoTargets {
		t.Errorf("series rule = %q, want %q", result[0].Rule, ruleNoTargets)
	}
}

// coverageSeriesNoLangArrClient returns a series with no OriginalLanguage.
type coverageSeriesNoLangArrClient struct{ dummyArrClient }

func (c coverageSeriesNoLangArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return []api.Series{
		{
			ID:         1,
			Title:      "No Lang Show",
			TvdbID:     55555,
			Statistics: &api.SeriesStatistics{EpisodeFileCount: 2},
		},
	}, nil
}

func TestHandleCoverageSeries_no_original_language_uses_default_rule(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesNoLangArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != "default" {
		t.Errorf("rule = %q, want %q", result[0].Rule, "default")
	}
}
