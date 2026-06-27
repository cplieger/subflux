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
	"github.com/cplieger/subflux/internal/server/events"
)

// --- handleDeleteFile validation paths ---

func TestHandleDeleteFile_rejects_non_delete(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDeleteFile(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleDeleteFile(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleDeleteFile_missing_params_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	tests := []struct {
		name  string
		query string
	}{
		{"no params", ""},
		{"missing path", "?media_type=movie&media_id=tmdb-123&language=en"},
		{"missing media_type", "?path=/f&media_id=tmdb-123&language=en"},
		{"missing media_id", "?path=/f&media_type=movie&language=en"},
		{"missing language", "?path=/f&media_type=movie&media_id=tmdb-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodDelete, "/api/files"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			s.handleDeleteFile(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleDeleteFile(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// --- handleBulkDeleteFiles validation paths ---

func TestHandleBulkDeleteFiles_rejects_non_delete(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files/bulk", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleBulkDeleteFiles(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBulkDeleteFiles_invalid_json_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/files/bulk", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleBulkDeleteFiles(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleBulkDeleteFiles_missing_fields_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	tests := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"missing media_type", `{"media_id":"tmdb-123"}`},
		{"missing media_id", `{"media_type":"movie"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodDelete, "/api/files/bulk", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			s.handleBulkDeleteFiles(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleBulkDeleteFiles(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// --- handleDeleteFile path validation + default variant ---

func TestHandleDeleteFile_invalid_path_returns_403(t *testing.T) {
	t.Parallel()
	cfg := &pathValidationErrorConfig{}
	s := &Server{
		db:       &qhMockStore{},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: cfg})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete,
		"/api/files?path=/etc/passwd&media_type=movie&media_id=tmdb-123&language=en",
		http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDeleteFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("handleDeleteFile(invalid path) status = %d, want %d",
			rec.Code, http.StatusForbidden)
	}
}

type deleteFileTrackingStore struct {
	variant api.Variant
	qhMockStore
}

func (m *deleteFileTrackingStore) DeleteSubtitleFile(_ context.Context, _ api.MediaType, _, _ string, variant api.Variant, _ api.SubtitleSource, _ string) error {
	m.variant = variant
	return nil
}

func (m *deleteFileTrackingStore) ManualSubtitlePaths(_ context.Context, _ api.MediaType, _, _ string) ([]string, error) {
	return nil, nil
}

func (m *deleteFileTrackingStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string) error {
	return nil
}

func TestHandleDeleteFile_default_variant_is_standard(t *testing.T) {
	t.Parallel()
	db := &deleteFileTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete,
		"/api/files?path=/nonexistent/sub.srt&media_type=movie&media_id=tmdb-123&language=en",
		http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDeleteFile(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("handleDeleteFile() status = %d, want %d",
			rec.Code, http.StatusNoContent)
	}
	if db.variant != "standard" {
		t.Errorf("DeleteSubtitleFile variant = %q, want %q",
			db.variant, "standard")
	}
}

// --- handleBulkDeleteFiles DB error ---

type bulkDeleteDBErrorStore struct{ qhMockStore }

func (m *bulkDeleteDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return nil, errMock
}

func TestHandleBulkDeleteFiles_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &bulkDeleteDBErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	body := `{"media_type":"movie","media_id":"tmdb-123"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/files/bulk", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleBulkDeleteFiles(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// --- deleteExternalFile ---

type deleteExtTrackingStore struct {
	deletedPath string
	qhMockStore
}

func (m *deleteExtTrackingStore) DeleteSubtitleFile(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant, _ api.SubtitleSource, path string) error {
	m.deletedPath = path
	return nil
}

func (m *deleteExtTrackingStore) ManualSubtitlePaths(_ context.Context, _ api.MediaType, _, _ string) ([]string, error) {
	return nil, nil
}

func (m *deleteExtTrackingStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string) error {
	return nil
}

func TestDeleteExternalFile_skips_embedded_source(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	row := &api.SubtitleEntry{
		Source: "embedded",
		Path:   "/media/movie.srt",
	}
	got := s.deleteExternalFile(context.Background(), &qhMockConfig{}, api.MediaTypeMovie, row)
	if got {
		t.Error("deleteExternalFile(embedded) = true, want false")
	}
}

func TestDeleteExternalFile_skips_empty_path(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	row := &api.SubtitleEntry{
		Source: "external",
		Path:   "",
	}
	got := s.deleteExternalFile(context.Background(), &qhMockConfig{}, api.MediaTypeMovie, row)
	if got {
		t.Error("deleteExternalFile(empty path) = true, want false")
	}
}

func TestDeleteExternalFile_skips_invalid_path(t *testing.T) {
	t.Parallel()
	cfg := &pathValidationErrorConfig{}
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	row := &api.SubtitleEntry{
		Source: "external",
		Path:   "/etc/passwd",
	}
	got := s.deleteExternalFile(context.Background(), cfg, api.MediaTypeMovie, row)
	if got {
		t.Error("deleteExternalFile(invalid path) = true, want false")
	}
}

func TestDeleteExternalFile_succeeds_for_nonexistent_file(t *testing.T) {
	t.Parallel()
	db := &deleteExtTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	row := &api.SubtitleEntry{
		MediaID:  "tmdb-123",
		Language: "en",
		Variant:  "standard",
		Source:   "external",
		Path:     "/nonexistent/movie.en.srt",
	}
	got := s.deleteExternalFile(context.Background(), &qhMockConfig{}, api.MediaTypeMovie, row)
	if !got {
		t.Error("deleteExternalFile(nonexistent file) = false, want true")
	}
	if db.deletedPath != "/nonexistent/movie.en.srt" {
		t.Errorf("DeleteSubtitleFile path = %q, want %q",
			db.deletedPath, "/nonexistent/movie.en.srt")
	}
}

// --- handleBulkDeleteFiles happy path ---

type bulkDeleteDataStore struct {
	deletedPath string
	rows        []api.SubtitleEntry
	qhMockStore
}

func (m *bulkDeleteDataStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return m.rows, nil
}

func (m *bulkDeleteDataStore) DeleteSubtitleFile(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant, _ api.SubtitleSource, path string) error {
	m.deletedPath = path
	return nil
}

func (m *bulkDeleteDataStore) ManualSubtitlePaths(_ context.Context, _ api.MediaType, _, _ string) ([]string, error) {
	return nil, nil
}

func (m *bulkDeleteDataStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string) error {
	return nil
}

func TestHandleBulkDeleteFiles_deletes_external_skips_embedded(t *testing.T) {
	t.Parallel()
	db := &bulkDeleteDataStore{
		rows: []api.SubtitleEntry{
			{
				MediaID:  "tmdb-123",
				Language: "en",
				Variant:  "standard",
				Source:   "external",
				Path:     "/nonexistent/movie.en.srt",
			},
			{
				MediaID:  "tmdb-123",
				Language: "fr",
				Variant:  "standard",
				Source:   "embedded",
				Path:     "",
			},
		},
	}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	body := `{"media_type":"movie","media_id":"tmdb-123"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/files/bulk", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBulkDeleteFiles() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var resp map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["deleted"] != 1 {
		t.Errorf("handleBulkDeleteFiles() deleted = %d, want 1", resp["deleted"])
	}
}

func TestHandleBulkDeleteFiles_no_rows_returns_zero(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"movie","media_id":"tmdb-999"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/files/bulk", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBulkDeleteFiles(no rows) status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var resp map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["deleted"] != 0 {
		t.Errorf("handleBulkDeleteFiles(no rows) deleted = %d, want 0", resp["deleted"])
	}
}

// --- handleDeleteFile explicit variant ---

func TestHandleDeleteFile_explicit_variant_passed_through(t *testing.T) {
	t.Parallel()
	db := &deleteFileTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete,
		"/api/files?path=/nonexistent/sub.srt&media_type=movie&media_id=tmdb-123&language=en&variant=forced",
		http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDeleteFile(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("handleDeleteFile() status = %d, want %d",
			rec.Code, http.StatusNoContent)
	}
	if db.variant != "forced" {
		t.Errorf("DeleteSubtitleFile variant = %q, want %q",
			db.variant, "forced")
	}
}

func TestHandleDeleteFile_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete,
		"/api/files?path=/f&media_type=foo&media_id=tmdb-123&language=en",
		http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDeleteFile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleDeleteFile(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleBulkDeleteFiles_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"foo","media_id":"tmdb-123"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/files/bulk", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleBulkDeleteFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleBulkDeleteFiles(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}
