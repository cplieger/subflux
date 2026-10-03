package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/server/activity"
)

func TestNew_requires_metrics_and_alerts(t *testing.T) {
	h := newHarness(t)
	var noMetrics *obs.Metrics
	var noAlerts *activity.AlertLog
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no Metrics", cfg: Config{Alerts: h.alerts}},
		{name: "no Alerts", cfg: Config{Metrics: h.metrics}},
		{name: "nil *obs.Metrics", cfg: Config{Metrics: noMetrics, Alerts: h.alerts}},
		{name: "nil *activity.AlertLog", cfg: Config{Metrics: h.metrics, Alerts: noAlerts}},
	}
	for _, tt := range tests {
		if w, err := New(tt.cfg); err == nil {
			t.Errorf("New(%s) = (%v, nil), want an error", tt.name, w)
		}
	}
}

func TestWriteFile_a_confirmed_fault_marks_the_folder_once(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })

	err := h.w.WriteFile(t.Context(), filepath.Join(show, "ep.en.srt"), []byte("1\n"))
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || !errors.Is(err, ErrUnwritable) || !errors.Is(err, syscall.EROFS) {
		t.Fatalf("WriteFile = %v, want an *UnwritableError matching ErrUnwritable and EROFS", err)
	}
	if uerr.Folder != show || uerr.Root != h.root || uerr.Op != opWrite {
		t.Errorf("UnwritableError = %+v, want folder %s, root %s, op write", uerr, show, h.root)
	}
	if err := h.w.WriteFile(t.Context(), filepath.Join(show, "ep2.en.srt"), []byte("1\n")); !errors.Is(err, ErrUnwritable) {
		t.Errorf("second WriteFile = %v, want ErrUnwritable", err)
	}

	if f, ok := h.w.Blocked(filepath.Join(show, "S01", "x.mkv")); !ok || f != show {
		t.Errorf("Blocked(below the folder) = %q, %v, want %q, true", f, ok, show)
	}
	if f, ok := h.w.Blocked(filepath.Join(h.root, "tv", "Other", "x.mkv")); ok {
		t.Errorf("Blocked(sibling) = %q, true, want false", f)
	}
	if g := h.gauge(t, h.root); g != 1 {
		t.Errorf("gauge = %v, want 1", g)
	}
	errs := h.logs.at(slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("ERROR records = %d, want exactly 1: %+v", len(errs), errs)
	}
	if a := errs[0].attrs; a["folder"] != show || a["root"] != h.root || !strings.Contains(a["error"], "read-only") {
		t.Errorf("ERROR attrs = %v, want folder, root and the EROFS error", a)
	}
	if !h.alerts.HasUndismissed(folderAlertPrefix + show) {
		t.Errorf("alerts = %v, want %s", h.alertSources(), folderAlertPrefix+show)
	}
}

func TestWriteFile_success_clears_a_marked_folder(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })
	_ = h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n"))

	h.setWrite(nil)
	if err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n")); err != nil {
		t.Fatalf("WriteFile after recovery = %v, want nil", err)
	}
	if len(h.logs.withMsg("media folder writable again")) != 1 {
		t.Error("want one INFO 'media folder writable again'")
	}
	if h.alerts.HasUndismissed(folderAlertPrefix + show) {
		t.Error("the folder alert is still undismissed after the clear")
	}
	if g := h.gauge(t, h.root); g != 0 {
		t.Errorf("gauge = %v, want 0", g)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
		t.Error("Blocked = true after the clear")
	}
}

func TestAlerts_are_capped_with_a_root_aggregate(t *testing.T) {
	h := newHarness(t)
	tv := h.dir(t, "tv")
	h.setWrite(failUnder(tv, erofs))
	var folders []string
	for i := range 70 {
		f := h.dir(t, fmt.Sprintf("tv/Show%02d", i))
		folders = append(folders, f)
		_ = h.w.WriteFile(t.Context(), filepath.Join(f, "e.srt"), []byte("1\n"))
	}

	own, agg := 0, 0
	for _, s := range h.alertSources() {
		switch {
		case strings.HasPrefix(s, folderAlertPrefix):
			own++
		case s == rootAlertPrefix+h.root:
			agg++
		}
	}
	if own != maxFolderAlerts || agg != 1 {
		t.Fatalf("alerts: %d per-folder and %d aggregate, want %d and 1", own, agg, maxFolderAlerts)
	}
	if msg := h.alertMessage(rootAlertPrefix + h.root); !strings.HasPrefix(msg, "70 folders under "+h.root) {
		t.Errorf("aggregate message = %q, want it to count 70 folders", msg)
	}

	if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true}); err != nil {
		t.Fatalf("Roots preflight = %v, want nil (the root itself is writable)", err)
	}
	for _, f := range folders {
		if _, ok := h.w.Blocked(filepath.Join(f, "e.mkv")); !ok {
			t.Fatalf("Blocked(%s) = false after a root probe, want the entry kept", f)
		}
	}
	if _, ok := h.w.Blocked(filepath.Join(h.root, "e.mkv")); ok {
		t.Error("the root is marked after its probe passed")
	}

	h.setWrite(nil)
	for _, f := range folders {
		if err := h.w.WriteFile(t.Context(), filepath.Join(f, "e.srt"), []byte("1\n")); err != nil {
			t.Fatalf("WriteFile(%s) = %v", f, err)
		}
	}
	if s := h.alertSources(); len(s) != 0 {
		t.Errorf("alerts after every folder cleared = %v, want none", s)
	}
}

func TestBind_adds_and_drops_root_series(t *testing.T) {
	h := newHarness(t)
	other := t.TempDir()
	h.w.Bind([]string{h.root, other}, h.validate)
	if g := h.gauge(t, other); g != 0 {
		t.Errorf("new root gauge = %v, want 0", g)
	}

	h.setWrite(failUnder(other, erofs))
	_ = h.w.WriteFile(t.Context(), filepath.Join(other, "a.srt"), []byte("1\n"))
	if !h.alerts.HasUndismissed(folderAlertPrefix + other) {
		t.Fatal("Setup: the root was not marked")
	}

	h.w.Bind([]string{h.root}, h.validate)
	if _, ok := h.series(t, `subflux_media_root_unwritable{root="`+other+`"}`); ok {
		t.Error("removed root's series is still exported")
	}
	if h.alerts.HasUndismissed(folderAlertPrefix + other) {
		t.Error("removed root's alert is still raised")
	}
	if _, ok := h.w.Blocked(filepath.Join(other, "a.mkv")); ok {
		t.Error("removed root's bad entry survived the Bind")
	}
}

func TestBind_a_nested_root_owns_its_folders(t *testing.T) {
	h := newHarness(t)
	nested := h.dir(t, "anime")
	show := h.dir(t, "anime/Show")
	h.w.Bind([]string{h.root, nested}, h.validate)
	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })

	err := h.w.WriteFile(t.Context(), filepath.Join(show, "ep.en.srt"), []byte("1\n"))
	var uerr *UnwritableError
	if !errors.As(err, &uerr) {
		t.Fatalf("WriteFile = %v, want an *UnwritableError", err)
	}
	if uerr.Root != nested {
		t.Errorf("UnwritableError.Root = %q, want the nested root %q", uerr.Root, nested)
	}
	if g := h.gauge(t, nested); g != 1 {
		t.Errorf("nested root gauge = %v, want 1", g)
	}
	if g := h.gauge(t, h.root); g != 0 {
		t.Errorf("parent root gauge = %v, want 0", g)
	}
}

// A probe that began before a Bind removed its root fails afterwards and
// must not bring back the folder's mark, alert or root series.
func TestBind_a_probe_from_a_removed_root_settles_nothing(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		if isProbeName(p) {
			once.Do(func() { close(entered) })
			<-release
			return erofs(p)
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}) }()
	<-entered

	h.w.Bind([]string{t.TempDir()}, h.validate)
	close(release)
	if err := <-done; err != nil {
		t.Errorf("Preflight whose folder left every root = %v, want nil", err)
	}

	if f, ok := h.w.Blocked(filepath.Join(show, "e.mkv")); ok {
		t.Errorf("Blocked = %q, true; want the stale probe to mark nothing", f)
	}
	if got := h.alertSources(); len(got) != 0 {
		t.Errorf("alerts = %v, want none", got)
	}
	if v, ok := h.series(t, `subflux_media_root_unwritable{root="`+h.root+`"}`); ok {
		t.Errorf("removed root's series is exported again with value %v", v)
	}
}

func TestBind_drops_the_passing_probe_cache(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	folders := PreflightRequest{Folders: []string{show}}
	_ = h.w.Preflight(t.Context(), folders)
	_ = h.w.Preflight(t.Context(), folders)
	if n := h.probeWrites(show); n != 1 {
		t.Fatalf("Setup: preflights within the TTL probed %d times, want 1", n)
	}

	h.w.Bind([]string{h.root}, h.validate)
	_ = h.w.Preflight(t.Context(), folders)
	if n := h.probeWrites(show); n != 2 {
		t.Errorf("a preflight after Bind probed %d times in total, want 2 (no cached pass)", n)
	}
}

func TestFolderFault(t *testing.T) {
	wrap := func(err error) error { return &atomicfile.WriteError{Err: err, Phase: atomicfile.PhaseTempCreate} }
	never := []error{
		context.Canceled, context.DeadlineExceeded, fs.ErrNotExist,
		atomicfile.ErrFileTooLarge, atomicfile.ErrSymlinkTarget, atomicfile.ErrNotRegular,
		atomicfile.ErrRaced, atomicfile.ErrUnsafePath, atomicfile.ErrEmptyPath,
	}
	for _, e := range never {
		for _, err := range []error{e, wrap(e)} {
			if folderFault(err, writeSource) || folderFault(err, probeSource) {
				t.Errorf("folderFault(%v) = true, want false for both sources", err)
			}
		}
	}
	for _, e := range []error{syscall.ENAMETOOLONG, syscall.EINVAL, syscall.EILSEQ} {
		err := wrap(&fs.PathError{Op: "rename", Path: "/x", Err: e})
		if folderFault(err, writeSource) || !folderFault(err, probeSource) {
			t.Errorf("folderFault(%v) = write %v, probe %v; want false, true",
				err, folderFault(err, writeSource), folderFault(err, probeSource))
		}
	}
	faults := []error{atomicfile.ErrModeNotStored, errProbeTimeout}
	for _, e := range []syscall.Errno{syscall.EROFS, syscall.EACCES, syscall.EPERM, syscall.EBUSY, syscall.ENOSPC, syscall.EDQUOT, syscall.EIO} {
		faults = append(faults, wrap(&fs.PathError{Op: "open", Path: "/x", Err: e}))
	}
	for _, err := range faults {
		if !folderFault(err, writeSource) || !folderFault(err, probeSource) {
			t.Errorf("folderFault(%v) = false, want true for both sources", err)
		}
	}
}

func TestWriteFile_a_per_target_error_is_returned_unwrapped(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	name := filepath.Join(show, strings.Repeat("n", 250)+".en.srt")
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		return &atomicfile.WriteError{Err: &fs.PathError{Op: "rename", Path: p, Err: syscall.ENAMETOOLONG}, Phase: atomicfile.PhaseRename}
	})

	err := h.w.WriteFile(t.Context(), name, []byte("1\n"))
	if !errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, ErrUnwritable) {
		t.Fatalf("WriteFile = %v, want the ENAMETOOLONG error without ErrUnwritable", err)
	}
	if n := h.probeWrites(""); n != 0 {
		t.Errorf("probe writes = %d, want 0 (no confirmation for a per-target error)", n)
	}
	if _, ok := h.w.Blocked(name); ok {
		t.Error("a per-target error marked the folder")
	}
	if w, e := len(h.logs.at(slog.LevelWarn)), len(h.logs.at(slog.LevelError)); w != 1 || e != 0 {
		t.Errorf("WARN=%d ERROR=%d, want 1 and 0", w, e)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h.setWrite(func(ctx context.Context, _ string, _ []byte) error { return ctx.Err() })
	before := h.logs.count()
	if err := h.w.WriteFile(ctx, name, []byte("1\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteFile(cancelled) = %v, want context.Canceled", err)
	}
	if h.logs.count() != before {
		t.Error("a cancelled write logged")
	}
}

func TestWriteFile_confirm_by_probe_spares_a_writable_folder(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	sub := filepath.Join(show, "ep.en.srt")
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		if p == sub {
			return &fs.PathError{Op: "rename", Path: p, Err: syscall.EACCES}
		}
		return nil
	})
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nhello there\n")

	err := h.w.WriteFile(t.Context(), sub, data)
	if !errors.Is(err, syscall.EACCES) || errors.Is(err, ErrUnwritable) {
		t.Fatalf("WriteFile = %v, want the EACCES error without ErrUnwritable", err)
	}
	if sizes := h.probeSizes(); len(sizes) != 1 || sizes[0] != len(data) {
		t.Errorf("confirming probe sizes = %v, want one of %d bytes", sizes, len(data))
	}
	if _, ok := h.w.Blocked(sub); ok || len(h.alertSources()) != 0 || h.gauge(t, h.root) != 0 {
		t.Error("an unconfirmed fault marked the folder, raised an alert or set the gauge")
	}
	warns := h.logs.withMsg("subtitle not written; its folder accepts other writes")
	if len(warns) != 1 || warns[0].attrs["path"] != sub || len(h.logs.at(slog.LevelError)) != 0 {
		t.Errorf("WARN = %+v, ERROR = %d; want one WARN naming the path and no ERROR", warns, len(h.logs.at(slog.LevelError)))
	}

	// A folder already bad is cleared by the passing confirmation.
	h.w.fail(show, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
	_ = h.w.WriteFile(t.Context(), sub, data)
	if _, ok := h.w.Blocked(sub); ok {
		t.Error("the passing confirmation did not clear the bad folder")
	}
	if len(h.logs.withMsg("media folder writable again")) != 1 {
		t.Error("want one INFO for the clear")
	}
}

func TestWriteFile_confirm_by_probe_is_sized_to_the_subtitle(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setWrite(func(_ context.Context, p string, data []byte) error {
		if len(data) >= 64 {
			return &fs.PathError{Op: "write", Path: p, Err: syscall.ENOSPC}
		}
		return nil
	})
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); err != nil {
		t.Fatalf("20-byte preflight = %v, want nil", err)
	}

	err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), make([]byte, 100))
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || uerr.Op != opWrite || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("WriteFile = %v, want an *UnwritableError op write with ENOSPC", err)
	}
	errs := h.logs.at(slog.LevelError)
	if len(errs) != 1 || errs[0].attrs["probe_error"] == "" || !strings.Contains(errs[0].attrs["error"], "a.srt") {
		t.Fatalf("ERROR records = %+v, want one naming the write's error and a probe_error", errs)
	}
	if h.gauge(t, h.root) != 1 || len(h.alertSources()) != 1 {
		t.Errorf("gauge %v, alerts %v; want 1 and one alert", h.gauge(t, h.root), h.alertSources())
	}
}

func TestPreflight_probe_files_and_cleanup_failures(t *testing.T) {
	h := newHarness(t)
	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })
	err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || !errors.Is(err, ErrUnwritable) || uerr.Folder != h.root || uerr.Op != opProbe {
		t.Fatalf("Roots preflight = %v, want an *UnwritableError for the root, op probe", err)
	}
	for _, c := range h.writesSnapshot() {
		if !probeNameRE.MatchString(filepath.Base(c.path)) {
			t.Errorf("probe file name %q does not match %s", filepath.Base(c.path), probeNameRE)
		}
	}

	h.setWrite(nil)
	h.setRemove(func(string) error { return &fs.PathError{Op: "remove", Path: "x", Err: syscall.EACCES} })
	err = h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
	if !errors.As(err, &uerr) || uerr.Op != opProbeCleanup {
		t.Fatalf("Roots preflight with a failing delete = %v, want op probe cleanup", err)
	}
}

func TestPreflight_a_probe_file_already_gone_is_a_pass(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.w.fail(show, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
	h.setRemove(func(p string) error {
		if isProbeName(p) {
			return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
		}
		return nil
	})
	errsBefore := len(h.logs.at(slog.LevelError))

	if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true, RecheckBad: true}); err != nil {
		t.Fatalf("preflight = %v, want nil", err)
	}
	if _, ok := h.w.Blocked(filepath.Join(h.root, "a.mkv")); ok {
		t.Error("the root was marked")
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
		t.Error("the bad folder was not cleared")
	}
	if len(h.logs.withMsg("media folder writable again")) != 1 || len(h.logs.at(slog.LevelError)) != errsBefore {
		t.Error("want one INFO clear and no new ERROR")
	}
}

func TestDefaultWrite_follows_the_umask(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })
	h := newHarness(t, func(c *Config) { c.Write = nil })
	path := filepath.Join(h.root, ".subflux-write-probe-0000000000000000")
	if err := h.w.cfg.Write(t.Context(), path, []byte(probeLine)); err != nil {
		t.Fatalf("default Write = %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("default Write mode under umask 027 = %v, want 0640", got)
	}
	if err := h.w.cfg.Write(t.Context(), filepath.Join(h.root, "big.srt"), make([]byte, 2<<20)); !errors.Is(err, atomicfile.ErrFileTooLarge) {
		t.Errorf("default Write over MaxBytes = %v, want ErrFileTooLarge", err)
	}
}
