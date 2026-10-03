package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// switchableWriter is a media writer over a temp root whose writes fail with
// EROFS while failing is set, and which counts its probe writes.
type switchableWriter struct {
	mw      *mediawrite.Writer
	root    string
	mu      sync.Mutex
	failing bool
	probes  int
}

func newSwitchableWriter(t *testing.T) *switchableWriter {
	t.Helper()
	sw := &switchableWriter{root: t.TempDir()}
	mw, err := mediawrite.New(mediawrite.Config{
		Metrics: testsupport.NopMediaMetrics{}, Alerts: testsupport.NopAlerts{},
		Write: func(_ context.Context, p string, _ []byte) error {
			sw.mu.Lock()
			defer sw.mu.Unlock()
			if strings.HasPrefix(filepath.Base(p), ".subflux-write-probe-") {
				sw.probes++
			}
			if sw.failing {
				return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS}
			}
			return nil
		},
		Remove: func(string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	mw.Bind([]string{sw.root}, func(context.Context, string) error { return nil })
	sw.mw = mw
	return sw
}

func (sw *switchableWriter) set(failing bool) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.failing = failing
}

func (sw *switchableWriter) probeCount() int {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.probes
}

func TestHandleScan_an_unwritable_root_answers_409_and_releases_the_flag(t *testing.T) {
	t.Parallel()
	sw := newSwitchableWriter(t)
	sw.set(true)
	s := &Server{
		db:       &qhMockStore{},
		metrics:  obs.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		media:    sw.mw,
		lifetime: t.Context(),
	}
	s.live.Store(&liveState{cfg: testConfig(t)})
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleScan(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/scan", http.NoBody))
		return rec
	}

	rec := post()
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusConflict || body.Code != string(subflux.CodeMediaUnwritable) || !strings.Contains(body.Error, sw.root) {
		t.Fatalf("handleScan(unwritable root) = %d %+v, want 409 media_unwritable naming the root", rec.Code, body)
	}
	if n := len(s.activity.Entries()); n != 0 {
		t.Errorf("a refused scan started %d activities, want none", n)
	}
	if s.scanning.Load() {
		t.Fatal("a refused scan left the scanning flag held")
	}

	sw.set(false)
	if rec := post(); rec.Code != http.StatusAccepted {
		t.Errorf("handleScan after the root recovered = %d, want 202", rec.Code)
	}
	s.bgWg.Wait()
}

func TestHandleScan_a_duplicate_start_probes_nothing(t *testing.T) {
	t.Parallel()
	sw := newSwitchableWriter(t)
	s := &Server{
		db:       &qhMockStore{},
		metrics:  obs.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		media:    sw.mw,
		lifetime: t.Context(),
	}
	s.live.Store(&liveState{cfg: testConfig(t)})
	s.activity.StartScan(scheduler.FullScanAction, scheduler.FullScanDetail,
		activity.SourceScheduled, activity.ScanScope{Kind: activity.ScanKindFull}, auth.RoleAdmin)
	s.scanning.Store(true)

	rec := httptest.NewRecorder()
	s.handleScan(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/scan", http.NoBody))
	if rec.Code != http.StatusAccepted || sw.probeCount() != 0 {
		t.Errorf("duplicate start = %d with %d probe writes, want 202 and none", rec.Code, sw.probeCount())
	}
}
