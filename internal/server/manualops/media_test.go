package manualops

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// mediaWriterFailing returns a writer bound to root whose writes into a
// folder for which fail returns an error fail with it.
func mediaWriterFailing(t *testing.T, root string, fail func(path string) error) *mediawrite.Writer {
	t.Helper()
	mw, err := mediawrite.New(mediawrite.Config{
		Metrics: testsupport.NopMediaMetrics{}, Alerts: testsupport.NopAlerts{},
		Write: func(_ context.Context, p string, data []byte) error {
			if err := fail(p); err != nil {
				return err
			}
			return os.WriteFile(p, data, 0o600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mw.Bind([]string{root}, func(context.Context, string) error { return nil })
	return mw
}

// videoUnder creates rel (a video file path) under root.
func videoUnder(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type pathRadarr struct{ path string }

func (r pathRadarr) MovieByID(context.Context, int) (arrapi.Movie, error) {
	return arrapi.Movie{ID: 42, MovieFile: &arrapi.MovieFile{Path: r.path}}, nil
}

// countingDownloads counts Download calls and fails each one.
type countingDownloads struct {
	httpStubProvider
	n atomic.Int32
}

func (p *countingDownloads) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	p.n.Add(1)
	return nil, errHTTPFake
}

func TestHandleManualDownload_a_known_bad_subfolder_refuses_before_any_download(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	video := videoUnder(t, root, "Movies/Film/film.mkv")
	folder := filepath.Dir(video)
	erofs := func(p string) error {
		if filepath.Dir(p) == folder {
			return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS}
		}
		return nil
	}
	mw := mediaWriterFailing(t, root, erofs)
	if err := mw.WriteFile(t.Context(), filepath.Join(folder, "film.en.srt"), []byte("1\n")); err == nil {
		t.Fatal("Setup: the folder was not marked")
	}

	p := &countingDownloads{name: "os"}
	h, wg := newHTTPHarness(&testsupport.NopStore{}, fakeManualCfg{}, []provider.Provider{p})
	h.deps.Media = mw
	h.deps.Resolve = &resolve.Resolver{
		Store: &testsupport.NopStore{},
		State: func() *resolve.State { return &resolve.State{Cfg: fakeManualCfg{}, Radarr: pathRadarr{path: video}} },
	}

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"media_type":"movie","language":"en"}`
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/search/download", strings.NewReader(body)))
	wg.Wait()

	var resp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusConflict || resp.Code != string(subflux.CodeMediaUnwritable) || !strings.Contains(resp.Error, folder) {
		t.Errorf("HandleManualDownload = %d %+v, want 409 media_unwritable naming %s", rec.Code, resp, folder)
	}
	if n := p.n.Load(); n != 0 {
		t.Errorf("provider downloads = %d, want 0", n)
	}
}

// terminalActivity records how each activity ended.
type terminalActivity struct {
	fakeActivity
	ended, failed []string
}

func (a *terminalActivity) End(id string)  { a.ended = append(a.ended, id) }
func (a *terminalActivity) Fail(id string) { a.failed = append(a.failed, id) }

func TestHandleManualDownload_an_accepted_download_saves_the_numbered_subtitle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	video := videoUnder(t, root, "Movies/Film/film.mkv")

	h, wg := newHTTPHarness(&testsupport.NopStore{}, fakeManualCfg{}, []provider.Provider{srtProvider{}})
	act := &terminalActivity{}
	h.deps.Activity = act
	h.deps.Media = mediaWriterFailing(t, root, func(string) error { return nil })
	h.deps.Resolve = &resolve.Resolver{
		Store: &testsupport.NopStore{},
		State: func() *resolve.State { return &resolve.State{Cfg: fakeManualCfg{}, Radarr: pathRadarr{path: video}} },
	}

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"media_type":"movie","language":"en"}`
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/search/download", strings.NewReader(body)))
	wg.Wait()

	if rec.Code != http.StatusAccepted {
		t.Fatalf("HandleManualDownload = %d %s, want 202", rec.Code, rec.Body.String())
	}
	sub := strings.TrimSuffix(video, ".mkv") + ".en.1.srt"
	if got, err := os.ReadFile(sub); err != nil || !strings.Contains(string(got), "Hello there.") {
		t.Errorf("ReadFile(%s) = %q, %v; want the downloaded subtitle", sub, got, err)
	}
	if len(act.ended) != 1 || len(act.failed) != 0 {
		t.Errorf("activity ended %v, failed %v; want one completed download", act.ended, act.failed)
	}
}

func TestRunDownload_a_failed_save_names_the_folder_or_the_path(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fail       func(path string) error
		name       string
		wantPrefix func(folder, sub string) string
	}{
		{
			name: "folder_fault",
			fail: func(p string) error { return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS} },
			wantPrefix: func(folder, _ string) string {
				return "Could not save the subtitle in " + folder + ": "
			},
		},
		{
			name: "per_target_refusal",
			fail: func(p string) error {
				if strings.HasSuffix(p, ".srt") {
					return &atomicfile.WriteError{Err: atomicfile.ErrSymlinkTarget, Phase: atomicfile.PhaseTempCreate}
				}
				return nil
			},
			wantPrefix: func(_, sub string) string { return "Could not save the subtitle at " + sub + ": " },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, ls, video := ordinalHarness(t, srtProvider{})
			folder := filepath.Dir(video)
			rec := &recordingActivity{details: map[string]string{}}
			deps.Activity = rec
			req := &DownloadRequest{
				Provider: "os", SubtitleID: "sub-1", Language: "en",
				MediaType: subflux.MediaTypeMovie, ArrID: 42,
			}
			req.setVideoPath(video)

			mw := mediaWriterFailing(t, folder, tc.fail)
			if runDownload(t.Context(), deps, ls, &recStore{}, mw, req, "act-1") {
				t.Fatal("RunDownload() = true for a failed save")
			}
			sub := strings.TrimSuffix(video, ".mkv") + ".en.1.srt"
			if got, want := rec.details["act-1"], tc.wantPrefix(folder, sub); !strings.HasPrefix(got, want) {
				t.Errorf("activity detail = %q, want prefix %q", got, want)
			}
		})
	}
}
