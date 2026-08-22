package synchandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
)

// HTTP-surface tests for the S7 FileRef contract on the sync verbs: the
// wire carries references, the server resolves paths from the store, and
// sync offsets stay keyed by the RESOLVED path.

// syncFakeStore provides subtitle rows plus offset recording.
type syncFakeStore struct {
	rows      []subflux.SubtitleEntry
	offsets   map[string]int64
	setPath   string
	setOffset int64
	getCalled bool
	setCalls  int
}

func (m *syncFakeStore) SubtitleFiles(_ context.Context, _ subflux.MediaType, _ string) ([]subflux.SubtitleEntry, error) {
	return m.rows, nil
}

func (m *syncFakeStore) SyncOffset(_ context.Context, path string) (int64, error) {
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
func (fakeProc) ParseSRT([]byte) ([]subflux.SubtitleCue, error) {
	return []subflux.SubtitleCue{{Start: time.Second, End: 2 * time.Second, Text: "hi"}}, nil
}

func (fakeProc) WriteSRT([]subflux.SubtitleCue) ([]byte, error) {
	return []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), nil
}

func (fakeProc) ShiftCues(cues []subflux.SubtitleCue, _ time.Duration) []subflux.SubtitleCue {
	return cues
}

func (fakeProc) SyncFromAudio(context.Context, []byte, string, string) subflux.AudioSyncResult {
	return subflux.AudioSyncResult{Applied: false, Method: "audio", Confidence: 0.1}
}

func newSyncHarness(store *syncFakeStore) *Handler {
	return newSyncHarnessWithProc(store, fakeProc{})
}

func newSyncHarnessWithProc(store *syncFakeStore, proc SubtitleProcessor) *Handler {
	cfg := fakePathValidator{}
	return New(Deps{
		Store:        store,
		SubtitleProc: proc,
		Activity:     activity.New(10),
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: cfg} },
		},
	})
}

// recordingProc is fakeProc with the two things the shift assertions need:
// it keeps the cues handed to WriteSRT (the only place the shifted times are
// observable), and its audio sync answers with a configured result.
type recordingProc struct {
	fakeProc
	audio   subflux.AudioSyncResult
	written []subflux.SubtitleCue
}

func (p *recordingProc) WriteSRT(cues []subflux.SubtitleCue) ([]byte, error) {
	p.written = cues
	return p.fakeProc.WriteSRT(cues)
}

func (p *recordingProc) SyncFromAudio(context.Context, []byte, string, string) subflux.AudioSyncResult {
	return p.audio
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
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath,
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/offset", refBody(t, SyncOffsetRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", OffsetMs: 250,
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
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
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", OffsetMs: 250,
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
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

// TestHandleSyncOffset_shifts_by_the_delta_from_the_stored_offset pins the
// arithmetic of a manual offset: the request carries an ABSOLUTE offset, the
// file on disk already carries the previously applied one, so the cues move
// by the difference and the recorded offset is the absolute value. Shifting
// by the absolute offset instead would double-apply on every adjustment.
func TestHandleSyncOffset_shifts_by_the_delta_from_the_stored_offset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(subPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &syncFakeStore{
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath,
		}},
		offsets: map[string]int64{subPath: 100},
	}
	proc := &recordingProc{}
	h := newSyncHarnessWithProc(store, proc)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/offset", refBody(t, SyncOffsetRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", OffsetMs: 250,
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncOffset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// The parsed cue is 1s -> 2s and the delta is 250-100 = 150ms.
	want := []subflux.SubtitleCue{{
		Start: 1150 * time.Millisecond, End: 2150 * time.Millisecond, Text: "hi",
	}}
	if !slices.Equal(proc.written, want) {
		t.Errorf("cues written for offset 250 over stored 100 = %v, want %v", proc.written, want)
	}
	if store.setOffset != 250 {
		t.Errorf("recorded offset = %d, want the absolute 250", store.setOffset)
	}
}

// TestHandleSyncAudio_applies_the_result_when_cues_are_returned pins the
// writeback gate: an applied, non-dry-run audio sync that came back WITH
// cues must reach the file and the offset store, and the response must carry
// the cumulative offset rather than the raw audio one.
func TestHandleSyncAudio_applies_the_result_when_cues_are_returned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "movie.en.srt")
	videoPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(subPath, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &syncFakeStore{
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath, VideoPath: videoPath,
		}},
		offsets: map[string]int64{subPath: 40},
	}
	proc := &recordingProc{audio: subflux.AudioSyncResult{
		Applied: true, Offset: 60, Confidence: 0.9, Method: "audio",
		Cues: []subflux.SubtitleCue{{Start: time.Second, End: 2 * time.Second, Text: "hi"}},
	}}
	h := newSyncHarnessWithProc(store, proc)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if store.setCalls != 1 {
		t.Errorf("SetSyncOffset calls = %d, want 1 (the applied result must be recorded)", store.setCalls)
	}
	if store.setOffset != 100 {
		t.Errorf("recorded offset = %d, want 100 (40 stored + 60 audio)", store.setOffset)
	}
	var resp SyncAudioResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.OffsetMs != 100 {
		t.Errorf("response offset_ms = %d, want the cumulative 100", resp.OffsetMs)
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
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath, VideoPath: videoPath,
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	// Non-dry-run apply on an ASS subtitle: refused with the format code.
	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (ASS apply refusal)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sync_unsupported_format") {
		t.Errorf("body = %q, want sync_unsupported_format", rec.Body.String())
	}

	// Dry-run is allowed (inspect-only).
	req = httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", DryRun: true,
	}))
	rec = httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("dry-run status = %d, want 200: %s", rec.Code, rec.Body.String())
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
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath, // no VideoPath anywhere
		}},
		offsets: map[string]int64{},
	}
	h := newSyncHarness(store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no video path derivable)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "media_not_found") {
		t.Errorf("body = %q, want media_not_found", rec.Body.String())
	}
}
