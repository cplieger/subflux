package filehandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/testsupport"
)

var errMock = errors.New("mock error")

// fakeFileStore implements FileStore with canned rows and recorded calls.
type fakeFileStore struct {
	getErr      error
	histErr     error
	deletedPath string
	histPrefix  string
	rows        []api.SubtitleEntry
	histIDs     []string
	variant     api.Variant
	histType    api.MediaType
}

func (m *fakeFileStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return m.rows, m.getErr
}

func (m *fakeFileStore) DeleteSubtitleFile(_ context.Context, _ api.MediaType, _, _ string, variant api.Variant, _ api.SubtitleSource, path string) error {
	m.variant = variant
	m.deletedPath = path
	return nil
}

func (m *fakeFileStore) ManualSubtitlePaths(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant) ([]string, error) {
	return nil, nil
}

func (m *fakeFileStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant) error {
	return nil
}

func (m *fakeFileStore) HistoryMediaIDs(_ context.Context, mediaType api.MediaType, prefix string) ([]string, error) {
	m.histType = mediaType
	m.histPrefix = prefix
	return m.histIDs, m.histErr
}

// newFileHandler builds a Handler around the given store and config, with a
// resolver over the same store (as server_init wires it).
func newFileHandler(store FileStore, cfg *testsupport.NopConfig) *Handler {
	return NewHandler(Deps{
		Store: store,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: cfg} },
		},
		StateFunc: func() *LiveState { return &LiveState{Cfg: cfg} },
		Events:    events.New(0),
	})
}

// deleteBody builds the FileRef JSON body for DELETE /api/files.
func deleteBody(mediaType, mediaID, language, variant string, ordinal int) string {
	b, _ := json.Marshal(DeleteFileRequest{
		MediaType: api.MediaType(mediaType), MediaID: mediaID,
		Language: language, Variant: variant, Ordinal: ordinal,
	})
	return string(b)
}

// --- HandleHistoryIDs ---

func TestHandleHistoryIDs(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_get", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodPost, "/api/state/ids", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleHistoryIDs(POST) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("missing_type_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/state/ids", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleHistoryIDs(no type) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("returns_empty_array_for_nil", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/state/ids?type=movie", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleHistoryIDs() status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := strings.TrimSpace(rec.Body.String())
		if body != "[]" {
			t.Errorf("HandleHistoryIDs() body = %q, want %q (empty array, not null)",
				body, "[]")
		}
	})

	t.Run("passes_type_and_prefix", func(t *testing.T) {
		t.Parallel()
		store := &fakeFileStore{}
		h := newFileHandler(store, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet,
			"/api/state/ids?type=episode&prefix=tvdb-81189-", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleHistoryIDs() status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.histType != "episode" {
			t.Errorf("HistoryMediaIDs mediaType = %q, want %q", store.histType, "episode")
		}
		if store.histPrefix != "tvdb-81189-" {
			t.Errorf("HistoryMediaIDs prefix = %q, want %q", store.histPrefix, "tvdb-81189-")
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{histErr: errMock}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/state/ids?type=movie", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleHistoryIDs(db error) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("returns_ids", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{histIDs: []string{"tmdb-123", "tmdb-456"}},
			&testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/state/ids?type=movie", nil)
		rec := httptest.NewRecorder()
		h.HandleHistoryIDs(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleHistoryIDs() status = %d, want %d", rec.Code, http.StatusOK)
		}
		var ids []string
		if err := json.NewDecoder(rec.Body).Decode(&ids); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(ids) != 2 {
			t.Errorf("HandleHistoryIDs() returned %d ids, want 2", len(ids))
		}
	})
}

// --- HandleListFiles ---

func TestHandleListFiles(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_get", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodPost, "/api/files", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleListFiles(POST) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("missing_params_returns_400", func(t *testing.T) {
		t.Parallel()
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
				h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
				req := httptest.NewRequest(http.MethodGet, "/api/files"+tt.query, nil)
				rec := httptest.NewRecorder()
				h.HandleListFiles(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("HandleListFiles(%s) status = %d, want %d",
						tt.name, rec.Code, http.StatusBadRequest)
				}
			})
		}
	})

	t.Run("invalid_media_type_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet,
			"/api/files?media_type=foo&media_id=tmdb-123", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleListFiles(invalid media_type) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{getErr: errMock}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet,
			"/api/files?media_type=movie&media_id=tmdb-123", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleListFiles(db error) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("returns_file_entries", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{rows: []api.SubtitleEntry{
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
		}}, &testsupport.NopConfig{})

		req := httptest.NewRequest(http.MethodGet,
			"/api/files?media_type=movie&media_id=tmdb-123", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HandleListFiles() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var entries []FileEntry
		if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("HandleListFiles() returned %d entries, want 2", len(entries))
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
	})

	t.Run("empty_rows_returns_empty_array", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet,
			"/api/files?media_type=movie&media_id=tmdb-999", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HandleListFiles() status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := strings.TrimSpace(rec.Body.String())
		if body != "[]" {
			t.Errorf("HandleListFiles(no rows) body = %q, want %q", body, "[]")
		}
	})

	t.Run("populates_size_for_valid_path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := dir + "/movie.en.srt"
		if err := os.WriteFile(path, []byte("test subtitle content"), 0o644); err != nil {
			t.Fatal(err)
		}

		h := newFileHandler(&fakeFileStore{rows: []api.SubtitleEntry{
			{
				MediaID:  "tmdb-123",
				Language: "en",
				Variant:  "standard",
				Source:   "external",
				Path:     path,
			},
		}}, &testsupport.NopConfig{})

		req := httptest.NewRequest(http.MethodGet,
			"/api/files?media_type=movie&media_id=tmdb-123", nil)
		rec := httptest.NewRecorder()
		h.HandleListFiles(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HandleListFiles() status = %d, want %d", rec.Code, http.StatusOK)
		}

		var entries []FileEntry
		if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("HandleListFiles() returned %d entries, want 1", len(entries))
		}
		if entries[0].Size != 21 {
			t.Errorf("HandleListFiles() entries[0].Size = %d, want 21", entries[0].Size)
		}
	})
}

// --- HandleDeleteFile ---

func TestHandleDeleteFile(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_delete", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
		rec := httptest.NewRecorder()
		h.HandleDeleteFile(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleDeleteFile(GET) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("missing_fields_returns_400", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			body string
		}{
			{"empty object", `{}`},
			{"missing media_type", `{"media_id":"tmdb-123","language":"en"}`},
			{"missing media_id", `{"media_type":"movie","language":"en"}`},
			{"missing language", `{"media_type":"movie","media_id":"tmdb-123"}`},
			{"invalid media_type", `{"media_type":"foo","media_id":"tmdb-123","language":"en"}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
				req := httptest.NewRequest(http.MethodDelete, "/api/files",
					strings.NewReader(tt.body))
				rec := httptest.NewRecorder()
				h.HandleDeleteFile(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("HandleDeleteFile(%s) status = %d, want %d",
						tt.name, rec.Code, http.StatusBadRequest)
				}
			})
		}
	})

	t.Run("containment_invariant_returns_500", func(t *testing.T) {
		t.Parallel()
		// The store resolves the FileRef, but the resolved path fails the
		// containment check: with server-derived paths that is an internal
		// invariant breach (500), never a client-attributable 4xx.
		store := &fakeFileStore{rows: []api.SubtitleEntry{{
			MediaID:  "tmdb-123",
			Language: "en",
			Variant:  "standard",
			Source:   "external",
			Path:     "/etc/movie.en.srt",
		}}}
		h := newFileHandler(store, &testsupport.NopConfig{PathErr: config.ErrPathNotAllowed})

		req := httptest.NewRequest(http.MethodDelete, "/api/files",
			strings.NewReader(deleteBody("movie", "tmdb-123", "en", "", 0)))
		rec := httptest.NewRecorder()
		h.HandleDeleteFile(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleDeleteFile(containment breach) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("default_variant_is_standard", func(t *testing.T) {
		t.Parallel()
		store := &fakeFileStore{rows: []api.SubtitleEntry{{
			MediaID:  "tmdb-123",
			Language: "en",
			Variant:  "standard",
			Source:   "external",
			Path:     "/nonexistent/sub.srt",
		}}}
		h := newFileHandler(store, &testsupport.NopConfig{})

		req := httptest.NewRequest(http.MethodDelete, "/api/files",
			strings.NewReader(deleteBody("movie", "tmdb-123", "en", "", 0)))
		rec := httptest.NewRecorder()
		h.HandleDeleteFile(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("HandleDeleteFile() status = %d, want %d",
				rec.Code, http.StatusNoContent)
		}
		if store.variant != "standard" {
			t.Errorf("DeleteSubtitleFile variant = %q, want %q", store.variant, "standard")
		}
	})

	t.Run("explicit_variant_passed_through", func(t *testing.T) {
		t.Parallel()
		store := &fakeFileStore{rows: []api.SubtitleEntry{{
			MediaID:  "tmdb-123",
			Language: "en",
			Variant:  "forced",
			Source:   "external",
			Path:     "/nonexistent/sub.srt",
		}}}
		h := newFileHandler(store, &testsupport.NopConfig{})

		req := httptest.NewRequest(http.MethodDelete, "/api/files",
			strings.NewReader(deleteBody("movie", "tmdb-123", "en", "forced", 0)))
		rec := httptest.NewRecorder()
		h.HandleDeleteFile(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("HandleDeleteFile() status = %d, want %d",
				rec.Code, http.StatusNoContent)
		}
		if store.variant != "forced" {
			t.Errorf("DeleteSubtitleFile variant = %q, want %q", store.variant, "forced")
		}
	})
}

// --- HandleDeleteFile reference resolution gate ---

// A FileRef that does not resolve to a stored external row answers 404
// subtitle_not_found; nothing on disk is touched. This replaces the old
// path-ownership gate: there is no client path to mismatch anymore.
func TestHandleDeleteFile_unresolved_ref_returns_404(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// rows returns the store rows given an on-disk victim path.
		rows func(victim string) []api.SubtitleEntry
		name string
		body func() string
	}{
		{
			name: "no rows for media",
			rows: func(string) []api.SubtitleEntry { return nil },
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 0) },
		},
		{
			name: "different variant",
			rows: func(victim string) []api.SubtitleEntry {
				return []api.SubtitleEntry{{
					MediaID: "tmdb-123", Language: "en", Variant: "forced",
					Source: "external", Path: victim,
				}}
			},
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 0) },
		},
		{
			name: "different language",
			rows: func(victim string) []api.SubtitleEntry {
				return []api.SubtitleEntry{{
					MediaID: "tmdb-123", Language: "fr", Variant: "standard",
					Source: "external", Path: victim,
				}}
			},
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 0) },
		},
		{
			name: "different media id",
			rows: func(victim string) []api.SubtitleEntry {
				return []api.SubtitleEntry{{
					MediaID: "tmdb-999", Language: "en", Variant: "standard",
					Source: "external", Path: victim,
				}}
			},
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 0) },
		},
		{
			name: "wrong ordinal",
			rows: func(victim string) []api.SubtitleEntry {
				return []api.SubtitleEntry{{
					MediaID: "tmdb-123", Language: "en", Variant: "standard",
					Source: "external", Path: victim,
				}}
			},
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 3) },
		},
		{
			name: "embedded rows are not addressable",
			rows: func(victim string) []api.SubtitleEntry {
				return []api.SubtitleEntry{{
					MediaID: "tmdb-123", Language: "en", Variant: "standard",
					Source: "embedded", Path: victim,
				}}
			},
			body: func() string { return deleteBody("movie", "tmdb-123", "en", "", 0) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			victim := filepath.Join(t.TempDir(), "video.en.srt")
			if err := os.WriteFile(victim, []byte("data"), 0o644); err != nil {
				t.Fatalf("WriteFile() unexpected error: %v", err)
			}
			store := &fakeFileStore{rows: tt.rows(victim)}
			h := newFileHandler(store, &testsupport.NopConfig{})

			req := httptest.NewRequest(http.MethodDelete, "/api/files",
				strings.NewReader(tt.body()))
			rec := httptest.NewRecorder()
			h.HandleDeleteFile(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("HandleDeleteFile(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusNotFound)
			}
			if !strings.Contains(rec.Body.String(), "subtitle_not_found") {
				t.Errorf("body = %q, want machine code subtitle_not_found", rec.Body.String())
			}
			if _, err := os.Stat(victim); err != nil {
				t.Errorf("file was deleted despite unresolved reference: %v", err)
			}
			if store.variant != "" {
				t.Errorf("DeleteSubtitleFile was called (variant %q), want no DB delete", store.variant)
			}
		})
	}
}

// A resolving FileRef deletes exactly the addressed file — including the
// ordinal-disambiguated manual sibling — and cleans up the DB row.
func TestHandleDeleteFile_resolved_ref_deletes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	autoPath := filepath.Join(dir, "movie.en.srt")
	manualPath := filepath.Join(dir, "movie.en.1.srt")
	for _, p := range []string{autoPath, manualPath} {
		if err := os.WriteFile(p, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() unexpected error: %v", err)
		}
	}
	store := &fakeFileStore{rows: []api.SubtitleEntry{
		{
			MediaID: "tmdb-123", Language: "en", Variant: "standard",
			Source: "external", Path: autoPath, Ordinal: 0,
		},
		{
			MediaID: "tmdb-123", Language: "en", Variant: "standard",
			Source: "external", Path: manualPath, Ordinal: 1,
		},
	}}
	h := newFileHandler(store, &testsupport.NopConfig{})

	// Delete the ordinal-1 manual sibling; the auto file must survive.
	req := httptest.NewRequest(http.MethodDelete, "/api/files",
		strings.NewReader(deleteBody("movie", "tmdb-123", "en", "", 1)))
	rec := httptest.NewRecorder()
	h.HandleDeleteFile(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("HandleDeleteFile(ordinal 1) status = %d, want %d: %s",
			rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if _, err := os.Stat(manualPath); !os.IsNotExist(err) {
		t.Error("addressed manual sibling still exists after delete")
	}
	if _, err := os.Stat(autoPath); err != nil {
		t.Errorf("auto file was deleted; ordinal disambiguation failed: %v", err)
	}
	if store.deletedPath != manualPath {
		t.Errorf("DeleteSubtitleFile path = %q, want %q", store.deletedPath, manualPath)
	}
}

// A stored row whose path has a non-subtitle extension is refused with 409
// subtitle_extension_not_allowed (S16 loud refusal, never a silent skip).
func TestHandleDeleteFile_extension_refused_returns_409(t *testing.T) {
	t.Parallel()
	victim := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(victim, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	// A (hypothetically corrupted) store row pointing at a video file.
	store := &fakeFileStore{rows: []api.SubtitleEntry{{
		MediaID: "tmdb-123", Language: "en", Variant: "standard",
		Source: "external", Path: victim,
	}}}
	h := newFileHandler(store, &testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodDelete, "/api/files",
		strings.NewReader(deleteBody("movie", "tmdb-123", "en", "", 0)))
	rec := httptest.NewRecorder()
	h.HandleDeleteFile(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("HandleDeleteFile(.mkv target) status = %d, want %d",
			rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "subtitle_extension_not_allowed") {
		t.Errorf("body = %q, want machine code subtitle_extension_not_allowed", rec.Body.String())
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("non-subtitle file was deleted: %v", err)
	}
	if store.variant != "" {
		t.Error("DB row deleted despite extension refusal")
	}
}

// A store failure during resolution must refuse the delete (fail closed)
// rather than falling through to the filesystem removal.
func TestHandleDeleteFile_store_lookup_error_returns_500(t *testing.T) {
	t.Parallel()
	victim := filepath.Join(t.TempDir(), "video.en.srt")
	if err := os.WriteFile(victim, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	h := newFileHandler(&fakeFileStore{getErr: errMock}, &testsupport.NopConfig{})

	req := httptest.NewRequest(http.MethodDelete, "/api/files",
		strings.NewReader(deleteBody("movie", "tmdb-123", "en", "", 0)))
	rec := httptest.NewRecorder()
	h.HandleDeleteFile(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleDeleteFile(store error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("file was deleted despite store lookup failure: %v", err)
	}
}

// --- HandleBulkDeleteFiles ---

func TestHandleBulkDeleteFiles(t *testing.T) {
	t.Parallel()

	t.Run("rejects_non_delete", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodGet, "/api/files/bulk", nil)
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("HandleBulkDeleteFiles(GET) status = %d, want %d",
				rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("invalid_json_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader("not json"))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleBulkDeleteFiles(bad json) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("missing_fields_returns_400", func(t *testing.T) {
		t.Parallel()
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
				h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
				req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
					strings.NewReader(tt.body))
				rec := httptest.NewRecorder()
				h.HandleBulkDeleteFiles(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("HandleBulkDeleteFiles(%s) status = %d, want %d",
						tt.name, rec.Code, http.StatusBadRequest)
				}
			})
		}
	})

	t.Run("invalid_media_type_returns_400", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		body := `{"media_type":"foo","media_id":"tmdb-123"}`
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("HandleBulkDeleteFiles(invalid media_type) status = %d, want %d",
				rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{getErr: errMock}, &testsupport.NopConfig{})
		body := `{"media_type":"movie","media_id":"tmdb-123"}`
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("HandleBulkDeleteFiles(db error) status = %d, want %d",
				rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("deletes_external_skips_embedded", func(t *testing.T) {
		t.Parallel()
		store := &fakeFileStore{rows: []api.SubtitleEntry{
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
		}}
		h := newFileHandler(store, &testsupport.NopConfig{})

		body := `{"media_type":"movie","media_id":"tmdb-123"}`
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HandleBulkDeleteFiles() status = %d, want %d",
				rec.Code, http.StatusOK)
		}

		var resp map[string]int
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp["deleted"] != 1 {
			t.Errorf("HandleBulkDeleteFiles() deleted = %d, want 1", resp["deleted"])
		}
	})

	// S16 preflight: a bulk set containing one deletable row and one row
	// whose stored path has a refused extension answers 409 with the
	// machine code, deletes NOTHING (disk and DB), and leaves both files
	// present — never 200 {"deleted":N} with a silent skip.
	t.Run("mixed_set_refused_extension_409_zero_deletions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		allowed := filepath.Join(dir, "movie.en.srt")
		refused := filepath.Join(dir, "movie.en.mkv") // corrupted row: video ext
		for _, p := range []string{allowed, refused} {
			if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
				t.Fatalf("WriteFile() unexpected error: %v", err)
			}
		}
		store := &fakeFileStore{rows: []api.SubtitleEntry{
			{
				MediaID: "tmdb-123", Language: "en", Variant: "standard",
				Source: "external", Path: allowed,
			},
			{
				MediaID: "tmdb-123", Language: "en", Variant: "forced",
				Source: "external", Path: refused,
			},
		}}
		h := newFileHandler(store, &testsupport.NopConfig{})

		body := `{"media_type":"movie","media_id":"tmdb-123"}`
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("HandleBulkDeleteFiles(mixed set) status = %d, want %d: %s",
				rec.Code, http.StatusConflict, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "subtitle_extension_not_allowed") {
			t.Errorf("body = %q, want machine code subtitle_extension_not_allowed",
				rec.Body.String())
		}
		for _, p := range []string{allowed, refused} {
			if _, err := os.Stat(p); err != nil {
				t.Errorf("file %q deleted despite preflight refusal: %v", p, err)
			}
		}
		if store.deletedPath != "" {
			t.Errorf("DB delete ran for %q despite preflight refusal", store.deletedPath)
		}
	})

	t.Run("no_rows_returns_zero", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		body := `{"media_type":"movie","media_id":"tmdb-999"}`
		req := httptest.NewRequest(http.MethodDelete, "/api/files/bulk",
			strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleBulkDeleteFiles(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("HandleBulkDeleteFiles(no rows) status = %d, want %d",
				rec.Code, http.StatusOK)
		}

		var resp map[string]int
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp["deleted"] != 0 {
			t.Errorf("HandleBulkDeleteFiles(no rows) deleted = %d, want 0", resp["deleted"])
		}
	})
}

// --- DeleteExternalFile ---

func TestDeleteExternalFile(t *testing.T) {
	t.Parallel()

	t.Run("skips_embedded_source", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		row := &api.SubtitleEntry{
			Source: "embedded",
			Path:   "/media/movie.srt",
		}
		got := h.DeleteExternalFile(context.Background(), &testsupport.NopConfig{}, api.MediaTypeMovie, row)
		if got {
			t.Error("DeleteExternalFile(embedded) = true, want false")
		}
	})

	t.Run("skips_empty_path", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		row := &api.SubtitleEntry{
			Source: "external",
			Path:   "",
		}
		got := h.DeleteExternalFile(context.Background(), &testsupport.NopConfig{}, api.MediaTypeMovie, row)
		if got {
			t.Error("DeleteExternalFile(empty path) = true, want false")
		}
	})

	t.Run("skips_invalid_path", func(t *testing.T) {
		t.Parallel()
		h := newFileHandler(&fakeFileStore{}, &testsupport.NopConfig{})
		row := &api.SubtitleEntry{
			Source: "external",
			Path:   "/etc/passwd",
		}
		got := h.DeleteExternalFile(context.Background(),
			&testsupport.NopConfig{PathErr: config.ErrPathNotAllowed}, api.MediaTypeMovie, row)
		if got {
			t.Error("DeleteExternalFile(invalid path) = true, want false")
		}
	})

	t.Run("succeeds_for_nonexistent_file", func(t *testing.T) {
		t.Parallel()
		store := &fakeFileStore{}
		h := newFileHandler(store, &testsupport.NopConfig{})
		row := &api.SubtitleEntry{
			MediaID:  "tmdb-123",
			Language: "en",
			Variant:  "standard",
			Source:   "external",
			Path:     "/nonexistent/movie.en.srt",
		}
		got := h.DeleteExternalFile(context.Background(), &testsupport.NopConfig{}, api.MediaTypeMovie, row)
		if !got {
			t.Error("DeleteExternalFile(nonexistent file) = false, want true")
		}
		if store.deletedPath != "/nonexistent/movie.en.srt" {
			t.Errorf("DeleteSubtitleFile path = %q, want %q",
				store.deletedPath, "/nonexistent/movie.en.srt")
		}
	})
}
