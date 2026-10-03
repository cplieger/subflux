package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

func TestBind_during_a_write_confirmation_keeps_the_fault_loud(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t)
		show := h.dir(t, "tv/Show")
		release := make(chan struct{})
		h.setWrite(func(_ context.Context, p string, _ []byte) error {
			if isProbeName(p) {
				<-release
			}
			return erofs(p)
		})
		done := make(chan error, 1)
		go func() { done <- h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n")) }()
		synctest.Wait()
		h.w.Bind([]string{h.root}, h.validate)
		close(release)
		if err := <-done; !errors.Is(err, ErrUnwritable) {
			t.Fatalf("WriteFile with a Bind during its confirmation = %v, want ErrUnwritable", err)
		}
		if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); !ok {
			t.Error("Blocked = false, want the confirmed folder marked")
		}
		if !h.alerts.HasUndismissed(folderAlertPrefix + show) {
			t.Errorf("alerts = %v, want %s", h.alertSources(), folderAlertPrefix+show)
		}
		if n := len(h.logs.at(slog.LevelError)); n != 1 {
			t.Errorf("ERROR records = %d, want 1", n)
		}
		if g := h.gauge(t, h.root); g != 1 {
			t.Errorf("gauge = %v, want 1", g)
		}
	})
}

func TestBind_reattaches_bad_folders_to_the_new_roots(t *testing.T) {
	h := newHarness(t)
	nested := h.dir(t, "anime")
	show := h.dir(t, "anime/Show")
	h.setWrite(failUnder(show, erofs))
	if err := h.w.WriteFile(t.Context(), filepath.Join(show, "a.srt"), []byte("1\n")); !errors.Is(err, ErrUnwritable) {
		t.Fatalf("Setup: WriteFile = %v, want ErrUnwritable", err)
	}

	h.w.Bind([]string{h.root, nested}, h.validate)
	if g, n := h.gauge(t, h.root), h.gauge(t, nested); g != 0 || n != 1 {
		t.Errorf("after adding the nested root: parent gauge %v, nested gauge %v; want 0 and 1", g, n)
	}
	err := h.w.Preflight(t.Context(), PreflightRequest{Folders: []string{show}})
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || uerr.Root != nested {
		t.Errorf("Preflight(%s) = %v, want an *UnwritableError under the nested root %s", show, err, nested)
	}

	h.w.Bind([]string{h.root}, h.validate)
	if _, ok := h.series(t, `subflux_media_root_unwritable{root="`+nested+`"}`); ok {
		t.Error("the removed nested root's series is still exported")
	}
	if g := h.gauge(t, h.root); g != 1 {
		t.Errorf("parent gauge after removing the nested root = %v, want 1", g)
	}
	if _, ok := h.w.Blocked(filepath.Join(show, "a.mkv")); !ok || !h.alerts.HasUndismissed(folderAlertPrefix+show) {
		t.Error("a bad folder the parent root still contains was dropped with its alert")
	}
}

func TestBind_adopts_or_drops_a_folder_outside_every_root(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	folder := filepath.Join(outside, "Show")
	h.setWrite(failUnder(folder, erofs))
	err := h.w.WriteFile(t.Context(), filepath.Join(folder, "a.srt"), []byte("1\n"))
	var uerr *UnwritableError
	if !errors.As(err, &uerr) || uerr.Root != unconfiguredRoot {
		t.Fatalf("Setup: WriteFile outside every root = %v, want root %q", err, unconfiguredRoot)
	}

	h.w.Bind([]string{h.root, outside}, h.validate)
	if g, u := h.gauge(t, outside), h.gauge(t, unconfiguredRoot); g != 1 || u != 0 {
		t.Errorf("after a root came to contain it: root gauge %v, unconfigured gauge %v; want 1 and 0", g, u)
	}
	if _, ok := h.w.Blocked(filepath.Join(folder, "a.mkv")); !ok {
		t.Fatal("the adopted folder was dropped")
	}

	h.w.Bind([]string{h.root}, h.validate)
	if _, ok := h.w.Blocked(filepath.Join(folder, "a.mkv")); ok {
		t.Error("a folder no bound root contains survived the Bind")
	}
	if h.alerts.HasUndismissed(folderAlertPrefix + folder) {
		t.Error("the dropped folder's alert is still raised")
	}
	if g := h.gauge(t, unconfiguredRoot); g != 0 {
		t.Errorf("unconfigured gauge after the drop = %v, want 0", g)
	}
}

func TestBind_rebuilds_the_root_aggregates(t *testing.T) {
	h := newHarness(t)
	tv := h.dir(t, "tv")
	h.setWrite(failUnder(tv, erofs))
	for i := range maxFolderAlerts + 2 {
		f := h.dir(t, fmt.Sprintf("tv/Show%02d", i))
		_ = h.w.WriteFile(t.Context(), filepath.Join(f, "e.srt"), []byte("1\n"))
	}
	if !h.alerts.HasUndismissed(rootAlertPrefix + h.root) {
		t.Fatalf("Setup: alerts = %v, want the parent root's aggregate", h.alertSources())
	}

	h.w.Bind([]string{h.root, tv}, h.validate)
	if h.alerts.HasUndismissed(rootAlertPrefix + h.root) {
		t.Error("the parent root's aggregate is still raised with no bad folder left under it")
	}
	msg := h.alertMessage(rootAlertPrefix + tv)
	if want := fmt.Sprintf("%d folders under %s", maxFolderAlerts+2, tv); !strings.HasPrefix(msg, want) {
		t.Errorf("nested root aggregate = %q, want it to start %q", msg, want)
	}
}
