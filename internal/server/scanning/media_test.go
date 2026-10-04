package scanning

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// fakeMedia answers every preflight with err and records the requests.
type fakeMedia struct {
	err  error
	reqs []mediawrite.PreflightRequest
	mu   sync.Mutex
}

func (f *fakeMedia) Preflight(ctx context.Context, req mediawrite.PreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.err
}

func (f *fakeMedia) requests() []mediawrite.PreflightRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mediawrite.PreflightRequest(nil), f.reqs...)
}

func (*fakeMedia) Blocked(string) (string, bool) { return "", false }

func unwritable(folder string) *mediawrite.UnwritableError {
	return &mediawrite.UnwritableError{Folder: folder, Root: "/media", Op: "probe", Err: syscall.EROFS}
}

func TestRunFullScan_a_refused_root_starts_nothing(t *testing.T) {
	t.Parallel()
	rig := newFullScanRig(t, foundOneSubtitle("/media/e.fr.srt"))
	media := &fakeMedia{err: unwritable("/media")}
	rig.deps.Media = media
	engine := rig.ls.Engine.(*fakeEngine)

	res := RunFullScan(t.Context(), make(chan struct{}), rig.deps, rig.ls, rig.actID)
	if res.Outcome != activity.OutcomeFailed || !res.MediaUnwritable {
		t.Fatalf("RunFullScan = %+v, want failed with MediaUnwritable", res)
	}
	if engine.callCount() != 0 || rig.sonarr.wantedCalls != 0 || rig.radarr.wantedCalls != 0 || rig.sonarr.resolveCalls != 0 {
		t.Errorf("engine %d, sonarr wanted %d, radarr wanted %d, tag resolves %d; want no work at all",
			engine.callCount(), rig.sonarr.wantedCalls, rig.radarr.wantedCalls, rig.sonarr.resolveCalls)
	}
	if e, _ := rig.log.Get(rig.actID); !strings.HasPrefix(e.Detail, "Not started: ") || !strings.Contains(e.Detail, "/media") {
		t.Errorf("activity detail = %q, want it to start %q and name the folder", e.Detail, "Not started: ")
	}
	if len(rig.db.markSets) != 0 || rig.db.cleared != 0 {
		t.Errorf("cycle mark set %d times and cleared %d times, want untouched", len(rig.db.markSets), rig.db.cleared)
	}
	want := mediawrite.PreflightRequest{Roots: true, RecheckBad: true, Raise: true}
	if got := media.requests(); len(got) != 1 || got[0].Roots != want.Roots || got[0].RecheckBad != want.RecheckBad || got[0].Raise != want.Raise || got[0].Folders != nil {
		t.Errorf("preflight requests = %+v, want one %+v", got, want)
	}
}

func TestRunFullScan_a_mid_run_folder_fault_stops_the_scan(t *testing.T) {
	t.Parallel()
	result := foundOneSubtitle("")
	result.Langs[0].Paths = nil
	result.Langs[0].WriteBlocked = 1
	result.WriteFailure = unwritable("/media/tv/Show")
	rig := newFullScanRig(t, result)
	engine := rig.ls.Engine.(*fakeEngine)

	res := RunFullScan(t.Context(), make(chan struct{}), rig.deps, rig.ls, rig.actID)
	if res.Outcome != activity.OutcomeFailed || !res.MediaUnwritable {
		t.Fatalf("RunFullScan = %+v, want failed with MediaUnwritable", res)
	}
	if n := engine.callCount(); n != 1 {
		t.Errorf("engine calls = %d, want 1 (the scan stops after the item that found the fault)", n)
	}
	if rig.db.cleared != 0 {
		t.Error("the cycle mark was cleared, so the retry would not resume")
	}
	if e, _ := rig.log.Get(rig.actID); !strings.HasPrefix(e.Detail, "Stopped: ") || !strings.Contains(e.Detail, "/media/tv/Show") {
		t.Errorf("activity detail = %q, want it to start %q and name the folder", e.Detail, "Stopped: ")
	}
	if infos := rig.alerts.infoAlerts(); len(infos) != 0 {
		t.Errorf("a stopped scan recorded the completion summary %q", infos)
	}
}

func TestRunFullScan_counts_write_blocked_items(t *testing.T) {
	t.Parallel()
	result := foundOneSubtitle("")
	result.Langs[0].Paths = nil
	result.Langs[0].WriteBlocked = 1
	rig := newFullScanRig(t, result)

	res := RunFullScan(t.Context(), make(chan struct{}), rig.deps, rig.ls, rig.actID)
	if res.Outcome != activity.OutcomeCompleted || res.MediaUnwritable {
		t.Fatalf("RunFullScan = %+v, want completed: a known-bad folder is skipped, not fatal", res)
	}
	if infos := rig.alerts.infoAlerts(); len(infos) != 1 || !strings.Contains(infos[0], ", 2 in unwritable folders") {
		t.Errorf("summary = %q, want it to count the two write-blocked items", infos)
	}
}

func TestScopedScans_refuse_an_unwritable_folder_before_the_202(t *testing.T) {
	t.Parallel()
	cases := []struct {
		invoke     func(rig *scanRig, w *httptest.ResponseRecorder)
		name       string
		wantFolder string
	}{
		{name: "series", wantFolder: "/media/tv/Show", invoke: func(rig *scanRig, w *httptest.ResponseRecorder) {
			rig.h.HandleScanSeries(w, scanRequest(t, "/api/scan/series/42", ""))
		}},
		{name: "season", wantFolder: "/media/tv/Show", invoke: func(rig *scanRig, w *httptest.ResponseRecorder) {
			rig.h.HandleScanSeason(w, scanRequest(t, "/api/scan/season/42/1", ""))
		}},
		{name: "item_episode", wantFolder: "/media/tv/Show", invoke: func(rig *scanRig, w *httptest.ResponseRecorder) {
			rig.h.HandleScanItem(w, scanRequest(t, "/api/scan/item", `{"media_type":"episode","media_id":42,"season":1,"episode":1}`))
		}},
		{name: "movie", wantFolder: "/media/movies/Film", invoke: func(rig *scanRig, w *httptest.ResponseRecorder) {
			rig.h.HandleScanMovie(w, scanRequest(t, "/api/scan/movie/7", ""))
		}},
		{name: "item_movie", wantFolder: "/media/movies/Film", invoke: func(rig *scanRig, w *httptest.ResponseRecorder) {
			rig.h.HandleScanItem(w, scanRequest(t, "/api/scan/item", `{"media_type":"movie","media_id":7}`))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := newScanRig(t)
			rig.sonarr.series.Path = "/media/tv/Show"
			rig.radarr.movie.Path = "/media/movies/Film"
			rig.radarr.movie.MovieFile = &arrapi.MovieFile{Path: "/media/movies/Film/Film.mkv"}
			rig.media.err = unwritable(tc.wantFolder)

			w := httptest.NewRecorder()
			tc.invoke(rig, w)
			var body struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			_ = json.NewDecoder(w.Body).Decode(&body)
			if w.Code != http.StatusConflict || body.Code != string(subflux.CodeMediaUnwritable) || !strings.Contains(body.Error, tc.wantFolder) {
				t.Errorf("%s scan = %d %+v, want 409 media_unwritable naming %s", tc.name, w.Code, body, tc.wantFolder)
			}
			if n := len(rig.log.Entries()); n != 0 {
				t.Errorf("%s scan started %d activities, want none", tc.name, n)
			}
			reqs := rig.media.requests()
			if len(reqs) != 1 || len(reqs[0].Folders) != 1 || reqs[0].Folders[0] != tc.wantFolder || !reqs[0].Raise || reqs[0].Roots {
				t.Errorf("%s preflight = %+v, want one Raise request for [%s]", tc.name, reqs, tc.wantFolder)
			}
		})
	}
}

func TestScopedScan_a_movie_without_a_file_tests_its_folder(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.radarr.movie.Path = "/media/movies/Film"
	rig.radarr.movie.MovieFile = nil
	rig.media.err = unwritable("/media/movies/Film")

	code, _ := rig.post(t, rig.h.HandleScanMovie, "/api/scan/movie/7", "")
	if reqs := rig.media.requests(); code != http.StatusConflict || len(reqs) != 1 || reqs[0].Folders[0] != "/media/movies/Film" {
		t.Errorf("HandleScanMovie(no file) = %d with preflight %+v, want 409 for the movie's folder", code, reqs)
	}
}

// countingSonarr records every call the handler makes before the 202.
type countingSonarr struct {
	*fakeSonarr
	calls []string
	mu    sync.Mutex
}

func (c *countingSonarr) SeriesByID(ctx context.Context, id int) (arrapi.Series, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "series")
	c.mu.Unlock()
	return c.fakeSonarr.SeriesByID(ctx, id)
}

func (c *countingSonarr) Episodes(ctx context.Context, id int) ([]arrapi.Episode, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "episodes")
	c.mu.Unlock()
	return c.fakeSonarr.Episodes(ctx, id)
}

func TestScanItem_a_bad_season_folder_refuses_another_season(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	season1 := filepath.Join(show, "Season 01")
	if err := os.MkdirAll(season1, 0o750); err != nil {
		t.Fatal(err)
	}
	var failSeason bool
	mw, err := mediawrite.New(mediawrite.Config{
		Metrics: testsupport.NopMediaMetrics{}, Alerts: testsupport.NopAlerts{},
		Write: func(_ context.Context, p string, _ []byte) error {
			if failSeason && filepath.Dir(p) == season1 {
				return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mw.Bind([]string{root}, func(context.Context, string) error { return nil })
	failSeason = true
	if err := mw.WriteFile(t.Context(), filepath.Join(season1, "e.srt"), []byte("1\n")); !errors.Is(err, mediawrite.ErrUnwritable) {
		t.Fatalf("Setup: marking Season 01 = %v", err)
	}

	rig := newScanRig(t)
	rig.sonarr.series.Path = show
	sonarr := &countingSonarr{fakeSonarr: rig.sonarr}
	h := rig.h
	h.deps.StateFunc = func() (*HandlerState, *LiveState) {
		return &HandlerState{Cfg: rig.cfg, Sonarr: sonarr}, &LiveState{Cfg: rig.cfg, Engine: rig.engine}
	}
	h.deps.ScanDeps = func() *Deps { return &Deps{Events: rig.ev, Activity: rig.act, Alerts: nopAlerts{}, Media: mw} }

	code, _ := rig.post(t, h.HandleScanItem, "/api/scan/item", `{"media_type":"episode","media_id":42,"season":2,"episode":1}`)
	if code != http.StatusConflict {
		t.Errorf("S02E01 scan with Season 01 bad = %d, want 409", code)
	}
	if got := strings.Join(sonarr.calls, ","); got != "series" {
		t.Errorf("sonarr calls before the answer = %q, want only the series lookup", got)
	}
}

func scanRequest(t *testing.T, target, body string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
}

func TestScopedScan_a_mid_run_folder_fault_fails_the_scan(t *testing.T) {
	t.Parallel()
	rig := newScanRig(t)
	rig.sonarr.episodes = epsWithFiles(3)
	rig.engine.result = subflux.SearchResult{
		Langs:        []subflux.LangOutcome{{Lang: "fr", Kind: subflux.LangSearched, Searched: 1, WriteBlocked: 1}},
		WriteFailure: unwritable("/media/tv/Show"),
	}

	code, accepted := rig.post(t, rig.h.HandleScanSeries, "/api/scan/series/42", "")
	if code != http.StatusAccepted {
		t.Fatalf("HandleScanSeries = %d, want 202", code)
	}
	waitFor(t, "the scan to end", func() bool {
		e, ok := rig.log.Get(accepted.ActivityID)
		return ok && e.Done
	})
	e, _ := rig.log.Get(accepted.ActivityID)
	if !e.Failed || !strings.HasPrefix(e.Detail, "Stopped: ") {
		t.Errorf("activity = failed %v, detail %q; want failed with a Stopped: detail", e.Failed, e.Detail)
	}
	if n := rig.engine.callCount(); n != 1 {
		t.Errorf("engine calls = %d, want 1 (stopped at the first episode)", n)
	}
}
