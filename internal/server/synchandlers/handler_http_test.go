package synchandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/testsupport"
)

// HTTP-surface tests for the S7 FileRef contract on the sync verbs: the
// wire carries references, the server resolves paths from the store, and
// sync offsets stay keyed by the RESOLVED path.

// syncFakeStore provides subtitle rows plus offset recording.
type syncFakeStore struct {
	rows      []api.SubtitleEntry
	offsets   map[string]int64
	setPath   string
	setOffset int64
	getCalled bool
	setCalls  int
}

func (m *syncFakeStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
	return m.rows, nil
}

func (m *syncFakeStore) GetSyncOffset(_ context.Context, path string) (int64, error) {
	m.getCalled = true
	return m.offsets[path], nil
}

func (m *syncFakeStore) SetSyncOffset(_ context.Context, path string, offsetMs int64) error {
	m.setPath, m.setOffset = path, offsetMs
	m.setCalls++
	return nil
}

// fakeProc is a minimal SubtitleProcessor: identity encoding, trivial SRT
// parse/write, never-applied audio sync.
type fakeProc struct{}

func (fakeProc) NormalizeEncoding(data []byte) []byte { return data }
func (fakeProc) ParseSRT([]byte) ([]api.SubtitleCue, error) {
	return []api.SubtitleCue{{Start: time.Second, End: 2 * time.Second, Text: "hi"}}, nil
}
func (fakeProc) WriteSRT([]api.SubtitleCue) ([]byte, error) {
	return []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), nil
}
func (fakeProc) ShiftCues(cues []api.SubtitleCue, _ time.Duration) []api.SubtitleCue { return cues }
func (fakeProc) SyncFromAudio(context.Context, []byte, string, string) api.AudioSyncResult {
	return api.AudioSyncResult{Applied: false, Method: "audio", Confidence: 0.1}
}

func newSyncHarness(store *syncFakeStore) *Handler {
	cfg := &testsupport.NopConfig{}
	return New(Deps{
		Store:        store,
		SubtitleProc: fakeProc{},
		Activity:     activity.New(10),
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: cfg} },
		},
	})
}

func refBody(t *testing.T, v any) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

func TestHandleSyncOffset_resolvedRefKeysOffsetByPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(subPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &syncFakeStore{
		rows: []api.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(api.SourceExternal), Path: subPath,
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/offset", refBody(t, SyncOffsetRequest{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", OffsetMs: 250,
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if store.setPath != subPath {
		t.Errorf("offset recorded for %q, want the RESOLVED path %q", store.setPath, subPath)
	}
	if store.setOffset != 250 {
		t.Errorf("offset = %d, want 250", store.setOffset)
	}
}

func TestHandleSyncOffset_unresolvedRefReturns404(t *testing.T) {
	t.Parallel()
	store := &syncFakeStore{offsets: map[string]int64{}}
	h := newSyncHarness(store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/offset", refBody(t, SyncOffsetRequest{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", OffsetMs: 250,
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "subtitle_not_found") {
		t.Errorf("body = %q, want subtitle_not_found", rec.Body.String())
	}
	if store.setCalls != 0 {
		t.Error("offset written despite unresolved reference")
	}
}

func TestHandleSyncOffset_missingRefFieldsReturns400(t *testing.T) {
	t.Parallel()
	h := newSyncHarness(&syncFakeStore{offsets: map[string]int64{}})
	req := httptest.NewRequest(http.MethodPost, "/api/sync/offset",
		strings.NewReader(`{"offset_ms":250}`))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSyncAudio_assGateOnResolvedPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "movie.en.ass")
	videoPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(subPath, []byte("[Script Info]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &syncFakeStore{
		rows: []api.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(api.SourceExternal), Path: subPath, VideoPath: videoPath,
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	// Non-dry-run apply on an ASS subtitle: refused with the format code.
	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (ASS apply refusal)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sync_unsupported_format") {
		t.Errorf("body = %q, want sync_unsupported_format", rec.Body.String())
	}

	// Dry-run is allowed (inspect-only).
	req = httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", DryRun: true,
	}))
	rec = httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSyncAudio_noVideoRecordedReturns404(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(subPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &syncFakeStore{
		rows: []api.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(api.SourceExternal), Path: subPath, // no VideoPath anywhere
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: api.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no video path derivable)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "media_not_found") {
		t.Errorf("body = %q, want media_not_found", rec.Body.String())
	}
}
