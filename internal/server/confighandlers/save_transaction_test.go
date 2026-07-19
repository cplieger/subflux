package confighandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
)

// Tests for the save-transaction lock (Handler.saveMu): the COMPLETE save —
// existing-file read + secret merge, canonicalization, old-state comparison
// + arr pings, activation, persistence — must be serialized across every
// save entry point, so live (last-activated) and disk (last-written) config
// generations can never diverge.

// TestSaveTransaction_concurrent_saves_keep_live_and_disk_in_sync is the
// barrier-controlled two-save test. Save A (structured PUT) is held inside
// the hot-reload hook — i.e. mid-transaction, after activation began but
// before persistence — while save B (raw PUT, deliberately the OTHER entry
// point) runs to completion. Without the transaction lock this interleaves
// publish-A, publish-B, write-B, write-A: live state B, disk state A, both
// requests 200. With the lock, B cannot start until A persists, so the
// last-activated config always equals the on-disk config.
//
// The barrier self-releases after a timeout so the FIXED behavior (B blocked
// on the lock, release never reached during A) cannot deadlock; the timeout
// only bounds how long a regression has to expose itself.
func TestSaveTransaction_concurrent_saves_keep_live_and_disk_in_sync(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")

	var (
		mu          sync.Mutex
		liveURL     string // sonarr URL of the last ACTIVATED config
		calls       int
		firstInside = make(chan struct{})
		release     = make(chan struct{})
		releaseOnce sync.Once
	)
	hotReload := func(_ context.Context, cfg api.ConfigProvider) error {
		mu.Lock()
		liveURL = cfg.SonarrConfig().URL
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			close(firstInside)
			select {
			case <-release:
			case <-time.After(250 * time.Millisecond):
			}
		} else {
			releaseOnce.Do(func() { close(release) })
		}
		return nil
	}
	h := newStructuredHandlerAt(t, cfgPath, hotReload)

	const payloadA = `{"sections": {
		"sonarr": {"url": "http://save-a:8989", "api_key": "ka"},
		"languages": {"default": [{"code": "en"}]}
	}}`
	const rawBodyB = `
sonarr:
  url: "http://save-b:8989"
  api_key: "kb"
languages:
  default:
    - code: en
`

	// Save A on its own goroutine; it will park inside the hot-reload hook.
	recA := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recA <- doStructuredSave(t, h, payloadA)
	}()
	<-firstInside

	// Save B through the raw entry point while A is mid-transaction.
	reqB := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(rawBodyB))
	recB := httptest.NewRecorder()
	h.HandleSaveConfig(recB, reqB)

	a := <-recA
	if a.Code != http.StatusOK {
		t.Fatalf("save A status = %d, body %s", a.Code, a.Body.String())
	}
	if recB.Code != http.StatusOK {
		t.Fatalf("save B status = %d, body %s", recB.Code, recB.Body.String())
	}
	mu.Lock()
	gotCalls, gotLive := calls, liveURL
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("hot reload calls = %d, want 2", gotCalls)
	}

	saved, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	cfg, err := config.LoadFromBytes(context.Background(), saved)
	if err != nil {
		t.Fatalf("persisted config does not load: %v\n%s", err, saved)
	}
	if diskURL := cfg.SonarrConfig().URL; diskURL != gotLive {
		t.Errorf("live and disk generations diverged: last activated %q, on disk %q",
			gotLive, diskURL)
	}
}

// TestResetConfig_serializes_with_saves: the unconfigured reset writes the
// config file, so it participates in the same save transaction. While the
// transaction lock is held, a reset must block; once released it completes
// and writes the default config.
func TestResetConfig_serializes_with_saves(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	h := New(&Deps{
		DefaultConfig: []byte("# default config\n"),
		Configured:    func() bool { return false },
		ConfigPath:    func() string { return cfgPath },
	})

	h.saveMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/config/reset", http.NoBody)
		h.HandleResetConfig(httptest.NewRecorder(), req)
	}()
	select {
	case <-done:
		t.Fatal("HandleResetConfig completed while the save-transaction lock was held")
	case <-time.After(50 * time.Millisecond):
		// Still blocked: reset is serialized with saves.
	}
	h.saveMu.Unlock()
	<-done

	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("reset did not write the default config after the lock released: %v", err)
	}
}
