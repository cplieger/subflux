package synchandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
)

// The season enumeration parity suite (D2): the server-side selection must
// equal the CLIENT POOL's selection contract on the same fixture data — the
// gate for deleting detail-season-sync.ts. The expected selections below
// are HAND-COMPUTED from the pool's algorithm (detail.ts
// collectSeasonSyncEps over the deduplicated coverage detail read), never
// derived by re-running the implementation.

// seasonFakeStore serves fixture rows with prefix filtering (the real
// store's contract). Paths in vanish disappear after the FIRST read: present
// for enumeration, gone when per-item resolution re-reads — the vanished-row
// window.
type seasonFakeStore struct {
	vanish map[string]bool
	rows   []subflux.SubtitleEntry
	reads  int
}

func (s *seasonFakeStore) SubtitleFiles(_ context.Context, _ subflux.MediaType, prefix string) ([]subflux.SubtitleEntry, error) {
	s.reads++
	var out []subflux.SubtitleEntry
	for _, r := range s.rows {
		if !strings.HasPrefix(r.MediaID, prefix) {
			continue
		}
		if s.reads > 1 && s.vanish[r.Path] {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *seasonFakeStore) SyncOffset(context.Context, string) (int64, error)  { return 0, nil }
func (s *seasonFakeStore) SetSyncOffset(context.Context, string, int64) error { return nil }

// seasonFakeSonarr answers the cached-wrapper surface from fixtures.
type seasonFakeSonarr struct {
	seriesErr   error
	episodesErr error
	episodes    map[int][]arrapi.Episode
	series      []arrapi.Series
}

func (s *seasonFakeSonarr) Series(context.Context) ([]arrapi.Series, error) {
	return s.series, s.seriesErr
}

func (s *seasonFakeSonarr) Episodes(_ context.Context, seriesID int) ([]arrapi.Episode, error) {
	return s.episodes[seriesID], s.episodesErr
}

// seasonFakeCfg answers the resolved configured target pairs — the config
// snapshot the enumeration captures at acceptance.
type seasonFakeCfg struct{ targets []subflux.SubtitleTarget }

func (c *seasonFakeCfg) ResolveTargetsWithFallback(string, []string) []subflux.SubtitleTarget {
	return c.targets
}

// row builds one external fixture row (video path optional).
func row(mediaID, lang, variant, path, videoPath string, ordinal int) subflux.SubtitleEntry {
	return subflux.SubtitleEntry{
		MediaID: mediaID, Language: lang, Variant: variant,
		Source: string(subflux.SourceExternal), Path: path, VideoPath: videoPath,
		Ordinal: ordinal,
	}
}

// seasonFixture is the one fixture every parity clause reads: a two-target
// profile over a season exercising every selection rule at once. Subtitle
// files exist on disk (the executor reads them at RUN time), video paths
// are fictional (only the scripted runner would touch them).
func seasonFixture(t *testing.T) (*seasonFakeStore, *seasonFakeSonarr, *seasonFakeCfg, func(string) string) {
	t.Helper()
	dir := t.TempDir()
	sub := func(name string) string { return filepath.Join(dir, name) }
	for _, name := range []string{
		"s01e01.en.srt", "s01e01.fr.forced.srt", "s01e01.de.srt", "s01e01.en.forced.srt",
		"s01e02.en.1.srt", "s01e02.en.srt", "s01e02.fr.forced.ass",
		"s01e03.en.srt", "s01e04.en.srt", "s01e05.en.srt", "s02e01.en.srt",
	} {
		if err := os.WriteFile(sub(name), []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := &seasonFakeStore{
		rows: []subflux.SubtitleEntry{
			// e01: one en row (selected), one embedded (excluded), one fr
			// forced (selected), one non-target lang, one wrong-variant en.
			row("tvdb-81189-s01e01", "en", "standard", sub("s01e01.en.srt"), "/tv/bb/e01.mkv", 0),
			{MediaID: "tvdb-81189-s01e01", Language: "en", Variant: "standard", Source: string(subflux.SourceEmbedded), Codec: "subrip"},
			row("tvdb-81189-s01e01", "fr", "forced", sub("s01e01.fr.forced.srt"), "/tv/bb/e01.mkv", 0),
			row("tvdb-81189-s01e01", "de", "standard", sub("s01e01.de.srt"), "/tv/bb/e01.mkv", 0),
			row("tvdb-81189-s01e01", "en", "forced", sub("s01e01.en.forced.srt"), "/tv/bb/e01.mkv", 0),
			// e02: a manual sibling FIRST in store order (the dedup
			// survivor, key order puts ".1.srt" before ".srt"), the auto
			// file second (collapsed away), and an .ass fr row (gated).
			row("tvdb-81189-s01e02", "en", "standard", sub("s01e02.en.1.srt"), "/tv/bb/e02.mkv", 1),
			row("tvdb-81189-s01e02", "en", "standard", sub("s01e02.en.srt"), "/tv/bb/e02.mkv", 0),
			row("tvdb-81189-s01e02", "fr", "forced", sub("s01e02.fr.forced.ass"), "/tv/bb/e02.mkv", 0),
			// e03 has no video file: its row must not be selected.
			row("tvdb-81189-s01e03", "en", "standard", sub("s01e03.en.srt"), "/tv/bb/e03.mkv", 0),
			// e04: no video path anywhere for the media — skip, reported.
			row("tvdb-81189-s01e04", "en", "standard", sub("s01e04.en.srt"), "", 0),
			// e05: vanishes between enumeration and resolution — skip.
			row("tvdb-81189-s01e05", "en", "standard", sub("s01e05.en.srt"), "/tv/bb/e05.mkv", 0),
			// Another season: never selected for season 1.
			row("tvdb-81189-s02e01", "en", "standard", sub("s02e01.en.srt"), "/tv/bb/s02e01.mkv", 0),
		},
		vanish: map[string]bool{sub("s01e05.en.srt"): true},
	}
	sonarr := &seasonFakeSonarr{
		series: []arrapi.Series{
			{ID: 7, TvdbID: 81189, Title: "Breaking Bad", OriginalLanguage: &arrapi.Language{Name: "English"}},
			{ID: 9, TvdbID: 0, Title: "No Canonical Id"},
		},
		episodes: map[int][]arrapi.Episode{7: {
			// Scrambled on purpose: enumeration must order by episode.
			{SeasonNumber: 1, EpisodeNumber: 2, HasFile: true},
			{SeasonNumber: 1, EpisodeNumber: 1, HasFile: true},
			{SeasonNumber: 1, EpisodeNumber: 3, HasFile: false},
			{SeasonNumber: 1, EpisodeNumber: 4, HasFile: true},
			{SeasonNumber: 1, EpisodeNumber: 5, HasFile: true},
			{SeasonNumber: 2, EpisodeNumber: 1, HasFile: true},
		}},
	}
	cfg := &seasonFakeCfg{targets: []subflux.SubtitleTarget{
		{Code: "en"},                    // effective variant: standard
		{Code: "fr", Variant: "forced"}, // explicit variant
	}}
	return store, sonarr, cfg, sub
}

// seasonHandler builds a Handler whose season surface runs over the fixture
// (no dispatcher run loop — enumeration only).
func seasonHandler(store *seasonFakeStore, sonarr *seasonFakeSonarr, cfg *seasonFakeCfg) *Handler {
	return New(Deps{
		Store: store,
		Files: store,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: fakePathValidator{}} },
		},
		SeasonState: func() *SeasonState { return &SeasonState{Cfg: cfg, Sonarr: sonarr} },
	})
}

func TestSeasonEnumeration_matches_the_client_pool_selection_contract(t *testing.T) {
	t.Parallel()
	store, sonarr, cfg, sub := seasonFixture(t)
	h := seasonHandler(store, sonarr, cfg)

	sel, err := h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 7, Season: 1})
	if err != nil {
		t.Fatalf("enumerateSeason() error = %v", err)
	}

	// Hand-computed from the client pool's algorithm: file-bearing episodes
	// ascending; per episode the configured target pairs in order; per pair
	// every EXTERNAL entry of the deduplicated rows, ordinal preserved.
	want := []syncjobs.ExecInput{
		{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeEpisode, MediaID: "tvdb-81189-s01e01",
				Language: "en", Variant: "standard", Source: "external",
			},
			SubtitlePath: sub("s01e01.en.srt"), VideoPath: "/tv/bb/e01.mkv",
		},
		{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeEpisode, MediaID: "tvdb-81189-s01e01",
				Language: "fr", Variant: "forced", Source: "external",
			},
			SubtitlePath: sub("s01e01.fr.forced.srt"), VideoPath: "/tv/bb/e01.mkv",
		},
		{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeEpisode, MediaID: "tvdb-81189-s01e02",
				Language: "en", Variant: "standard", Source: "external", Ordinal: 1,
			},
			SubtitlePath: sub("s01e02.en.1.srt"), VideoPath: "/tv/bb/e02.mkv",
		},
	}
	if !reflect.DeepEqual(sel.items, want) {
		t.Errorf("enumerateSeason() items =\n%+v\nwant the client pool's selection\n%+v", sel.items, want)
	}
	// The ASS row, the video-less row, and the vanished row are SKIPPED and
	// reported — never selected, never a batch failure.
	if sel.skipped != 3 {
		t.Errorf("skipped = %d, want 3 (ASS gate + no video path + vanished row)", sel.skipped)
	}
	if sel.detail != "Breaking Bad S01 · 3 files · 3 skipped" {
		t.Errorf("detail = %q, want the aggregate label", sel.detail)
	}
}

func TestSeasonEnumeration_empty_season_selects_nothing(t *testing.T) {
	t.Parallel()
	store, sonarr, cfg, _ := seasonFixture(t)
	h := seasonHandler(store, sonarr, cfg)

	// Season 3 has no episodes at all; season 2 has a file-bearing episode
	// with a row, proving season scoping is per-request, not global.
	sel, err := h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 7, Season: 3})
	if err != nil {
		t.Fatalf("enumerateSeason(S3) error = %v", err)
	}
	if len(sel.items) != 0 || sel.skipped != 0 {
		t.Errorf("S3 selection = %+v, want nothing", sel)
	}

	sel, err = h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 7, Season: 2})
	if err != nil {
		t.Fatalf("enumerateSeason(S2) error = %v", err)
	}
	if len(sel.items) != 1 || sel.items[0].Ref.MediaID != "tvdb-81189-s02e01" {
		t.Errorf("S2 selection = %+v, want exactly the S2 row", sel.items)
	}
}

func TestSeasonEnumeration_unknown_or_unaddressable_series(t *testing.T) {
	t.Parallel()
	store, sonarr, cfg, _ := seasonFixture(t)
	h := seasonHandler(store, sonarr, cfg)

	if _, err := h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 999, Season: 1}); !errors.Is(err, resolve.ErrMediaNotFound) {
		t.Errorf("unknown series error = %v, want ErrMediaNotFound", err)
	}
	// A series without a positive TVDB id is unaddressable (A2 parity).
	if _, err := h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 9, Season: 1}); !errors.Is(err, resolve.ErrMediaNotFound) {
		t.Errorf("zero-tvdb series error = %v, want ErrMediaNotFound", err)
	}
}

// --- HTTP surface ---

// (blockingRunner comes from handler_http_test.go: parks admitted jobs
// until released, honoring the admission hook.)

// seasonHTTPHarness wires the full async stack (handler + dispatcher + run
// loop) over the season fixture.
type seasonHTTPHarness struct {
	h   *Handler
	d   *syncjobs.Dispatcher
	log *activity.Log
}

func newSeasonHTTPHarness(t *testing.T, runner AudioJobRunner) *seasonHTTPHarness {
	t.Helper()
	store, sonarr, cfg, _ := seasonFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	log := activity.New(50)
	exec := &AudioExecutor{Store: store, Proc: fakeProc{}, Runner: runner}
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
	h := New(Deps{
		Store:        store,
		Files:        store,
		SubtitleProc: fakeProc{},
		Jobs:         d,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: fakePathValidator{}} },
		},
		SeasonState: func() *SeasonState { return &SeasonState{Cfg: cfg, Sonarr: sonarr} },
	})
	return &seasonHTTPHarness{h: h, d: d, log: log}
}

func postSeason(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sync/season", refBody(t, body))
	rec := httptest.NewRecorder()
	h.HandleSyncSeason(rec, req)
	return rec
}

func TestHandleSyncSeason_202_and_the_registry_lists_the_full_item_set(t *testing.T) {
	t.Parallel()
	hh := newSeasonHTTPHarness(t, &blockingRunner{release: make(chan struct{})})

	rec := postSeason(t, hh.h, SyncSeasonRequest{SeriesID: 7, Season: 1})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var acc SeasonSyncAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || acc.ActivityID == "" {
		t.Fatalf("202 body = %q (err %v), want {activity_id}", rec.Body.String(), err)
	}

	// ALL item records exist at acceptance, ordinal set, filterable by the
	// batch id — a reload sees the full list before any sync:done.
	jobs := hh.d.Jobs(acc.ActivityID)
	if len(jobs) != 3 {
		t.Fatalf("Jobs(batch) = %d records, want the full selection", len(jobs))
	}
	for _, j := range jobs {
		if j.BatchActivityID != acc.ActivityID || j.Ordinal == 0 {
			t.Errorf("job %d = batch %q ordinal %d, want the batch id and a 1-based ordinal", j.JobID, j.BatchActivityID, j.Ordinal)
		}
		if j.Outcome != "" {
			t.Errorf("job %d outcome = %q before any settlement, want none", j.JobID, j.Outcome)
		}
	}

	// The batch activity entry carries the AGGREGATE only.
	entry, ok := hh.log.Get(acc.ActivityID)
	if !ok || entry.Total != 3 {
		t.Errorf("batch entry = %+v, want Total 3", entry)
	}
	if entry.Action != "Season Sync" {
		t.Errorf("entry action = %q, want Season Sync", entry.Action)
	}
}

func TestHandleSyncSeason_repeat_answers_the_live_batch(t *testing.T) {
	t.Parallel()
	hh := newSeasonHTTPHarness(t, &blockingRunner{release: make(chan struct{})})

	first := postSeason(t, hh.h, SyncSeasonRequest{SeriesID: 7, Season: 1})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}
	second := postSeason(t, hh.h, SyncSeasonRequest{SeriesID: 7, Season: 1})
	if second.Code != http.StatusAccepted {
		t.Fatalf("second status = %d: %s", second.Code, second.Body.String())
	}
	var a1, a2 SeasonSyncAccepted
	if err := json.Unmarshal(first.Body.Bytes(), &a1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &a2); err != nil {
		t.Fatal(err)
	}
	if a1.ActivityID != a2.ActivityID {
		t.Errorf("repeat POST minted a new batch %q, want the live one %q", a2.ActivityID, a1.ActivityID)
	}
}

func TestHandleSyncSeason_error_statuses(t *testing.T) {
	t.Parallel()
	hh := newSeasonHTTPHarness(t, &blockingRunner{release: make(chan struct{})})

	cases := []struct {
		name string
		body any
		want int
	}{
		{"unknown series", SyncSeasonRequest{SeriesID: 999, Season: 1}, http.StatusNotFound},
		{"unaddressable series", SyncSeasonRequest{SeriesID: 9, Season: 1}, http.StatusNotFound},
		{"nothing to sync", SyncSeasonRequest{SeriesID: 7, Season: 3}, http.StatusNotFound},
		{"zero series id", SyncSeasonRequest{SeriesID: 0, Season: 1}, http.StatusBadRequest},
		{"negative season", SyncSeasonRequest{SeriesID: 7, Season: -1}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			rec := postSeason(t, hh.h, tc.body)
			if rec.Code != tc.want {
				t.Errorf("POST season (%s) = %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	t.Run("method_not_allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sync/season", nil)
		rec := httptest.NewRecorder()
		hh.h.HandleSyncSeason(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET season = %d, want 405", rec.Code)
		}
	})

	t.Run("arr_failure_is_502", func(t *testing.T) {
		store, sonarr, cfg, _ := seasonFixture(t)
		sonarr.episodesErr = errors.New("sonarr down")
		h := seasonHandler(store, sonarr, cfg)
		rec := postSeason(t, h, SyncSeasonRequest{SeriesID: 7, Season: 1})
		if rec.Code != http.StatusBadGateway {
			t.Errorf("arr-failure season POST = %d, want 502: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandleSyncSeason_capacity_is_a_typed_429(t *testing.T) {
	t.Parallel()
	hh := newSeasonHTTPHarness(t, &blockingRunner{release: make(chan struct{})})

	// Fill the admission lease with blocked singles (real files: the
	// executor reads them at run time before the runner parks).
	dir := t.TempDir()
	for i := range syncjobs.MaxJobs {
		path := filepath.Join(dir, fmt.Sprintf("hold%d.en.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := hh.d.Dispatch(&syncjobs.ExecInput{
			Ref: resolve.FileRef{
				MediaType: subflux.MediaTypeMovie, MediaID: fmt.Sprintf("tmdb-%d", i),
				Language: "en", Variant: "standard", Source: "external",
			},
			SubtitlePath: path,
			VideoPath:    "/hold.mkv",
		}); err != nil {
			t.Fatalf("fill dispatch %d: %v", i, err)
		}
	}

	rec := postSeason(t, hh.h, SyncSeasonRequest{SeriesID: 7, Season: 1})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("season POST at cap = %d, want 429: %s", rec.Code, rec.Body.String())
	}
}

// TestSeasonEnumeration_config_snapshot_at_acceptance pins the snapshot
// rule: the target pairs are resolved from config exactly ONCE per
// dispatch, at accept time — never re-read per episode or per item, so a
// hot config change mid-batch cannot reshape an accepted selection.
func TestSeasonEnumeration_config_snapshot_at_acceptance(t *testing.T) {
	t.Parallel()
	store, sonarr, _, _ := seasonFixture(t)
	cfg := &countingSeasonCfg{targets: []subflux.SubtitleTarget{{Code: "en"}}}
	h := New(Deps{
		Store: store,
		Files: store,
		Resolve: &resolve.Resolver{
			Store: store,
			State: func() *resolve.State { return &resolve.State{Cfg: fakePathValidator{}} },
		},
		SeasonState: func() *SeasonState { return &SeasonState{Cfg: cfg, Sonarr: sonarr} },
	})

	if _, err := h.enumerateSeason(t.Context(), &SyncSeasonRequest{SeriesID: 7, Season: 1}); err != nil {
		t.Fatalf("enumerateSeason() error = %v", err)
	}
	if cfg.calls != 1 {
		t.Errorf("config resolved %d times, want exactly once at acceptance", cfg.calls)
	}
}

type countingSeasonCfg struct {
	targets []subflux.SubtitleTarget
	calls   int
}

func (c *countingSeasonCfg) ResolveTargetsWithFallback(string, []string) []subflux.SubtitleTarget {
	c.calls++
	return c.targets
}
