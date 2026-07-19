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
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/testsupport"
)

// listEntries drives HandleListFiles and decodes the response.
func listEntries(t *testing.T, h *Handler, query string) []FileEntry {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/files?"+query, nil)
	rec := httptest.NewRecorder()
	h.HandleListFiles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleListFiles() status = %d: %s", rec.Code, rec.Body.String())
	}
	var entries []FileEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return entries
}

// orphanOf returns the single orphan entry from a listing.
func orphanOf(t *testing.T, entries []FileEntry) FileEntry {
	t.Helper()
	var orphans []FileEntry
	for _, e := range entries {
		if e.OrphanHandle != "" {
			orphans = append(orphans, e)
		}
	}
	if len(orphans) != 1 {
		t.Fatalf("orphan entries = %d, want 1 (%+v)", len(orphans), entries)
	}
	return orphans[0]
}

// deleteHandle drives HandleDeleteFile with an orphan handle body.
func deleteHandle(t *testing.T, h *Handler, handle string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(DeleteFileRequest{OrphanHandle: handle})
	req := httptest.NewRequest(http.MethodDelete, "/api/files", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.HandleDeleteFile(rec, req)
	return rec
}

// newOrphanFixture creates a media dir with one stored subtitle row and one
// orphan subtitle file beside it, returning the handler and the paths.
func newOrphanFixture(t *testing.T) (h *Handler, storedPath, orphanPath string) {
	t.Helper()
	dir := t.TempDir()
	storedPath = filepath.Join(dir, "movie.en.srt")
	orphanPath = filepath.Join(dir, "movie.stray.de.srt")
	for _, p := range []string{storedPath, orphanPath} {
		if err := os.WriteFile(p, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakeFileStore{rows: []api.SubtitleEntry{{
		MediaID: "tmdb-123", Language: "en", Variant: "standard",
		Source: "external", Path: storedPath,
	}}}
	return newFileHandler(store, &testsupport.NopConfig{}), storedPath, orphanPath
}

// TestOrphanLifecycle_mintDeleteConsume covers the design's handle
// lifecycle: listing surfaces the orphan with a handle, deleting through the
// handle removes the file and consumes the handle, and a second delete with
// the same handle answers 410 Gone.
func TestOrphanLifecycle_mintDeleteConsume(t *testing.T) {
	t.Parallel()
	h, storedPath, orphanPath := newOrphanFixture(t)

	entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123")
	orphan := orphanOf(t, entries)
	if orphan.Name != filepath.Base(orphanPath) {
		t.Errorf("orphan name = %q, want %q", orphan.Name, filepath.Base(orphanPath))
	}
	if orphan.Source != string(api.SourceExternal) {
		t.Errorf("orphan source = %q, want external", orphan.Source)
	}
	if len(orphan.OrphanHandle) != 32 {
		t.Errorf("handle length = %d, want 32 hex chars (128-bit)", len(orphan.OrphanHandle))
	}

	rec := deleteHandle(t, h, orphan.OrphanHandle)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("orphan delete status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Error("orphan file still exists after handle delete")
	}
	if _, err := os.Stat(storedPath); err != nil {
		t.Errorf("stored subtitle was deleted by the orphan flow: %v", err)
	}

	// Handle was consumed: from then on it is unknown, so the second
	// delete answers 404 (not 410 — that is reserved for expiry).
	rec = deleteHandle(t, h, orphan.OrphanHandle)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete with consumed handle = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "orphan_handle_unknown") {
		t.Errorf("body = %q, want orphan_handle_unknown", rec.Body.String())
	}
}

// TestOrphanLifecycle_unknownAndExpired pins the redemption status split:
// a never-known handle answers 404 orphan_handle_unknown, an expired handle
// (TTL passed) 410 orphan_handle_expired.
func TestOrphanLifecycle_unknownAndExpired(t *testing.T) {
	t.Parallel()
	h, _, _ := newOrphanFixture(t)

	rec := deleteHandle(t, h, strings.Repeat("ab", 16))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown handle delete = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "orphan_handle_unknown") {
		t.Errorf("body = %q, want orphan_handle_unknown", rec.Body.String())
	}

	// Expiry: mint via listing, then move the table's clock past the TTL.
	entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123")
	orphan := orphanOf(t, entries)
	h.orphans.now = func() time.Time { return time.Now().Add(orphanTTL + time.Minute) }
	rec = deleteHandle(t, h, orphan.OrphanHandle)
	if rec.Code != http.StatusGone {
		t.Fatalf("expired handle delete = %d, want 410", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "orphan_handle_expired") {
		t.Errorf("body = %q, want orphan_handle_expired", rec.Body.String())
	}
}

// TestOrphanLifecycle_metadataMismatchRefused: a file swapped between
// listing and deletion (size change) is refused with 409 and survives.
func TestOrphanLifecycle_metadataMismatchRefused(t *testing.T) {
	t.Parallel()
	h, _, orphanPath := newOrphanFixture(t)

	entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123")
	orphan := orphanOf(t, entries)

	// Swap the file content (size changes).
	if err := os.WriteFile(orphanPath, []byte("swapped content of a different length"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := deleteHandle(t, h, orphan.OrphanHandle)
	if rec.Code != http.StatusConflict {
		t.Fatalf("swapped-file delete = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "orphan_changed") {
		t.Errorf("body = %q, want orphan_changed", rec.Body.String())
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Errorf("swapped file was deleted despite metadata mismatch: %v", err)
	}
}

// TestOrphanListing_skipsNonSubtitleAndKnown: the walk only surfaces on-disk
// subtitle extensions that have no store row.
func TestOrphanListing_skipsNonSubtitleAndKnown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storedPath := filepath.Join(dir, "movie.en.srt")
	files := map[string]string{
		storedPath:                         "stored subtitle",
		filepath.Join(dir, "movie.mkv"):    "video file (not a subtitle ext)",
		filepath.Join(dir, "movie.nfo"):    "metadata (not a subtitle ext)",
		filepath.Join(dir, "movie.a.vtt"):  "vtt is archive-input only, not on-disk",
		filepath.Join(dir, "stray.de.ass"): "orphan",
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakeFileStore{rows: []api.SubtitleEntry{{
		MediaID: "tmdb-123", Language: "en", Variant: "standard",
		Source: "external", Path: storedPath,
	}}}
	h := newFileHandler(store, &testsupport.NopConfig{})

	entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123")
	orphan := orphanOf(t, entries)
	if orphan.Name != "stray.de.ass" {
		t.Errorf("orphan = %q, want stray.de.ass (the only on-disk-capability orphan)", orphan.Name)
	}
}

// TestOrphanTable_capEviction pins the bounded-table guarantee: the table
// never exceeds its cap; at capacity the oldest-expiring entries fall out.
func TestOrphanTable_capEviction(t *testing.T) {
	t.Parallel()
	tbl := newOrphanTable()
	base := time.Now()
	tbl.now = func() time.Time { return base }
	for i := range orphanTableCap + 10 {
		base = base.Add(time.Millisecond) // distinct expiry order
		if _, err := tbl.mint(filepath.Join("/x", "f", "s.srt"), int64(i), base); err != nil {
			t.Fatal(err)
		}
	}
	tbl.mu.Lock()
	n := len(tbl.entries)
	tbl.mu.Unlock()
	if n > orphanTableCap {
		t.Fatalf("table size %d exceeds cap %d", n, orphanTableCap)
	}
}

// --- Bound arr fallback (all-orphan edge) ---

// fakeSonarrArr implements FileSonarrClient with canned responses.
type fakeSonarrArr struct {
	seriesErr   error
	episodesErr error
	episodes    []arrapi.Episode
	series      arrapi.Series
}

func (f *fakeSonarrArr) GetSeriesByID(context.Context, int) (arrapi.Series, error) {
	return f.series, f.seriesErr
}

func (f *fakeSonarrArr) GetEpisodes(context.Context, int) ([]arrapi.Episode, error) {
	return f.episodes, f.episodesErr
}

// fakeRadarrArr implements FileRadarrClient with canned responses.
type fakeRadarrArr struct {
	err   error
	movie arrapi.Movie
}

func (f *fakeRadarrArr) GetMovieByID(context.Context, int) (arrapi.Movie, error) {
	return f.movie, f.err
}

// newFileHandlerArr builds a Handler whose LiveState carries the given arr
// fakes (nil-safe), over an empty store: the all-orphan edge.
func newFileHandlerArr(store FileStore, sonarr FileSonarrClient, radarr FileRadarrClient) *Handler {
	cfg := &testsupport.NopConfig{}
	return NewHandler(Deps{
		Store: store,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: cfg} },
		},
		StateFunc: func() *LiveState { return &LiveState{Cfg: cfg, Sonarr: sonarr, Radarr: radarr} },
		Events:    events.New(0),
	})
}

// listStatus drives HandleListFiles and returns the raw recorder (for
// non-200 assertions).
func listStatus(t *testing.T, h *Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/files?"+query, nil)
	rec := httptest.NewRecorder()
	h.HandleListFiles(rec, req)
	return rec
}

// writeStray creates an orphan subtitle file inside dir and returns its path.
func writeStray(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestOrphanFallback_mismatchedArrIDRejected: an arr_id whose arr item does
// not correspond to the requested media_id answers 400 and surfaces nothing
// — a caller cannot mint handles for media B's files by requesting media A
// with B's arr ID.
func TestOrphanFallback_mismatchedArrIDRejected(t *testing.T) {
	t.Parallel()

	t.Run("movie", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		victim := writeStray(t, dir, "other.movie.en.srt")
		radarr := &fakeRadarrArr{movie: arrapi.Movie{
			ID: 42, TmdbID: 999, // NOT tmdb-123
			MovieFile: &arrapi.MovieFile{Path: filepath.Join(dir, "other.movie.mkv")},
		}}
		h := newFileHandlerArr(&fakeFileStore{}, nil, radarr)

		rec := listStatus(t, h, "media_type=movie&media_id=tmdb-123&arr_id=42")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mismatched movie arr_id status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "arr_id does not correspond to media_id") {
			t.Errorf("body = %q, want binding-mismatch message", rec.Body.String())
		}
		if _, err := os.Stat(victim); err != nil {
			t.Errorf("victim file disturbed: %v", err)
		}
	})

	t.Run("episode", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeStray(t, dir, "other.show.en.srt")
		sonarr := &fakeSonarrArr{
			series: arrapi.Series{ID: 42, TvdbID: 999}, // NOT tvdb-777
			episodes: []arrapi.Episode{{
				SeasonNumber: 1, EpisodeNumber: 5,
				EpisodeFile: &arrapi.EpisodeFile{Path: filepath.Join(dir, "other.show.mkv")},
			}},
		}
		h := newFileHandlerArr(&fakeFileStore{}, sonarr, nil)

		rec := listStatus(t, h, "media_type=episode&media_id=tvdb-777-&arr_id=42")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mismatched series arr_id status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestOrphanFallback_episodeConfinedToEpisodeDir: an episode-level media_id
// walks ONLY that episode file's directory; a sibling episode's stray file
// is NOT surfaced (and so can never be deleted through this listing).
func TestOrphanFallback_episodeConfinedToEpisodeDir(t *testing.T) {
	t.Parallel()
	dirA := t.TempDir() // S01E05's directory
	dirB := t.TempDir() // S01E06's directory
	strayA := writeStray(t, dirA, "show.s01e05.stray.de.srt")
	writeStray(t, dirB, "show.s01e06.stray.de.srt")

	sonarr := &fakeSonarrArr{
		series: arrapi.Series{ID: 42, TvdbID: 777},
		episodes: []arrapi.Episode{
			{
				SeasonNumber: 1, EpisodeNumber: 5,
				EpisodeFile: &arrapi.EpisodeFile{Path: filepath.Join(dirA, "show.s01e05.mkv")},
			},
			{
				SeasonNumber: 1, EpisodeNumber: 6,
				EpisodeFile: &arrapi.EpisodeFile{Path: filepath.Join(dirB, "show.s01e06.mkv")},
			},
		},
	}
	h := newFileHandlerArr(&fakeFileStore{}, sonarr, nil)

	entries := listEntries(t, h, "media_type=episode&media_id=tvdb-777-s01e05&arr_id=42")
	orphan := orphanOf(t, entries)
	if orphan.Name != filepath.Base(strayA) {
		t.Errorf("orphan = %q, want %q (the addressed episode's stray only)",
			orphan.Name, filepath.Base(strayA))
	}
	for _, e := range entries {
		if e.Name == "show.s01e06.stray.de.srt" {
			t.Error("sibling episode's stray file was surfaced by an episode-level listing")
		}
	}
}

// TestOrphanFallback_seriesPrefixCoversSeries: a series-prefix media_id
// ("tvdb-777-", the series-level file manager) legitimately scopes the
// listing to the whole series, so — with the series binding verified —
// every episode directory is walked.
func TestOrphanFallback_seriesPrefixCoversSeries(t *testing.T) {
	t.Parallel()
	dirA, dirB := t.TempDir(), t.TempDir()
	writeStray(t, dirA, "show.s01e05.stray.de.srt")
	writeStray(t, dirB, "show.s01e06.stray.de.srt")

	sonarr := &fakeSonarrArr{
		series: arrapi.Series{ID: 42, TvdbID: 777},
		episodes: []arrapi.Episode{
			{
				SeasonNumber: 1, EpisodeNumber: 5,
				EpisodeFile: &arrapi.EpisodeFile{Path: filepath.Join(dirA, "show.s01e05.mkv")},
			},
			{
				SeasonNumber: 1, EpisodeNumber: 6,
				EpisodeFile: &arrapi.EpisodeFile{Path: filepath.Join(dirB, "show.s01e06.mkv")},
			},
		},
	}
	h := newFileHandlerArr(&fakeFileStore{}, sonarr, nil)

	entries := listEntries(t, h, "media_type=episode&media_id=tvdb-777-&arr_id=42")
	if len(entries) != 2 {
		t.Fatalf("series-prefix listing surfaced %d orphans, want 2 (%+v)", len(entries), entries)
	}
}

// TestOrphanFallback_verifiedMovieBinding: the positive movie arm — a
// matching TMDB identity surfaces the movie directory's stray file.
func TestOrphanFallback_verifiedMovieBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stray := writeStray(t, dir, "movie.stray.de.srt")
	radarr := &fakeRadarrArr{movie: arrapi.Movie{
		ID: 42, TmdbID: 123,
		MovieFile: &arrapi.MovieFile{Path: filepath.Join(dir, "movie.mkv")},
	}}
	h := newFileHandlerArr(&fakeFileStore{}, nil, radarr)

	entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123&arr_id=42")
	orphan := orphanOf(t, entries)
	if orphan.Name != filepath.Base(stray) {
		t.Errorf("orphan = %q, want %q", orphan.Name, filepath.Base(stray))
	}
}

// TestOrphanFallback_bindingEdges covers the remaining fallback edges: an
// unknown arr item (404), an unbindable media_id shape (400), a transient
// arr failure (degrade to the store-rows-only listing), and an invalid
// arr_id parameter (400).
func TestOrphanFallback_bindingEdges(t *testing.T) {
	t.Parallel()

	t.Run("unknown arr item returns 404", func(t *testing.T) {
		t.Parallel()
		radarr := &fakeRadarrArr{err: &arrapi.StatusError{Code: http.StatusNotFound, Path: "/api/v3/movie/42"}}
		h := newFileHandlerArr(&fakeFileStore{}, nil, radarr)
		rec := listStatus(t, h, "media_type=movie&media_id=tmdb-123&arr_id=42")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown arr item status = %d, want 404: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "media_not_found") {
			t.Errorf("body = %q, want media_not_found", rec.Body.String())
		}
	})

	t.Run("unbindable episode media_id returns 400", func(t *testing.T) {
		t.Parallel()
		sonarr := &fakeSonarrArr{series: arrapi.Series{ID: 42, TvdbID: 777}}
		h := newFileHandlerArr(&fakeFileStore{}, sonarr, nil)
		// A season prefix is not a bindable series or episode identity.
		rec := listStatus(t, h, "media_type=episode&media_id=tvdb-777-s01&arr_id=42")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unbindable media_id status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("transient arr failure degrades to store rows", func(t *testing.T) {
		t.Parallel()
		radarr := &fakeRadarrArr{err: errors.New("connection refused")}
		h := newFileHandlerArr(&fakeFileStore{}, nil, radarr)
		entries := listEntries(t, h, "media_type=movie&media_id=tmdb-123&arr_id=42")
		if len(entries) != 0 {
			t.Fatalf("transient-failure listing = %+v, want empty (no fallback walk)", entries)
		}
	})

	t.Run("invalid arr_id parameter returns 400", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{"abc", "-1", "0"} {
			h := newFileHandlerArr(&fakeFileStore{}, nil, nil)
			rec := listStatus(t, h, "media_type=movie&media_id=tmdb-123&arr_id="+bad)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("arr_id=%q status = %d, want 400", bad, rec.Code)
			}
		}
	})
}
