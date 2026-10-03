package polling

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/mediapresence"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// switchableStat fails stats of one path with err until err is cleared.
type switchableStat struct {
	err  error
	path string
	mu   sync.Mutex
}

func (s *switchableStat) stat(p string) (fs.FileInfo, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if p == s.path && err != nil {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: err}
	}
	return os.Stat(p)
}

func (s *switchableStat) heal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = nil
}

func presenceFor(t *testing.T, root string, stat func(string) (fs.FileInfo, error)) *mediapresence.Checker {
	t.Helper()
	c, err := mediapresence.New(mediapresence.Config{
		Metrics: testsupport.NopPresenceMetrics{}, Alerts: testsupport.NopAlerts{}, Stat: stat,
	})
	if err != nil {
		t.Fatalf("Setup: mediapresence.New: %v", err)
	}
	c.Bind([]string{root})
	return c
}

func mountedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "other.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return root
}

func TestImport_an_unreadable_video_deletes_no_state_and_holds_on_its_root(t *testing.T) {
	for _, tc := range []struct {
		setup func(t *testing.T) (root, video string, stat func(string) (fs.FileInfo, error))
		name  string
	}{
		{name: "EACCES", setup: failingStat(syscall.EACCES)},
		{name: "EIO", setup: failingStat(syscall.EIO)},
		{name: "ESTALE", setup: failingStat(syscall.ESTALE)},
		{name: "ENOTCONN", setup: failingStat(syscall.ENOTCONN)},
		{name: "root_missing", setup: func(t *testing.T) (string, string, func(string) (fs.FileInfo, error)) {
			root := filepath.Join(t.TempDir(), "unmounted")
			return root, filepath.Join(root, "Show", "e1.mkv"), nil
		}},
		{name: "root_empty", setup: func(t *testing.T) (string, string, func(string) (fs.FileInfo, error)) {
			root := t.TempDir()
			return root, filepath.Join(root, "Show", "e1.mkv"), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, video, stat := tc.setup(t)
			store := &mockStore{}
			deps := fullDeps(store)
			deps.Presence = presenceFor(t, root, stat)
			ls := &LiveState{Cfg: &mockCfg{interval: time.Second, langs: []string{"en"}}}
			p := &Poller{deps: deps, stateFunc: func() *LiveState { return ls }}

			res := importOne(t.Context(), p, ls, video,
				func() (*ImportResult, error) {
					t.Error("buildFn ran for a video whose root cannot be read")
					return nil, nil
				}, nil)

			if len(store.deletedPaths) != 0 {
				t.Errorf("DeleteStateByPaths calls = %v, want none for a %s stat", store.deletedPaths, tc.name)
			}
			if !res.held || res.heldOn != root {
				t.Errorf("import result held = %v on %q, want held on the root %q", res.held, res.heldOn, root)
			}
		})
	}
}

func failingStat(errno error) func(t *testing.T) (string, string, func(string) (fs.FileInfo, error)) {
	return func(t *testing.T) (string, string, func(string) (fs.FileInfo, error)) {
		root := mountedRoot(t)
		video := filepath.Join(root, "Show", "e1.mkv")
		s := &switchableStat{path: video, err: errno}
		return root, video, s.stat
	}
}

func TestImport_a_missing_video_under_a_mounted_root_deletes_its_state(t *testing.T) {
	root := mountedRoot(t)
	video := filepath.Join(root, "Show", "e1.mkv")
	store := &mockStore{}
	deps := fullDeps(store)
	deps.Presence = presenceFor(t, root, nil)
	ls := &LiveState{Cfg: &mockCfg{interval: time.Second, langs: []string{"en"}}}
	p := &Poller{deps: deps, stateFunc: func() *LiveState { return ls }}

	res := importOne(t.Context(), p, ls, video,
		func() (*ImportResult, error) {
			t.Error("buildFn ran for a deleted video")
			return nil, nil
		}, nil)

	if len(store.deletedPaths) != 1 || store.deletedPaths[0][0] != video {
		t.Errorf("DeleteStateByPaths calls = %v, want one for %s", store.deletedPaths, video)
	}
	if res.held {
		t.Errorf("import of a deleted video held on %q, want skipped", res.heldOn)
	}
}

func TestPoller_an_unreadable_root_holds_the_batch_until_it_reads_again(t *testing.T) {
	r := newHoldRig(t)
	e1 := r.video(t, "Show", 1)
	s := &switchableStat{path: pathOf(e1), err: syscall.EIO}
	presence := presenceFor(t, r.root, s.stat)
	r.p.deps.Presence = presence
	r.setHistory(e1)

	r.p.PollOnce(t.Context())
	drainOne(t, r.p)
	if r.engine.total() != 0 {
		t.Fatalf("engine searches = %d, want 0 while the root cannot be read", r.engine.total())
	}
	if got := r.cursor(t); !got.Equal(e1.Date) {
		t.Errorf("durable cursor = %v, want held at E1 (%v)", got, e1.Date)
	}
	if folder, skip := r.p.skipHeld(subflux.PollKeySonarr); !skip || folder != r.root {
		t.Errorf("skipHeld = %q, %v; want the source held on %q", folder, skip, r.root)
	}

	s.heal()
	if _, err := presence.Gone(t.Context(), pathOf(e1)); err != nil {
		t.Fatalf("Setup: recheck of the healed path = %v", err)
	}
	r.p.PollOnce(t.Context())
	drainOne(t, r.p)
	if n := r.engine.count(pathOf(e1)); n != 1 {
		t.Errorf("E1 searches after the root reads again = %d, want 1", n)
	}
	if got := r.cursor(t); !got.Equal(past(e1)) {
		t.Errorf("durable cursor = %v, want just past E1 (%v)", got, past(e1))
	}
}
