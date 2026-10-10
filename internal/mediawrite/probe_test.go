package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/config"
)

var probeNameRE = regexp.MustCompile(`^\.subflux-write-probe-[0-9a-f]{16}$`)

func TestPreflight_Roots_ignores_a_bad_subfolder_RecheckBad_reprobes_it(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setWrite(failUnder(show, erofs))
	_ = h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n"))

	if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true}); err != nil {
		t.Fatalf("Roots preflight = %v, want nil", err)
	}
	if n := h.probeWrites(show); n != 1 {
		t.Errorf("probe writes into the subfolder after a Roots preflight = %d, want 1 (only its write confirmation)", n)
	}
	if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true, RecheckBad: true}); err != nil {
		t.Fatalf("Roots+RecheckBad preflight while the subfolder fails = %v, want nil", err)
	}
	if n := h.probeWrites(show); n != 2 {
		t.Errorf("probe writes into the subfolder = %d, want 2 (re-probed by RecheckBad)", n)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); !ok {
		t.Fatal("the still-failing subfolder was cleared")
	}

	h.setWrite(nil)
	if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true, RecheckBad: true}); err != nil {
		t.Fatalf("Roots+RecheckBad preflight after recovery = %v", err)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
		t.Error("RecheckBad did not clear the recovered subfolder")
	}
}

func TestPreflight_probes_bound_roots_concurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		roots := []string{h.root, t.TempDir(), t.TempDir(), t.TempDir()}
		h.w.Bind(roots, h.validate)
		release := make(chan struct{})
		h.setWrite(func(context.Context, string, []byte) error { <-release; return nil })

		start := time.Now()
		err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
		if elapsed := time.Since(start); elapsed > defaultProbeTimeout+time.Second {
			t.Errorf("four hung roots took %v, want at most %v", elapsed, defaultProbeTimeout+time.Second)
		}
		if !errors.Is(err, errProbeTimeout) {
			t.Errorf("Preflight = %v, want the probe timeout", err)
		}
		close(release)
		synctest.Wait()
	})
}

func TestProbe_stale_probe_files_are_swept(t *testing.T) {
	h := newHarness(t)
	h.setRemove(os.Remove)
	show := h.dir(t, "tv/Show")
	stale := filepath.Join(show, probePrefix+"0123456789abcdef")
	other := filepath.Join(show, ".other")
	for _, p := range []string{stale, other} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); err != nil {
		t.Fatalf("Preflight = %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale probe file still present (stat err %v)", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another dotfile was removed: %v", err)
	}
}

func TestProbe_is_single_flight_and_bounded_by_the_timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		release := make(chan struct{})
		h.setWrite(func(context.Context, string, []byte) error { <-release; return nil })
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { _ = h.w.Preflight(t.Context(), PreflightRequest{Roots: true}) })
		}
		synctest.Wait()
		if n := h.probeWrites(h.root); n != 1 {
			t.Errorf("probe writes for 20 concurrent preflights = %d, want 1", n)
		}
		close(release)
		wg.Wait()
		synctest.Wait()

		hung := make(chan struct{})
		h.setWrite(func(context.Context, string, []byte) error { <-hung; return nil })
		start := time.Now()
		err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
		if !errors.Is(err, errProbeTimeout) || time.Since(start) > defaultProbeTimeout+time.Second {
			t.Fatalf("Preflight over a hung write = %v after %v, want the timeout within %v", err, time.Since(start), defaultProbeTimeout+time.Second)
		}
		writes := h.probeWrites(h.root)
		start = time.Now()
		err = h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
		if !errors.Is(err, errProbeTimeout) || time.Since(start) != 0 || h.probeWrites(h.root) != writes {
			t.Errorf("second preflight while hung = %v after %v with %d new writes, want the timeout at once and no write",
				err, time.Since(start), h.probeWrites(h.root)-writes)
		}
		close(hung)
		synctest.Wait()
		if _, ok := h.w.Blocked(filepath.Join(h.root, "a.mkv")); !ok {
			t.Error("a late successful completion cleared the timed-out folder")
		}
		if n := len(h.logs.at(slog.LevelError)); n != 1 {
			t.Errorf("ERROR records = %d, want 1", n)
		}
		if n := h.statCalls(h.root); n != 0 {
			t.Errorf("Stat calls for a bound root = %d, want 0", n)
		}
	})
}

func TestProbe_callers_across_rebinds_join_the_running_probe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		release := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-release
			}
			return nil
		})
		results := make(chan error, 3)
		go func() { results <- h.w.Preflight(t.Context(), PreflightRequest{Roots: true}) }()
		synctest.Wait()
		for range 2 {
			h.w.Bind([]string{h.root}, h.validate)
			go func() { results <- h.w.Preflight(t.Context(), PreflightRequest{Roots: true}) }()
			synctest.Wait()
		}
		start := time.Now()
		close(release)
		for i := range 3 {
			if err := <-results; err != nil {
				t.Errorf("preflight %d = %v, want nil", i+1, err)
			}
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("preflights returned %v after the probe did, want at once", elapsed)
		}
		if n := h.probeWrites(h.root); n != 1 {
			t.Errorf("probe writes for three preflights across two rebinds = %d, want 1", n)
		}
	})
}

func TestProbe_a_rebind_starts_no_second_write_into_a_hung_folder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		hung := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-hung
			}
			return nil
		})
		if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true}); !errors.Is(err, errProbeTimeout) {
			t.Fatalf("Setup: Preflight over a hung write = %v, want the timeout", err)
		}
		for i := range 3 {
			h.w.Bind([]string{h.root}, h.validate)
			start := time.Now()
			err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
			if !errors.Is(err, errProbeTimeout) || time.Since(start) > defaultProbeTimeout {
				t.Errorf("preflight after rebind %d = %v after %v, want the timeout within %v", i+1, err, time.Since(start), defaultProbeTimeout)
			}
		}
		if n := h.probeWrites(h.root); n != 1 {
			t.Errorf("probe writes into a hung folder across 3 rebinds = %d, want 1", n)
		}
		if _, ok := h.w.Blocked(filepath.Join(h.root, "a.mkv")); !ok {
			t.Error("the hung root is not marked under the current binding")
		}

		close(hung)
		synctest.Wait()
		h.setWrite(nil)
		if err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true}); err != nil {
			t.Errorf("preflight once the hung write returned = %v, want nil", err)
		}
		if n := h.probeWrites(h.root); n != 2 {
			t.Errorf("probe writes after the hung write returned = %d, want 2", n)
		}
	})
}

func TestProbe_a_hung_probe_marks_its_folder_again_after_a_write_clears_it(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		hung := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-hung
			}
			return nil
		})
		folders := PreflightRequest{Folders: []string{show}}
		if err := h.w.Preflight(t.Context(), folders); !errors.Is(err, errProbeTimeout) {
			t.Fatalf("Setup: Preflight over a hung probe = %v, want the timeout", err)
		}
		if err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n")); err != nil {
			t.Fatalf("Setup: WriteFile = %v", err)
		}
		if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
			t.Fatal("Setup: the successful write did not clear the folder")
		}

		err := h.w.Preflight(t.Context(), folders)
		if !errors.Is(err, errProbeTimeout) {
			t.Errorf("Preflight while the probe still hangs = %v, want the timeout", err)
		}
		if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); !ok || !h.alerts.HasUndismissed(folderAlertPrefix+show) {
			t.Error("a preflight answered by the hung probe returned a fault the writer does not report")
		}
		if n := h.probeWrites(show); n != 1 {
			t.Errorf("probe writes = %d, want 1", n)
		}
		close(hung)
		synctest.Wait()
	})
}

func TestProbe_a_hung_validate_or_stat_is_a_timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		vblock := make(chan struct{})
		h.setValidate(func(context.Context, string) error { <-vblock; return nil })
		start := time.Now()
		err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
		if !errors.Is(err, errProbeTimeout) || time.Since(start) > defaultProbeTimeout+time.Second {
			t.Fatalf("Preflight over a hung validate = %v after %v", err, time.Since(start))
		}
		h.w.mu.Lock()
		b := h.w.bad[show]
		h.w.mu.Unlock()
		if b == nil || b.vetted {
			t.Fatalf("bad entry = %+v, want marked and unvetted", b)
		}
		close(vblock)
		synctest.Wait()

		writesAtValidate := -1
		h.setValidate(func(context.Context, string) error {
			writesAtValidate = h.probeWrites(show)
			return nil
		})
		before := h.probeWrites(show)
		_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
		if writesAtValidate != before {
			t.Errorf("validate ran after %d of the probe's writes, want before any", writesAtValidate-before)
		}

		season := h.dir(t, "tv/Show2")
		h.w.fail(season, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
		sblock := make(chan struct{})
		h.setStat(func(p string) (fs.FileInfo, error) {
			if p == season {
				<-sblock
			}
			return os.Stat(p)
		})
		start = time.Now()
		err = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{season}})
		if !errors.Is(err, errProbeTimeout) || time.Since(start) > defaultProbeTimeout+time.Second {
			t.Errorf("Preflight over a hung Stat = %v after %v", err, time.Since(start))
		}
		close(sblock)
		synctest.Wait()
	})
}

func TestDismissal_only_a_raising_caller_re_creates_the_alert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		h.setWrite(failUnder(show, erofs))
		_ = h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n"))
		source := folderAlertPrefix + show
		h.alerts.DismissBySource(source)

		if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); !errors.Is(err, ErrUnwritable) {
			t.Fatalf("poller-style preflight = %v, want ErrUnwritable", err)
		}
		if h.alerts.HasUndismissed(source) {
			t.Fatal("a preflight without Raise re-created the dismissed alert")
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- h.w.Run(ctx) }()
		before := h.writeErrors(t)
		probes := h.probeWrites(show)
		time.Sleep(defaultRecheckInterval + time.Second)
		synctest.Wait()
		if h.probeWrites(show) != probes+1 {
			t.Fatalf("Setup: recheck probes = %d, want 1", h.probeWrites(show)-probes)
		}
		if h.alerts.HasUndismissed(source) {
			t.Error("a recheck re-created the dismissed alert")
		}
		if after := h.writeErrors(t); after != before {
			t.Errorf("recheck moved subtitle_write_errors_total %v -> %v, want unchanged", before, after)
		}
		cancel()
		<-done

		if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}, Raise: true}); !errors.Is(err, ErrUnwritable) {
			t.Fatalf("scan-style preflight = %v, want ErrUnwritable", err)
		}
		if !h.alerts.HasUndismissed(source) {
			t.Fatal("a Raise preflight did not re-create the alert")
		}
		for _, req := range []PreflightRequest{{Folders: []string{show}}, {Folders: []string{show}, Raise: true}} {
			_ = h.w.Preflight(t.Context(), req)
		}
		n := 0
		for _, s := range h.alertSources() {
			if s == source {
				n++
			}
		}
		if n != 1 {
			t.Errorf("undismissed alerts for %s = %d, want 1 (refreshed, not duplicated)", source, n)
		}
	})
}

func TestPreflight_Folders_probes_related_known_bad_folders(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	season := h.dir(t, "tv/Show/Season 01")
	cases := []struct {
		name string
		bad  string
	}{
		{name: "the_folder_itself", bad: show},
		{name: "a_bad_root_above_it", bad: h.root},
		{name: "a_bad_season_below_it", bad: season},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h.setWrite(func(_ context.Context, p string, _ []byte) error {
				if filepath.Dir(p) == tc.bad {
					return erofs(p)
				}
				return nil
			})
			h.w.fail(tc.bad, faultReport{Op: opProbe, Err: syscall.EROFS}, true, false)
			before := h.probeWrites(tc.bad)
			err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
			var uerr *UnwritableError
			if !errors.As(err, &uerr) || uerr.Folder != tc.bad {
				t.Errorf("Preflight(%s) with %s bad = %v, want an *UnwritableError naming it", show, tc.bad, err)
			}
			if h.probeWrites(tc.bad) != before+1 {
				t.Errorf("the bad folder was not re-probed")
			}
			h.setWrite(nil)
			_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
		})
	}
}

func TestPreflight_Folders_reuses_a_passing_probe_for_the_TTL(t *testing.T) {
	clk := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	h := newHarness(t, func(c *Config) { c.Now = clk.Now })
	show := h.dir(t, "tv/Show")
	folders := PreflightRequest{Folders: []string{show}}

	_ = h.w.Preflight(t.Context(), folders)
	clk.advance(10 * time.Second)
	_ = h.w.Preflight(t.Context(), folders)
	if n := h.probeWrites(show); n != 1 {
		t.Errorf("Folders preflights 10s apart probed %d times, want 1", n)
	}
	clk.advance(61 * time.Second)
	_ = h.w.Preflight(t.Context(), folders)
	if n := h.probeWrites(show); n != 2 {
		t.Errorf("Folders preflights 71s apart probed %d times, want 2", n)
	}

	_ = h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
	clk.advance(10 * time.Second)
	_ = h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
	if n := h.probeWrites(h.root); n != 2 {
		t.Errorf("Roots preflights 10s apart probed %d times, want 2", n)
	}

	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })
	clk.advance(61 * time.Second)
	_ = h.w.Preflight(t.Context(), folders)
	h.setWrite(nil)
	clk.advance(10 * time.Second)
	before := h.probeWrites(show)
	_ = h.w.Preflight(t.Context(), folders)
	if h.probeWrites(show) != before+1 {
		t.Error("a failed probe was cached")
	}
}

func TestPreflight_vanished_folders_and_a_missing_root(t *testing.T) {
	h := newHarness(t)
	cfg := &config.Config{MediaRootDirs: []string{h.root}}
	h.w.Bind([]string{h.root}, cfg.ValidatePath)
	gone := filepath.Join(h.root, "tv", "Gone")

	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{gone}}); err != nil {
		t.Fatalf("Preflight(missing folder) = %v, want nil", err)
	}
	if n := h.probeWrites(""); n != 0 {
		t.Errorf("probe writes for a missing folder = %d, want 0", n)
	}

	h.w.fail(gone, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
	infos := len(h.logs.withMsg("media folder writable again"))
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{gone}}); err != nil {
		t.Fatalf("Preflight(vanished bad folder) = %v, want nil", err)
	}
	if h.alerts.HasUndismissed(folderAlertPrefix+gone) || len(h.logs.withMsg("media folder gone; dropping its write state")) != 1 ||
		len(h.logs.withMsg("media folder writable again")) != infos {
		t.Error("a vanished bad folder was not forgotten with its alert dismissed, one DEBUG and no INFO")
	}
	h.w.fail(gone, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
	_ = h.w.Preflight(t.Context(), PreflightRequest{Roots: true, RecheckBad: true})
	if _, ok := h.w.Blocked(filepath.Join(gone, "a.mkv")); ok {
		t.Error("RecheckBad kept a vanished bad folder")
	}

	missing := filepath.Join(t.TempDir(), "absent")
	h.w.Bind([]string{missing}, h.validate)
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		if _, err := os.Stat(filepath.Dir(p)); err != nil {
			return &atomicfile.WriteError{Err: &fs.PathError{Op: "open", Path: p, Err: syscall.ENOENT}, Phase: atomicfile.PhaseTempCreate}
		}
		return nil
	})
	errsBefore := len(h.logs.at(slog.LevelError))
	validates := h.totalValidates()
	err := h.w.Preflight(t.Context(), PreflightRequest{Roots: true})
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || uerr.Folder != missing {
		t.Fatalf("Roots preflight with a missing root = %v, want an *UnwritableError naming it", err)
	}
	if h.statCalls(missing) != 0 || h.totalValidates() != validates {
		t.Error("a bound root was stat-ed or validated")
	}
	if len(h.logs.at(slog.LevelError)) != errsBefore+1 || len(h.logs.withMsg("media folder write test inconclusive")) != 0 {
		t.Error("want exactly one new ERROR and no inconclusive WARN for a missing root")
	}
	if !h.alerts.HasUndismissed(folderAlertPrefix+missing) || h.gauge(t, missing) != 1 {
		t.Error("a missing root raised no alert or left the gauge at 0")
	}
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{missing}}); !errors.Is(err, ErrUnwritable) {
		t.Errorf("Folders preflight naming the missing root = %v, want ErrUnwritable", err)
	}
	if h.totalValidates() != validates || len(h.logs.at(slog.LevelError)) != errsBefore+1 {
		t.Error("the missing root named as a folder was validated or logged a second ERROR")
	}
}

func TestPreflight_a_cancelled_context_marks_nothing(t *testing.T) {
	h := newHarness(t)
	h.setWrite(func(_ context.Context, p string, _ []byte) error { return erofs(p) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.w.Preflight(ctx, PreflightRequest{Roots: true, Raise: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Preflight(cancelled) = %v, want context.Canceled", err)
	}
	if _, ok := h.w.Blocked(filepath.Join(h.root, "a.mkv")); ok || len(h.alertSources()) != 0 || len(h.logs.at(slog.LevelError)) != 0 {
		t.Error("a cancelled preflight marked, alerted or logged an ERROR")
	}
}

func TestRun_clears_a_recovered_folder_without_a_scan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- h.w.Run(ctx) }()

		time.Sleep(30 * time.Minute)
		synctest.Wait()
		if n := h.writeCount(); n != 0 {
			t.Fatalf("writes with an empty bad set = %d, want 0", n)
		}

		h.setWrite(failUnder(show, erofs))
		_ = h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n"))
		h.setWrite(nil)
		time.Sleep(defaultRecheckInterval + time.Second)
		synctest.Wait()
		if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
			t.Fatal("the recovered folder was not cleared by the recheck")
		}
		if len(h.logs.withMsg("media folder writable again")) != 1 || h.alerts.HasUndismissed(folderAlertPrefix+show) || h.gauge(t, h.root) != 0 {
			t.Error("the recheck clear left no INFO, an alert or the gauge at 1")
		}
		writes := h.writeCount()
		time.Sleep(30 * time.Minute)
		synctest.Wait()
		if h.writeCount() != writes {
			t.Errorf("writes after the clear = %d, want 0", h.writeCount()-writes)
		}

		outside := t.TempDir()
		h.setWrite(failUnder(outside, erofs))
		err := h.w.WriteFile(t.Context(), filepath.Join(outside, "a.srt"), []byte("1\n"))
		if _, ok := errors.AsType[*UnwritableError](err); !ok {
			t.Fatalf("WriteFile outside every root = %v, want an *UnwritableError", err)
		}
		if u := h.gauge(t, unconfiguredRoot); u != 1 {
			t.Fatalf("unconfigured gauge = %v, want 1", u)
		}
		h.setWrite(nil)
		time.Sleep(defaultRecheckInterval + time.Second)
		synctest.Wait()
		if _, ok := h.w.Blocked(filepath.Join(outside, "a.mkv")); ok {
			t.Error("the recheck did not clear an unconfigured folder")
		}

		a, b := h.dir(t, "tv/A"), h.dir(t, "tv/B")
		h.w.fail(a, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
		h.w.fail(b, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
		sblock := make(chan struct{})
		h.setStat(func(p string) (fs.FileInfo, error) {
			if p == a {
				<-sblock
			}
			return os.Stat(p)
		})
		time.Sleep(defaultRecheckInterval + defaultProbeTimeout + time.Second)
		synctest.Wait()
		if _, ok := h.w.Blocked(filepath.Join(b, "x.mkv")); ok {
			t.Error("a hung sibling stopped B from clearing")
		}
		time.Sleep(3 * (defaultRecheckInterval + defaultProbeTimeout))
		synctest.Wait()
		if n := h.statCalls(a); n != 1 {
			t.Errorf("Stat calls for the hung folder over four rounds = %d, want 1", n)
		}
		close(sblock)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v, want context.Canceled", err)
		}
		synctest.Wait()
	})
}

func TestPreflight_probes_only_folders_inside_a_bound_root(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{"/etc"}}); err != nil {
		t.Errorf("Preflight(/etc) = %v, want nil", err)
	}
	h.w.fail("/etc/x", faultReport{Op: opWrite, Err: syscall.EIO}, true, false)
	_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{"/etc"}})
	if h.writeCount() != 0 || h.removeCount() != 0 || h.totalValidates() != 0 || h.statCalls("/etc") != 0 {
		t.Errorf("out-of-root preflights made %d writes, %d removes, %d validates, %d stats; want none",
			h.writeCount(), h.removeCount(), h.totalValidates(), h.statCalls("/etc"))
	}

	h.w.Bind(nil, h.validate)
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); err != nil || h.writeCount() != 0 {
		t.Errorf("Preflight with no roots = %v and %d writes, want nil and 0", err, h.writeCount())
	}

	cfg := &config.Config{MediaRootDirs: []string{h.root}}
	h.w.Bind([]string{h.root}, cfg.ValidatePath)
	link := filepath.Join(h.root, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{link}})
	if h.writeCount() != 0 {
		t.Errorf("a symlinked folder escaping the root got %d writes, want 0", h.writeCount())
	}
}

func TestWriteFile_a_confirmation_waits_for_a_smaller_call(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		gate := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, data []byte) error {
			if isProbeName(p) && len(data) == len(probeLine) {
				<-gate
			}
			if len(data) >= 64 {
				return &fs.PathError{Op: "write", Path: p, Err: syscall.ENOSPC}
			}
			return nil
		})
		go func() { _ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}) }()
		synctest.Wait()
		var err error
		done := make(chan struct{})
		go func() { err = h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), make([]byte, 100)); close(done) }()
		synctest.Wait()
		close(gate)
		<-done
		if sizes := h.probeSizes(); len(sizes) != 2 || sizes[0] != len(probeLine) || sizes[1] != 100 {
			t.Errorf("probe sizes = %v, want [20 100]", sizes)
		}
		if !errors.Is(err, ErrUnwritable) {
			t.Errorf("WriteFile = %v, want ErrUnwritable", err)
		}
	})
}

func TestWriteFile_a_confirmation_starts_no_second_write_into_a_hung_folder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		hung := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-hung
				return nil
			}
			return &fs.PathError{Op: "write", Path: p, Err: syscall.ENOSPC}
		})
		if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); !errors.Is(err, errProbeTimeout) {
			t.Fatalf("Setup: Preflight over a hung write = %v, want the timeout", err)
		}
		err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), make([]byte, 100))
		if !errors.Is(err, ErrUnwritable) {
			t.Errorf("WriteFile into the hung folder = %v, want ErrUnwritable", err)
		}
		if sizes := h.probeSizes(); len(sizes) != 1 {
			t.Errorf("probe sizes = %v, want one probe write while the first is hung", sizes)
		}
		close(hung)
		synctest.Wait()
	})
}

func TestWriteFile_a_confirmation_behind_a_hung_smaller_probe_waits_one_timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		hung := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-hung
				return nil
			}
			return &fs.PathError{Op: "write", Path: p, Err: syscall.ENOSPC}
		})
		go func() { _ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}) }()
		synctest.Wait()
		start := time.Now()
		err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), make([]byte, 100))
		if elapsed := time.Since(start); !errors.Is(err, ErrUnwritable) || elapsed > defaultProbeTimeout {
			t.Errorf("WriteFile behind a hung 20-byte probe = %v after %v, want ErrUnwritable within %v", err, elapsed, defaultProbeTimeout)
		}
		if sizes := h.probeSizes(); len(sizes) != 1 || sizes[0] != len(probeLine) {
			t.Errorf("probe sizes = %v, want only the hung [%d]", sizes, len(probeLine))
		}
		if msg := h.alertMessage(folderAlertPrefix + show); !strings.Contains(msg, "no space left") {
			t.Errorf("folder alert = %q, want it to name the failed write's error", msg)
		}
		close(hung)
		synctest.Wait()
	})
}

// A confirmation that waited for a slow but passing smaller probe gets its
// own full ProbeTimeout, so its passing probe clears the folder.
func TestWriteFile_a_confirmation_behind_a_slow_smaller_probe_gets_its_own_timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		h.setWrite(func(_ context.Context, p string, data []byte) error {
			switch {
			case !isProbeName(p):
				return erofs(p)
			case len(data) == len(probeLine):
				time.Sleep(defaultProbeTimeout - 100*time.Millisecond)
			default:
				time.Sleep(time.Second)
			}
			return nil
		})
		go func() { _ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}) }()
		time.Sleep(50 * time.Millisecond)
		target := filepath.Join(show, "e.srt")
		err := h.w.WriteFile(t.Context(), target, make([]byte, 100))
		if !errors.Is(err, syscall.EROFS) || errors.Is(err, ErrUnwritable) {
			t.Errorf("WriteFile(%q) behind a 9.9s passing probe, own probe passing in 1s = %v, want the write's own EROFS", target, err)
		}
		if folder, blocked := h.w.Blocked(target); blocked {
			t.Errorf("Blocked(%q) = %q, want the folder clear", target, folder)
		}
		if sizes := h.probeSizes(); !slices.Equal(sizes, []int{len(probeLine), 100}) {
			t.Errorf("probe sizes = %v, want [%d 100]", sizes, len(probeLine))
		}
		// A probe still running would end the bubble in a deadlock panic.
		time.Sleep(time.Second)
		synctest.Wait()
	})
}

func TestProbe_own_deadline_and_not_proven(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		h.setWrite(func(ctx context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		})
		ctx1, cancel1 := context.WithCancel(t.Context())
		first := make(chan error, 1)
		go func() { first <- h.w.Preflight(ctx1, PreflightRequest{Folders: []string{show}}) }()
		time.Sleep(time.Second)
		cancel1()
		if err1 := <-first; !errors.Is(err1, context.Canceled) {
			t.Fatalf("cancelled waiter = %v, want context.Canceled", err1)
		}
		time.Sleep(4 * time.Second)
		start := time.Now()
		err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
		if !errors.Is(err, errProbeTimeout) || time.Since(start) != 5*time.Second {
			t.Fatalf("late waiter = %v after %v, want the timeout at the probe's own deadline (5s)", err, time.Since(start))
		}
		if len(h.logs.at(slog.LevelError)) != 1 || len(h.alertSources()) != 1 || h.gauge(t, h.root) != 1 {
			t.Error("the probe deadline did not mark the folder once")
		}
	})

	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		if isProbeName(p) {
			return &atomicfile.WriteError{Err: atomicfile.ErrRaced, Phase: atomicfile.PhaseTempCreate}
		}
		return nil
	})
	errsBefore := h.writeErrors(t)
	err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || uerr.Op != opProbe || !errors.Is(err, ErrUnwritable) {
		t.Fatalf("not-proven preflight = %v, want an *UnwritableError op probe", err)
	}
	if got := h.writeErrors(t) - errsBefore; got != 1 {
		t.Errorf("not-proven preflight moved subtitle_write_errors_total by %v, want 1", got)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok || len(h.alertSources()) != 0 || h.gauge(t, h.root) != 0 {
		t.Error("a not-proven probe marked the folder, alerted or set the gauge")
	}
	if len(h.logs.at(slog.LevelWarn)) != 1 || len(h.logs.at(slog.LevelError)) != 0 {
		t.Error("want one WARN and no ERROR for a not-proven probe")
	}

	sub := filepath.Join(show, "a.srt")
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		if p == sub {
			return &fs.PathError{Op: "rename", Path: p, Err: syscall.EACCES}
		}
		return &atomicfile.WriteError{Err: atomicfile.ErrRaced, Phase: atomicfile.PhaseTempCreate}
	})
	errsBefore = h.writeErrors(t)
	if err := h.w.WriteFile(t.Context(), sub, []byte("1\n")); !errors.Is(err, syscall.EACCES) || errors.Is(err, ErrUnwritable) {
		t.Fatalf("WriteFile with a not-proven confirmation = %v, want the EACCES error without ErrUnwritable", err)
	}
	if got := h.writeErrors(t) - errsBefore; got != 1 {
		t.Errorf("a failed write whose confirmation is not proven moved subtitle_write_errors_total by %v, want 1", got)
	}
}

func TestPreflight_a_validate_refusal_is_a_skip(t *testing.T) {
	refuse := func(_ context.Context, p string) error { return fmt.Errorf("path %q: %w", p, config.ErrPathNotAllowed) }
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.setValidate(refuse)
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); err != nil {
		t.Fatalf("Preflight(refused) = %v, want nil", err)
	}
	if h.writeCount() != 0 || h.removeCount() != 0 || len(h.logs.at(slog.LevelError)) != 0 ||
		len(h.logs.at(slog.LevelWarn)) != 0 || len(h.alertSources()) != 0 {
		t.Error("a refusal wrote, removed, logged or alerted")
	}

	h.w.fail(show, faultReport{Op: opProbe, Err: errProbeTimeout}, false, false)
	_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
	if h.alerts.HasUndismissed(folderAlertPrefix+show) || len(h.logs.withMsg("media folder gone; dropping its write state")) != 1 || h.gauge(t, h.root) != 0 {
		t.Error("a refused unvetted bad folder was not forgotten")
	}

	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		h.setValidate(func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() })
		_ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
		synctest.Wait()
		h.w.mu.Lock()
		b := h.w.bad[show]
		h.w.mu.Unlock()
		if b == nil || b.vetted || len(h.logs.at(slog.LevelError)) != 1 {
			t.Errorf("validate past the deadline: entry %+v, %d ERROR; want marked unvetted with one ERROR", b, len(h.logs.at(slog.LevelError)))
		}
	})
}

func TestPreflight_a_vetted_bad_folder_is_not_revalidated(t *testing.T) {
	h := newHarness(t)
	show := h.dir(t, "tv/Show")
	h.w.fail(show, faultReport{Op: opProbe, Err: syscall.EIO}, true, false)
	h.setValidate(func(context.Context, string) error { return config.ErrPathNotAllowed })
	eio := &fs.PathError{Op: "stat", Path: show, Err: syscall.EIO}
	h.setStat(func(string) (fs.FileInfo, error) { return nil, eio })
	h.setWrite(func(_ context.Context, p string, _ []byte) error {
		return &fs.PathError{Op: "open", Path: p, Err: syscall.EIO}
	})

	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); !errors.Is(err, ErrUnwritable) {
		t.Fatalf("Preflight = %v, want ErrUnwritable", err)
	}
	if h.validateCalls(show) != 0 || h.statCalls(show) != 1 {
		t.Errorf("validate calls %d, stat calls %d; want 0 and 1", h.validateCalls(show), h.statCalls(show))
	}
	if !h.alerts.HasUndismissed(folderAlertPrefix+show) || len(h.logs.withMsg("media folder gone; dropping its write state")) != 0 {
		t.Error("a still-failing vetted folder lost its alert or was forgotten")
	}

	h.setStat(func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist })
	if err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}}); err != nil {
		t.Errorf("Preflight of a vanished vetted folder = %v, want nil", err)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); ok {
		t.Error("a vanished vetted folder was kept")
	}
}

func TestWriteFile_a_confirmation_joining_a_refused_call_marks_nothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		folder := h.dir(t, "tv/New")
		release := make(chan error)
		h.setValidate(func(context.Context, string) error { return <-release })
		sub := filepath.Join(folder, "e.en.srt")
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if p == sub {
				return &fs.PathError{Op: "rename", Path: p, Err: syscall.EACCES}
			}
			return nil
		})
		go func() { _ = h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{folder}}) }()
		synctest.Wait()
		var err error
		done := make(chan struct{})
		go func() { err = h.w.WriteFile(t.Context(), sub, make([]byte, 10)); close(done) }()
		synctest.Wait()
		release <- fmt.Errorf("path %q: %w", folder, config.ErrPathNotAllowed)
		<-done
		if !errors.Is(err, syscall.EACCES) || errors.Is(err, ErrUnwritable) {
			t.Fatalf("WriteFile = %v, want the EACCES error without ErrUnwritable", err)
		}
		if n := h.probeWrites(""); n != 0 {
			t.Errorf("probe writes = %d, want 0 (the joined call wrote nothing)", n)
		}
		if _, ok := h.w.Blocked(sub); ok || len(h.alertSources()) != 0 || h.gauge(t, h.root) != 0 {
			t.Error("the refused join marked the folder, alerted or set the gauge")
		}
		warns := h.logs.at(slog.LevelWarn)
		if len(warns) != 1 || warns[0].attrs["path"] != sub || len(h.logs.at(slog.LevelError)) != 0 {
			t.Errorf("WARN = %+v; want one naming the path and no ERROR", warns)
		}
	})
}
