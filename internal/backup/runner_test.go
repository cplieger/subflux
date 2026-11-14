package backup_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/backup"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"go.yaml.in/yaml/v4"
)

// gauges records what the runner sets on its metrics.
type gauges struct {
	last     time.Time
	interval time.Duration
	failures int
	mu       sync.Mutex
	enabled  bool
}

func (g *gauges) SetBackupSchedule(enabled bool, interval time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled, g.interval = enabled, interval
}

func (g *gauges) SetBackupLastSuccess(at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = at
}

func (g *gauges) IncBackupFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
}

func (g *gauges) failed() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failures
}

// rig is a runner's world inside a synctest bubble: a real store, a status
// store over the reference persister, and the section the runner reads.
type rig struct {
	db     *statedb.Store
	st     *status.Store
	m      *gauges
	logs   *capture.Recorder
	cfg    backup.Config
	path   string
	cancel context.CancelFunc
	done   chan error
}

// newRig parses section over a fresh store; path is where its backups go.
func newRig(t *testing.T, section func(path string) string) *rig {
	t.Helper()
	dir := t.TempDir()
	db, err := statedb.Open(filepath.Join(dir, "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{db: db, st: st, m: &gauges{}, path: filepath.Join(dir, "backups", "nested")}
	r.cfg = parse(t, section(r.path), dir)
	return r
}

func parse(t *testing.T, src, storeDir string) backup.Config {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	c, errs := backup.ParseConfig(doc.Content[0], storeDir)
	if len(errs) > 0 {
		t.Fatalf("ParseConfig(%q) = %v", src, errs)
	}
	return c
}

// start runs a runner over the rig until stop, and waits for its first
// check.
func (r *rig) start(t *testing.T) {
	t.Helper()
	logger, rec := capture.New()
	r.logs = rec
	run := backup.New(backup.Deps{Store: r.db, Status: r.st, Metrics: r.m, Config: func() backup.Config { return r.cfg }, Logger: logger})
	ctx, cancel := context.WithCancel(t.Context())
	r.cancel, r.done = cancel, make(chan error, 1)
	go func() { r.done <- run.Run(ctx) }()
	synctest.Wait()
}

func (r *rig) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// names are the backup folder's file names, sorted.
func (r *rig) names(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(r.path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// backups are the names of the layout.
func (r *rig) backups(t *testing.T) []string {
	t.Helper()
	return slices.DeleteFunc(r.names(t), func(n string) bool {
		_, err := time.Parse(backup.NameLayout, n)
		return err != nil
	})
}

func (r *rig) alerted() bool {
	snap, _ := r.st.Snapshot()
	return slices.ContainsFunc(snap.Alerts, func(a status.Alert) bool { return a.Source == status.AlertBackup })
}

// outcomes are the Backup jobs' outcomes, oldest first.
func (r *rig) outcomes() []status.Outcome {
	snap, _ := r.st.Snapshot()
	var out []status.Outcome
	for _, j := range slices.Backward(snap.Jobs) {
		if j.Kind == status.Backup {
			out = append(out, j.Outcome)
		}
	}
	return out
}

func nameAt(at time.Time) string { return at.UTC().Truncate(time.Second).Format(backup.NameLayout) }

func enabledAt(extra string) func(string) string {
	return func(path string) string { return "enabled: true\npath: " + path + "\n" + extra }
}

func TestFirstTickCreatesMissingDirectory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, enabledAt(""))
		r.start(t)
		defer r.stop(t)
		info, err := os.Stat(r.path)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("backup folder = %v, %v; want it created with mode 0700", info, err)
		}
		if got := r.backups(t); !slices.Equal(got, []string{nameAt(time.Now())}) {
			t.Errorf("backups = %v, want one named for now", got)
		}
		if got := r.outcomes(); !slices.Equal(got, []status.Outcome{status.Succeeded}) || r.alerted() {
			t.Errorf("jobs = %v, alert %v; want one Succeeded and no alert", got, r.alerted())
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, enabledAt(""))
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r.cfg = parse(t, "enabled: true\npath: "+filepath.Join(file, "backups")+"\n", "/config")
		r.start(t)
		defer r.stop(t)
		if got := r.outcomes(); !slices.Equal(got, []status.Outcome{status.Failed}) || !r.alerted() || r.m.failed() != 1 {
			t.Fatalf("jobs = %v, alert %v, failures %d; want one Failed, backup raised, one counted", got, r.alerted(), r.m.failed())
		}
		time.Sleep(14 * time.Minute)
		synctest.Wait()
		if n := r.m.failed(); n != 1 {
			t.Errorf("failures after 14m = %d, want 1: the retry waits 15 minutes", n)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := r.m.failed(); n != 2 {
			t.Errorf("failures after 15m = %d, want 2: the retry ran", n)
		}
	})
}

func TestBackupDueFromNewestName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, enabledAt(""))
		if err := os.MkdirAll(r.path, 0o700); err != nil {
			t.Fatal(err)
		}
		old := nameAt(time.Now().Add(-30 * time.Hour))
		if err := os.WriteFile(filepath.Join(r.path, old), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r.start(t)
		r.stop(t)
		if got := r.backups(t); !slices.Equal(got, []string{old, nameAt(time.Now())}) {
			t.Errorf("backups = %v, want a new one at the first tick after %s", got, old)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, enabledAt(""))
		start := time.Now()
		for range 6 {
			r.start(t)
			time.Sleep(12*time.Hour - 30*time.Second)
			r.stop(t)
		}
		want := []string{nameAt(start), nameAt(start.Add(24 * time.Hour)), nameAt(start.Add(48 * time.Hour))}
		if got := r.backups(t); !slices.Equal(got, want) {
			t.Errorf("backups over six 12h runs = %v, want one a day: %v", got, want)
		}
	})
}

func TestRetentionIgnoresFutureAndForeignNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, enabledAt("retention: 2\n"))
		if err := os.MkdirAll(r.path, 0o700); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		future := nameAt(now.Add(48 * time.Hour))
		foreign := []string{"notes.txt", "subflux.bolt", "subflux-20000101T000000Z.bolt.part", "subflux-2000-01-01T00:00:00Z.bolt", "subflux-19990101T000000.5Z.bolt"}
		for _, n := range append([]string{future, nameAt(now.Add(-72 * time.Hour)), nameAt(now.Add(-48 * time.Hour)), nameAt(now.Add(-30 * time.Hour))}, foreign...) {
			if err := os.WriteFile(filepath.Join(r.path, n), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		r.start(t)
		time.Sleep(23 * time.Hour)
		synctest.Wait()
		r.stop(t)
		want := slices.Sorted(slices.Values(append([]string{nameAt(now.Add(-30 * time.Hour)), nameAt(now), future}, foreign...)))
		if got := r.names(t); !slices.Equal(got, want) {
			t.Errorf("folder = %v, want the newest two past, the future one and every foreign file: %v", got, want)
		}
		if got := r.outcomes(); len(got) != 1 {
			t.Errorf("runs over 23h = %v, want one: the future name does not count as the newest", got)
		}
		var warns int
		for _, rec := range r.logs.Records() {
			if rec.Level == slog.LevelWarn && strings.Contains(rec.Message, "named after now") {
				warns++
			}
		}
		if warns != 1 {
			t.Errorf("WARNs for the future name = %d, want 1", warns)
		}
	})
}

func TestDisabledRunsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, func(path string) string { return "enabled: false\npath: " + path + "\n" })
		r.start(t)
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		r.stop(t)
		if _, err := os.Stat(r.path); !os.IsNotExist(err) || len(r.outcomes()) != 0 {
			t.Errorf("a disabled runner made %v and ran %v, want nothing", err, r.outcomes())
		}
		if r.m.enabled || r.m.interval != 24*time.Hour {
			t.Errorf("schedule gauges = %v, %v; want off at 24h", r.m.enabled, r.m.interval)
		}
	})
}
