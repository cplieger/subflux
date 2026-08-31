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
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/syncworker"
)

// HTTP-surface tests for the S7 FileRef contract on the sync verbs: the
// wire carries references, the server resolves paths from the store, and
// sync offsets stay keyed by the RESOLVED path. POST /api/sync/audio is
// async (D1): validation is synchronous, the analysis is a dispatched job.

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
// parse/write.
type fakeProc struct{}

func (fakeProc) NormalizeEncoding(data []byte) []byte { return data }
func (fakeProc) ParseSRT([]byte) ([]subflux.SubtitleCue, error) {
	return []subflux.SubtitleCue{{Start: time.Second, End: 2 * time.Second, Text: "hi"}}, nil
}

func (fakeProc) WriteSRT([]subflux.SubtitleCue) ([]byte, error) {
	return []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), nil
}

// fakeRunner is the typed sync core double: it honors the admission-hook
// contract and answers the configured outcome.
type fakeRunner struct {
	out syncworker.RunOutcome
}

func (r *fakeRunner) RunAudio(_ context.Context, _ []byte, _, _ string, hook syncworker.AdmissionHook) syncworker.RunOutcome {
	if hook != nil && !hook() {
		return syncworker.RunOutcome{Outcome: syncworker.OutcomeCancelled, Err: syncworker.ErrAdmissionRefused}
	}
	return r.out
}

// syncHarness is the async sync stack over fakes: handler + dispatcher +
// its run loop, torn down by t.Cleanup.
type syncHarness struct {
	h    *Handler
	d    *syncjobs.Dispatcher
	log  *activity.Log
	done chan struct{}
}

func newSyncHarness(t *testing.T, store *syncFakeStore) *syncHarness {
	t.Helper()
	return newSyncHarnessWith(t, store, fakeProc{}, &fakeRunner{out: syncworker.RunOutcome{
		Outcome: syncworker.OutcomeResult,
		Result:  subsync.SyncResult{Method: subsync.MethodNone, Confidence: 0.1},
	}})
}

func newSyncHarnessWith(t *testing.T, store *syncFakeStore, proc SubtitleProcessor, runner AudioJobRunner) *syncHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	log := activity.New(50)
	exec := &AudioExecutor{Store: store, Proc: proc, Runner: runner}
	d := syncjobs.New(syncjobs.Deps{
		Exec:        exec.Execute,
		Log:         log,
		Stops:       &activity.StopRegistry{},
		PublishDone: func(*events.SyncDoneEvent) {},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	cfg := fakePathValidator{}
	h := New(Deps{
		Store:        store,
		SubtitleProc: proc,
		Jobs:         d,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: cfg} },
		},
	})
	return &syncHarness{h: h, d: d, log: log, done: done}
}

// recordingProc is fakeProc with the one thing the shift assertions need: it
// keeps the cues handed to WriteSRT (the only place shifted times are
// observable).
type recordingProc struct {
	fakeProc
	written []subflux.SubtitleCue
}

func (p *recordingProc) WriteSRT(cues []subflux.SubtitleCue) ([]byte, error) {
	p.written = cues
	return p.fakeProc.WriteSRT(cues)
}

func refBody(t *testing.T, v any) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

// awaitJob polls the registry until the job settles, then returns it.
func awaitJob(t *testing.T, d *syncjobs.Dispatcher, id int64) syncjobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, j := range d.Jobs("") {
			if j.JobID == id && j.State == syncjobs.StateDone {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d never settled; registry: %+v", id, d.Jobs(""))
		}
		time.Sleep(2 * time.Millisecond)
	}
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
	h := newSyncHarness(t, store).h

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
	h := newSyncHarness(t, store).h

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
	h := newSyncHarness(t, &syncFakeStore{offsets: map[string]int64{}}).h
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
	h := newSyncHarnessWith(t, store, proc, &fakeRunner{}).h

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

// TestHandleSyncAudio_dispatches_and_the_job_applies_the_result pins the
// async contract end to end: validation answers 202 {activity_id, job_id},
// the JOB (not the request) runs the analysis, and an applied non-dry-run
// result reaches the file and the offset store with the CUMULATIVE offset.
// The request context is cancelled right after the 202 — the analysis
// outlives the request and its disconnect by design.
func TestHandleSyncAudio_dispatches_and_the_job_applies_the_result(t *testing.T) {
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
	proc := &recordingProc{}
	runner := &fakeRunner{out: syncworker.RunOutcome{
		Outcome: syncworker.OutcomeResult,
		Result: subsync.SyncResult{
			Method: subsync.MethodAudio, Offset: 60, Confidence: 0.9,
			Cues: []subsync.Cue{{Start: time.Second, End: 2 * time.Second, Text: "hi"}},
		},
	}}
	hs := newSyncHarnessWith(t, store, proc, runner)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	})).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	hs.h.HandleSyncAudio(rec, req)
	cancelReq() // the client walked away; the accepted job must not care

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var resp SyncAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID == 0 || resp.ActivityID == "" {
		t.Fatalf("202 body = %+v, want both ids", resp)
	}

	job := awaitJob(t, hs.d, resp.JobID)
	if job.Outcome != subflux.JobResult || !job.Applied {
		t.Fatalf("job = %+v, want an applied result", job)
	}
	if job.OffsetMs != 100 {
		t.Errorf("job offset_ms = %d, want the cumulative 100 (40 stored + 60 audio)", job.OffsetMs)
	}
	if store.setCalls != 1 || store.setOffset != 100 {
		t.Errorf("SetSyncOffset = (%d calls, %d), want (1, 100)", store.setCalls, store.setOffset)
	}
	if len(proc.written) == 0 {
		t.Error("no cues written; the job must apply the result to disk")
	}
}

// TestHandleSyncAudio_dedupe_answers_the_existing_ids pins the same-file
// contract: a second POST while the first job is live answers 202 with the
// FIRST job's ids and dispatches nothing new.
func TestHandleSyncAudio_dedupe_answers_the_existing_ids(t *testing.T) {
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
		offsets: map[string]int64{},
	}
	// A runner that parks until released keeps the first job live.
	release := make(chan struct{})
	runner := &blockingRunner{release: release}
	hs := newSyncHarnessWith(t, store, fakeProc{}, runner)
	defer close(release)

	post := func() SyncAccepted {
		req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
			MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
		}))
		rec := httptest.NewRecorder()
		hs.h.HandleSyncAudio(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		var resp SyncAccepted
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first := post()
	second := post()
	if second.JobID != first.JobID || second.ActivityID != first.ActivityID {
		t.Errorf("second 202 = %+v, want the first job's ids %+v", second, first)
	}
}

// TestHandleSyncAudio_capacity_answers_a_typed_429 pins the admission lease
// at the wire: with the lease full, the next DISTINCT file answers 429 with
// the rate_limited code — never a 500 — and a same-file POST still 202s.
func TestHandleSyncAudio_capacity_answers_a_typed_429(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := make([]subflux.SubtitleEntry, 0, syncjobs.MaxJobs+1)
	for i := 0; i <= syncjobs.MaxJobs; i++ {
		name := filepath.Join(dir, "m"+string(rune('a'+i))+".en.srt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, subflux.SubtitleEntry{
			MediaID: "tmdb-" + string(rune('a'+i)), Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: name,
			VideoPath: filepath.Join(dir, "m.mkv"),
		})
	}
	store := &syncFakeStore{rows: rows, offsets: map[string]int64{}}
	release := make(chan struct{})
	hs := newSyncHarnessWith(t, store, fakeProc{}, &blockingRunner{release: release})
	defer close(release)

	post := func(mediaID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
			MediaType: subflux.MediaTypeMovie, MediaID: mediaID, Language: "en",
		}))
		rec := httptest.NewRecorder()
		hs.h.HandleSyncAudio(rec, req)
		return rec
	}
	for i := range syncjobs.MaxJobs {
		if rec := post("tmdb-" + string(rune('a'+i))); rec.Code != http.StatusAccepted {
			t.Fatalf("fill %d status = %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	over := post("tmdb-" + string(rune('a'+syncjobs.MaxJobs)))
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("overflow status = %d, want 429: %s", over.Code, over.Body.String())
	}
	if !strings.Contains(over.Body.String(), string(subflux.CodeRateLimited)) {
		t.Errorf("overflow body = %q, want the %s code", over.Body.String(), subflux.CodeRateLimited)
	}
	if rec := post("tmdb-a"); rec.Code != http.StatusAccepted {
		t.Errorf("same-file at cap status = %d, want 202 (dedupe answers the existing ids)", rec.Code)
	}
}

// TestHandleSyncAudio_unreadable_file_is_a_failed_job pins the deferred
// read: validation resolves paths only, so a vanished file 202s and the JOB
// fails — the activity entry marks failed and the record carries the error.
func TestHandleSyncAudio_unreadable_file_is_a_failed_job(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subPath := filepath.Join(dir, "gone.en.srt") // never created
	store := &syncFakeStore{
		rows: []subflux.SubtitleEntry{{
			MediaID: "tmdb-1", Language: "en", Variant: "standard",
			Source: string(subflux.SourceExternal), Path: subPath,
			VideoPath: filepath.Join(dir, "movie.mkv"),
		}},
		offsets: map[string]int64{},
	}
	hs := newSyncHarness(t, store)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en",
	}))
	rec := httptest.NewRecorder()
	hs.h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (the read is the job's)", rec.Code)
	}
	var resp SyncAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	job := awaitJob(t, hs.d, resp.JobID)
	if job.Outcome != subflux.JobCrash || job.Error == "" {
		t.Errorf("job = %+v, want done(crash) with the read error", job)
	}
	entry, ok := hs.log.Get(resp.ActivityID)
	if !ok || !entry.Done || !entry.Failed {
		t.Errorf("activity entry = %+v, want terminal failed", entry)
	}
}

// blockingRunner parks admitted jobs until released (hook honored).
type blockingRunner struct {
	release chan struct{}
}

func (r *blockingRunner) RunAudio(ctx context.Context, _ []byte, _, _ string, hook syncworker.AdmissionHook) syncworker.RunOutcome {
	if hook != nil && !hook() {
		return syncworker.RunOutcome{Outcome: syncworker.OutcomeCancelled, Err: syncworker.ErrAdmissionRefused}
	}
	select {
	case <-r.release:
		return syncworker.RunOutcome{Outcome: syncworker.OutcomeResult, Result: subsync.SyncResult{Method: subsync.MethodNone}}
	case <-ctx.Done():
		return syncworker.RunOutcome{Outcome: syncworker.OutcomeCancelled, Err: context.Cause(ctx)}
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
	h := newSyncHarness(t, store).h

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

	// Dry-run is allowed (inspect-only) — accepted as a job like any other.
	req = httptest.NewRequest(http.MethodPost, "/api/sync/audio", refBody(t, SyncAudioRequest{
		MediaType: subflux.MediaTypeMovie, MediaID: "tmdb-1", Language: "en", DryRun: true,
	}))
	rec = httptest.NewRecorder()
	h.HandleSyncAudio(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("dry-run status = %d, want 202: %s", rec.Code, rec.Body.String())
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
	h := newSyncHarness(t, store).h

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
