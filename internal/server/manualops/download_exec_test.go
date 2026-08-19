package manualops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/testsupport"
)

// fakeArr is a minimal ManualArrClient for exercising the media-ID and title
// lookups. Get* return the configured value (or error); Refresh* are unused by
// the functions under test and are no-ops.
type fakeArr struct {
	movie  arrapi.Movie
	series arrapi.Series
	getErr error
}

var (
	_ ManualSonarrClient = (*fakeArr)(nil)
	_ ManualRadarrClient = (*fakeArr)(nil)
)

func (f *fakeArr) GetMovieByID(context.Context, int) (arrapi.Movie, error) { return f.movie, f.getErr }
func (f *fakeArr) GetSeriesByID(context.Context, int) (arrapi.Series, error) {
	return f.series, f.getErr
}
func (f *fakeArr) RescanMovie(context.Context, int) error  { return nil }
func (f *fakeArr) RescanSeries(context.Context, int) error { return nil }

func TestResolveMediaIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		radarr      ManualRadarrClient
		sonarr      ManualSonarrClient
		mediaType   subflux.MediaType
		arrID       int
		season      int
		episode     int
		wantCover   string
		wantHistory string
	}{
		{
			name:        "movie resolved via radarr uses tmdb id for both",
			radarr:      &fakeArr{movie: arrapi.Movie{TmdbID: 123}},
			mediaType:   subflux.MediaTypeMovie,
			arrID:       5,
			wantCover:   "tmdb-123",
			wantHistory: "tmdb-123",
		},
		{
			name:        "movie without radarr falls back to radarr-<id> history",
			mediaType:   subflux.MediaTypeMovie,
			arrID:       5,
			wantCover:   "",
			wantHistory: "radarr-5",
		},
		{
			name:        "movie radarr error falls back to radarr-<id> history",
			radarr:      &fakeArr{getErr: errors.New("radarr unreachable")},
			mediaType:   subflux.MediaTypeMovie,
			arrID:       5,
			wantCover:   "",
			wantHistory: "radarr-5",
		},
		{
			name:        "episode resolved via sonarr uses tvdb id for both",
			sonarr:      &fakeArr{series: arrapi.Series{TvdbID: 999}},
			mediaType:   subflux.MediaTypeEpisode,
			arrID:       7,
			season:      1,
			episode:     2,
			wantCover:   "tvdb-999-s01e02",
			wantHistory: "tvdb-999-s01e02",
		},
		{
			name:        "episode without sonarr falls back to zero-padded sonarr-<id> history",
			mediaType:   subflux.MediaTypeEpisode,
			arrID:       7,
			season:      1,
			episode:     2,
			wantCover:   "",
			wantHistory: "sonarr-7-s01e02",
		},
		{
			name:        "episode without arr id uses build-media-id fallback",
			mediaType:   subflux.MediaTypeEpisode,
			arrID:       0,
			wantCover:   "",
			wantHistory: "s00e00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &LiveState{Radarr: tt.radarr, Sonarr: tt.sonarr}
			cover, history := ResolveMediaIDs(t.Context(), ls, tt.mediaType, tt.arrID, tt.season, tt.episode)
			if cover != tt.wantCover {
				t.Errorf("coverageID = %q, want %q", cover, tt.wantCover)
			}
			if history != tt.wantHistory {
				t.Errorf("historyID = %q, want %q", history, tt.wantHistory)
			}
		})
	}
}

// TestLookupEpisodeMediaID_pairOrder pins the season/episode order through the
// production path that writes the coverage row's primary key. A transposition
// here does not fail: it produces a well-formed key for a different episode.
func TestLookupEpisodeMediaID_pairOrder(t *testing.T) {
	t.Parallel()
	ls := &LiveState{Sonarr: &fakeArr{series: arrapi.Series{TvdbID: 121361}}}
	const want = "tvdb-121361-s01e09"
	got := LookupEpisodeMediaID(t.Context(), ls, 7, 1, 9)
	if got != want {
		t.Errorf("LookupEpisodeMediaID(series 7, season 1, episode 9) = %q, want %q (a transposed pair yields tvdb-121361-s09e01, another episode's key)",
			got, want)
	}
}

func TestLookupMediaTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		radarr    ManualRadarrClient
		sonarr    ManualSonarrClient
		mediaType subflux.MediaType
		arrID     int
		want      string
	}{
		{
			name:      "movie title from radarr",
			radarr:    &fakeArr{movie: arrapi.Movie{Title: "The Matrix"}},
			mediaType: subflux.MediaTypeMovie,
			arrID:     5,
			want:      "The Matrix",
		},
		{
			name:      "episode title from sonarr",
			sonarr:    &fakeArr{series: arrapi.Series{Title: "Breaking Bad"}},
			mediaType: subflux.MediaTypeEpisode,
			arrID:     7,
			want:      "Breaking Bad",
		},
		{
			name:      "zero arr id never consults the client",
			radarr:    &fakeArr{movie: arrapi.Movie{Title: "Should Not Be Read"}},
			mediaType: subflux.MediaTypeMovie,
			arrID:     0,
			want:      "",
		},
		{
			name:      "movie with nil radarr returns empty",
			mediaType: subflux.MediaTypeMovie,
			arrID:     5,
			want:      "",
		},
		{
			name:      "movie radarr error returns empty",
			radarr:    &fakeArr{getErr: errors.New("radarr unreachable")},
			mediaType: subflux.MediaTypeMovie,
			arrID:     5,
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &LiveState{Radarr: tt.radarr, Sonarr: tt.sonarr}
			if got := LookupMediaTitle(t.Context(), ls, tt.mediaType, tt.arrID); got != tt.want {
				t.Errorf("LookupMediaTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

// recordingActivity captures Progress detail updates per activity ID.
type recordingActivity struct {
	details map[string]string
}

func (r *recordingActivity) Start(string, string, activity.ActivitySource) string { return "act-9" }
func (r *recordingActivity) End(string)                                           {}
func (r *recordingActivity) Fail(string)                                          {}
func (r *recordingActivity) Progress(id string, _, _ int, detail string) {
	r.details[id] = detail
}

// srtProvider serves a fixed valid SRT payload.
type srtProvider struct{}

func (srtProvider) Name() subflux.ProviderID { return "os" }
func (srtProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (srtProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	return []byte("1\n00:00:01,000 --> 00:00:02,000\nHello there.\n\n"), nil
}

// A successful download must write the saved subtitle path into the
// activity entry's detail (via Progress) — the completion datum the remote
// CLI's poll loop reports as "Saved: <path>".
func TestRunDownload_records_saved_path_in_activity_detail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("write fake video: %v", err)
	}

	cfg := fakeManualCfg{}
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(&testsupport.NopStore{}), search.WithConfig(cfg),
		search.WithMetrics(obs.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))

	rec := &recordingActivity{details: map[string]string{}}
	deps := &SearchDeps{
		DB:       &testsupport.NopStore{},
		Activity: rec,
		Alerts:   activity.NewAlertLog(10),
		Events:   fakeEvents{},
	}
	ls := &LiveState{Cfg: cfg, Engine: engine}

	req := &DownloadRequest{
		Provider: "os", SubtitleID: "sub-1", Language: "en",
		MediaType: subflux.MediaTypeMovie, ArrID: 42,
	}
	req.SetVideoPath(videoPath)

	if ok := RunDownload(t.Context(), deps, ls, &testsupport.NopStore{}, srtProvider{}, req, "act-9"); !ok {
		t.Fatal("RunDownload() = false, want success")
	}

	detail := rec.details["act-9"]
	if detail == "" {
		t.Fatal("activity detail not updated with the saved path")
	}
	if !strings.HasPrefix(detail, dir) {
		t.Errorf("activity detail = %q, want a path under %q", detail, dir)
	}
	if _, err := os.Stat(detail); err != nil {
		t.Errorf("activity detail %q does not point at the written subtitle: %v", detail, err)
	}
}

// emptyProvider answers 200 with no bytes — the shape a provider takes when it
// has the record but not the file.
type emptyProvider struct{}

func (emptyProvider) Name() subflux.ProviderID { return "os" }
func (emptyProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (emptyProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	return []byte{}, nil
}

// recordingWarns captures the operator-facing alert text.
type recordingWarns struct{ msgs []string }

func (r *recordingWarns) RecordWarn(_, msg string) { r.msgs = append(r.msgs, msg) }

// A zero-byte download must fail the manual path before anything lands: no
// numbered .srt on disk, no history row, no manual lock. This path has no
// length guard of its own — subtitlefile.Validate is the whole gate — and it
// runs before the ordinal reservation, so a regression there both writes an
// unreadable file and locks the quad against automation that would have fixed
// it. The alert must name the empty file rather than blaming an archive format.
func TestRunDownload_rejects_zero_byte_payload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("write fake video: %v", err)
	}

	cfg := fakeManualCfg{}
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(&testsupport.NopStore{}), search.WithConfig(cfg),
		search.WithMetrics(obs.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))

	warns := &recordingWarns{}
	store := &ordinalStore{}
	deps := &SearchDeps{
		DB:       &testsupport.NopStore{},
		Activity: &recordingActivity{details: map[string]string{}},
		Alerts:   warns,
		Events:   fakeEvents{},
	}
	ls := &LiveState{Cfg: cfg, Engine: engine}

	req := &DownloadRequest{
		Provider: "os", SubtitleID: "sub-1", Language: "en",
		MediaType: subflux.MediaTypeMovie, ArrID: 42,
	}
	req.SetVideoPath(videoPath)

	if RunDownload(t.Context(), deps, ls, store, emptyProvider{}, req, "act-1") {
		t.Error("RunDownload() = true: a zero-byte download reported success")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == subtitlefile.ExtSRT {
			t.Errorf("a zero-byte subtitle was written to disk as %q", e.Name())
		}
	}
	if rows := store.snapshot(); len(rows) != 0 {
		t.Errorf("history recorded %d row(s) for a download that produced no subtitle", len(rows))
	}
	if len(warns.msgs) != 1 {
		t.Fatalf("alerts = %v, want exactly one", warns.msgs)
	}
	if !strings.Contains(warns.msgs[0], "empty") {
		t.Errorf("alert = %q, want it to name the empty file rather than the archive format", warns.msgs[0])
	}
}

// --- numbered-path ordinal allocation (top picks + concurrency) ---

// ordinalStore is an in-memory DownloadStore whose NextManualNumber mirrors
// the boltstore contract: the next ordinal is one greater than the highest
// ordinal encoded in ANY saved row's path for the quad, regardless of the
// Manual flag (a top pick records as an auto row on a numbered path).
// onNext, when set, observes each allocation with the number of rows
// already committed — the concurrency test uses it as a handshake proving
// the per-quad reservation spans allocation through history insertion.
type ordinalStore struct {
	testsupport.NopStore

	onNext func(committedRows int)
	mu     sync.Mutex
	rows   []subflux.DownloadRecord
}

func (s *ordinalStore) NextManualNumber(_ context.Context, _ subflux.ManualLockKey) int {
	s.mu.Lock()
	maxOrdinal := 0
	for i := range s.rows {
		if n := subtitlefile.ManualOrdinal(s.rows[i].Path); n > maxOrdinal {
			maxOrdinal = n
		}
	}
	committed := len(s.rows)
	s.mu.Unlock()
	if s.onNext != nil {
		s.onNext(committed)
	}
	return maxOrdinal + 1
}

func (s *ordinalStore) SaveDownload(_ context.Context, rec *subflux.DownloadRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, *rec)
	return nil
}

// snapshot returns a copy of the recorded rows in save order.
func (s *ordinalStore) snapshot() []subflux.DownloadRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]subflux.DownloadRecord, len(s.rows))
	copy(out, s.rows)
	return out
}

// seqProvider serves a distinct valid SRT payload per Download call, so
// overwrite bugs are visible as a lost payload.
type seqProvider struct{ calls atomic.Int32 }

func (*seqProvider) Name() subflux.ProviderID { return "os" }
func (*seqProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (p *seqProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	n := p.calls.Add(1)
	return fmt.Appendf(nil, "1\n00:00:01,000 --> 00:00:02,000\nPayload %d.\n\n", n), nil
}

// ordinalHarness builds the RunDownload collaborators for the ordinal
// tests: a real engine over testsupport fakes, no-op sinks, and a fake
// video file in a temp dir.
func ordinalHarness(t *testing.T) (*SearchDeps, *LiveState, string) {
	t.Helper()
	videoPath := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("write fake video: %v", err)
	}
	cfg := fakeManualCfg{}
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(&testsupport.NopStore{}), search.WithConfig(cfg),
		search.WithMetrics(obs.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))
	deps := &SearchDeps{
		DB:       &testsupport.NopStore{},
		Activity: fakeActivity{},
		Alerts:   activity.NewAlertLog(10),
		Events:   fakeEvents{},
	}
	return deps, &LiveState{Cfg: cfg, Engine: engine}, videoPath
}

// downloadReq builds a same-quad manual download request for the harness's
// video (movie, arr id 42, language en).
func downloadReq(videoPath, subtitleID string, topPick bool) *DownloadRequest {
	req := &DownloadRequest{
		Provider: "os", SubtitleID: subtitleID, Language: "en",
		MediaType: subflux.MediaTypeMovie, ArrID: 42, TopPick: topPick,
	}
	req.SetVideoPath(videoPath)
	return req
}

// readFileT reads a saved subtitle file, failing the test on a miss (a
// missing ordinal file means one download overwrote the other).
func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Two sequential top picks must land at .1 then .2: top picks record as
// AUTO rows (manual=false), so ordinal discovery has to count auto-row
// paths or the second download reuses .1 and overwrites the first.
func TestRunDownload_sequentialTopPicks_getDistinctOrdinals(t *testing.T) {
	t.Parallel()
	deps, ls, videoPath := ordinalHarness(t)
	store := &ordinalStore{}
	prov := &seqProvider{}

	for i := 1; i <= 2; i++ {
		req := downloadReq(videoPath, fmt.Sprintf("sub-%d", i), true)
		if ok := RunDownload(t.Context(), deps, ls, store, prov, req, "act"); !ok {
			t.Fatalf("RunDownload(top pick %d) = false, want success", i)
		}
	}

	first := subtitlefile.ManualPath(videoPath, 1, subtitlefile.Tags{Lang: "en"})
	second := subtitlefile.ManualPath(videoPath, 2, subtitlefile.Tags{Lang: "en"})
	if got := readFileT(t, first); !strings.Contains(got, "Payload 1") {
		t.Errorf("%s content = %q, want the FIRST download's payload intact", first, got)
	}
	if got := readFileT(t, second); !strings.Contains(got, "Payload 2") {
		t.Errorf("%s content = %q, want the second download's payload", second, got)
	}

	rows := store.snapshot()
	if len(rows) != 2 || rows[0].Path != first || rows[1].Path != second {
		t.Fatalf("recorded paths = %+v, want [%s %s]", rows, first, second)
	}
	for i := range rows {
		if rows[i].Meta == nil || rows[i].Meta.Manual {
			t.Errorf("row %d recorded manual, want auto (top pick)", i)
		}
	}
}

// A manual (non-top) pick after a top pick continues the shared per-quad
// sequence at .2 rather than restarting at .1 over the top pick's file.
func TestRunDownload_topPickThenManual_continuesSequence(t *testing.T) {
	t.Parallel()
	deps, ls, videoPath := ordinalHarness(t)
	store := &ordinalStore{}
	prov := &seqProvider{}

	if ok := RunDownload(t.Context(), deps, ls, store, prov,
		downloadReq(videoPath, "sub-top", true), "act"); !ok {
		t.Fatal("RunDownload(top pick) = false, want success")
	}
	if ok := RunDownload(t.Context(), deps, ls, store, prov,
		downloadReq(videoPath, "sub-manual", false), "act"); !ok {
		t.Fatal("RunDownload(manual) = false, want success")
	}

	first := subtitlefile.ManualPath(videoPath, 1, subtitlefile.Tags{Lang: "en"})
	second := subtitlefile.ManualPath(videoPath, 2, subtitlefile.Tags{Lang: "en"})
	if got := readFileT(t, first); !strings.Contains(got, "Payload 1") {
		t.Errorf("%s content = %q, want the top pick's payload intact", first, got)
	}
	if got := readFileT(t, second); !strings.Contains(got, "Payload 2") {
		t.Errorf("%s content = %q, want the manual download's payload", second, got)
	}

	rows := store.snapshot()
	if len(rows) != 2 {
		t.Fatalf("recorded rows = %d, want 2", len(rows))
	}
	if rows[0].Meta.Manual || !rows[1].Meta.Manual {
		t.Errorf("Manual flags = [%v %v], want [false true] (top pick auto, then manual)",
			rows[0].Meta.Manual, rows[1].Meta.Manual)
	}
	if rows[1].Path != second {
		t.Errorf("manual row path = %q, want %q (sequence continues past the auto-numbered row)",
			rows[1].Path, second)
	}
}

// Two concurrent downloads for the SAME quad must allocate distinct
// ordinals: the per-quad reservation spans allocation, atomic write, and
// history insertion, so the second allocation is proven to observe the
// first download's committed row (handshake), and both payloads survive.
func TestRunDownload_concurrentSameQuad_allocatesDistinctOrdinals(t *testing.T) {
	t.Parallel()
	deps, ls, videoPath := ordinalHarness(t)

	entered := make(chan int, 2)
	release := make(chan struct{})
	store := &ordinalStore{onNext: func(committed int) {
		entered <- committed
		<-release
	}}
	prov := &seqProvider{}

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := range 2 {
		wg.Go(func() {
			req := downloadReq(videoPath, fmt.Sprintf("sub-%d", i), true)
			<-start
			results[i] = RunDownload(t.Context(), deps, ls, store, prov, req, "act")
		})
	}
	close(start)

	// First allocation: nothing committed yet. While it is parked here the
	// competing download must be blocked at the gate, not allocating.
	if got := <-entered; got != 0 {
		t.Errorf("first allocation observed %d committed rows, want 0", got)
	}
	release <- struct{}{}
	// Second allocation may only begin after the first download's history
	// row is committed (the gate covers write + insert): observing 0 here
	// means both downloads allocated the same ordinal.
	if got := <-entered; got != 1 {
		t.Errorf("second allocation observed %d committed rows, want 1 (previous download committed under the gate)", got)
	}
	release <- struct{}{}
	wg.Wait()

	if !results[0] || !results[1] {
		t.Fatalf("RunDownload results = %v, want both successful", results)
	}
	rows := store.snapshot()
	if len(rows) != 2 {
		t.Fatalf("recorded rows = %d, want 2", len(rows))
	}
	ordinals := map[int]bool{}
	for i := range rows {
		ordinals[subtitlefile.ManualOrdinal(rows[i].Path)] = true
	}
	if !ordinals[1] || !ordinals[2] {
		t.Fatalf("allocated ordinals = %v, want {1, 2} (no reuse)", ordinals)
	}
	c1 := readFileT(t, subtitlefile.ManualPath(videoPath, 1, subtitlefile.Tags{Lang: "en"}))
	c2 := readFileT(t, subtitlefile.ManualPath(videoPath, 2, subtitlefile.Tags{Lang: "en"}))
	if c1 == c2 {
		t.Errorf("both ordinal files hold identical content %q; one download overwrote the other", c1)
	}
}
