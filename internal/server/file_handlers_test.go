package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"subflux/internal/api"
	"subflux/internal/server/activity"
	"subflux/internal/server/events"
)

// --- handleHistoryIDs ---

func TestHandleHistoryIDs_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/state/ids", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleHistoryIDs(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleHistoryIDs_missing_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state/ids", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleHistoryIDs(no type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleHistoryIDs_returns_empty_array_for_nil(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state/ids?type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleHistoryIDs() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleHistoryIDs() body = %q, want %q (empty array, not null)",
			body, "[]")
	}
}

func TestHandleHistoryIDs_passes_type_and_prefix(t *testing.T) {
	t.Parallel()
	db := &historyIDsTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state/ids?type=episode&prefix=tvdb-81189-", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleHistoryIDs() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if db.mediaType != "episode" {
		t.Errorf("HistoryMediaIDs mediaType = %q, want %q",
			db.mediaType, "episode")
	}
	if db.prefix != "tvdb-81189-" {
		t.Errorf("HistoryMediaIDs prefix = %q, want %q",
			db.prefix, "tvdb-81189-")
	}
}

func TestHandleHistoryIDs_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &historyIDsErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state/ids?type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleHistoryIDs(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleHistoryIDs_returns_ids(t *testing.T) {
	t.Parallel()
	db := &historyIDsResultStore{
		ids: []string{"tmdb-123", "tmdb-456"},
	}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state/ids?type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHistoryIDs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleHistoryIDs() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var ids []string
	if err := json.NewDecoder(rec.Body).Decode(&ids); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("handleHistoryIDs() returned %d ids, want 2", len(ids))
	}
}

// --- handleListFiles validation paths ---

func TestHandleListFiles_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/files", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleListFiles(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleListFiles_missing_params_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	tests := []struct {
		name  string
		query string
	}{
		{"no params", ""},
		{"only media_type", "?media_type=movie"},
		{"only media_id", "?media_id=tmdb-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodGet, "/api/files"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			s.handleListFiles(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleListFiles(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

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

// --- Mock stores for handleHistoryIDs ---

type historyIDsTrackingStore struct {
	mediaType api.MediaType
	prefix    string
	qhMockStore
}

func (m *historyIDsTrackingStore) HistoryMediaIDs(_ context.Context, mediaType api.MediaType, prefix string) ([]string, error) {
	m.mediaType = mediaType
	m.prefix = prefix
	return nil, nil
}

type historyIDsErrorStore struct{ qhMockStore }

func (m *historyIDsErrorStore) HistoryMediaIDs(_ context.Context, _ api.MediaType, _ string) ([]string, error) {
	return nil, errMock
}

type historyIDsResultStore struct {
	ids []string
	qhMockStore
}

func (m *historyIDsResultStore) HistoryMediaIDs(_ context.Context, _ api.MediaType, _ string) ([]string, error) {
	return m.ids, nil
}

// --- handleListFiles DB error + happy path ---

type listFilesErrorStore struct{ qhMockStore }

func (m *listFilesErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return nil, errMock
}

func TestHandleListFiles_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &listFilesErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files?media_type=movie&media_id=tmdb-123", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleListFiles(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

type listFilesDataStore struct {
	rows []api.SubtitleFileRow
	qhMockStore
}

func (m *listFilesDataStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return m.rows, nil
}

func TestHandleListFiles_returns_file_entries(t *testing.T) {
	t.Parallel()
	db := &listFilesDataStore{
		rows: []api.SubtitleFileRow{
			{
				MediaID:  "tmdb-123",
				Language: "en",
				Variant:  "standard",
				Source:   "external",
				Codec:    "srt",
				Score:    85,
				OffsetMs: 500,
			},
			{
				MediaID:  "tmdb-123",
				Language: "fr",
				Variant:  "forced",
				Source:   "embedded",
				Codec:    "subrip",
			},
		},
	}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files?media_type=movie&media_id=tmdb-123", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListFiles() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var entries []fileEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("handleListFiles() returned %d entries, want 2", len(entries))
	}
	if entries[0].MediaID != "tmdb-123" {
		t.Errorf("entries[0].MediaID = %q, want %q", entries[0].MediaID, "tmdb-123")
	}
	if entries[0].Language != "en" {
		t.Errorf("entries[0].Language = %q, want %q", entries[0].Language, "en")
	}
	if entries[0].Score != 85 {
		t.Errorf("entries[0].Score = %d, want %d", entries[0].Score, 85)
	}
	if entries[0].OffsetMs != 500 {
		t.Errorf("entries[0].OffsetMs = %d, want %d", entries[0].OffsetMs, 500)
	}
	if entries[0].Size != 0 {
		t.Errorf("entries[0].Size = %d, want 0 (no path, stat skipped)", entries[0].Size)
	}
	if entries[1].Language != "fr" {
		t.Errorf("entries[1].Language = %q, want %q", entries[1].Language, "fr")
	}
	if entries[1].Variant != "forced" {
		t.Errorf("entries[1].Variant = %q, want %q", entries[1].Variant, "forced")
	}
}

func TestHandleListFiles_empty_rows_returns_empty_array(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files?media_type=movie&media_id=tmdb-999", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListFiles() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleListFiles(no rows) body = %q, want %q", body, "[]")
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

func (m *bulkDeleteDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
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

	row := &api.SubtitleFileRow{
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

	row := &api.SubtitleFileRow{
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

	row := &api.SubtitleFileRow{
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

	row := &api.SubtitleFileRow{
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
	rows        []api.SubtitleFileRow
	qhMockStore
}

func (m *bulkDeleteDataStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
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
		rows: []api.SubtitleFileRow{
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

// --- handleListFiles file size enrichment ---

func TestHandleListFiles_populates_size_for_valid_path(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := dir + "/movie.en.srt"
	if err := os.WriteFile(path, []byte("test subtitle content"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &qhMockConfig{}
	db := &listFilesDataStore{
		rows: []api.SubtitleFileRow{
			{
				MediaID:  "tmdb-123",
				Language: "en",
				Variant:  "standard",
				Source:   "external",
				Path:     path,
			},
		},
	}
	s := &Server{
		db:       db,
		stores:   storeFacade{file: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: cfg})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files?media_type=movie&media_id=tmdb-123", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleListFiles() status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var entries []fileEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("handleListFiles() returned %d entries, want 1", len(entries))
	}
	if entries[0].Size != 21 {
		t.Errorf("handleListFiles() entries[0].Size = %d, want 21", entries[0].Size)
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

// --- mediaType validation ---

func TestHandleListFiles_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/files?media_type=foo&media_id=tmdb-123", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleListFiles(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleListFiles(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
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
