package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/server/activity"
)

func TestActivityLog_start_records_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "Full library scan", "scheduled")

	if id == "" {
		t.Fatal("start() returned empty ID")
	}

	entry, ok := al.Get(id)
	if !ok {
		t.Fatal("entry not found after start()")
	}
	if n := len(al.Entries()); n != 1 {
		t.Fatalf("entries count = %d, want 1", n)
	}
	if entry.Action != "Scan" {
		t.Errorf("entry.Action = %q, want %q", entry.Action, "Scan")
	}
	if entry.Detail != "Full library scan" {
		t.Errorf("entry.Detail = %q, want %q", entry.Detail, "Full library scan")
	}
	if entry.Done {
		t.Error("entry.Done = true, want false")
	}
	if entry.StartedAt.IsZero() {
		t.Error("entry.StartedAt is zero")
	}
}

func TestActivityLog_start_trims_to_maxItems(t *testing.T) {
	t.Parallel()
	al := activity.New(3)

	idA := al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")
	al.Start("C", "third", "scheduled")
	// Only completed entries are trim candidates (running entries survive
	// capacity pressure); complete A so the overflow start evicts it.
	al.End(idA)
	al.Start("D", "fourth", "scheduled")

	entries := al.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries count = %d, want 3 (trimmed)", len(entries))
	}
	// The completed oldest entry should have been trimmed.
	if entries[0].Action != "B" {
		t.Errorf("entries[0].Action = %q, want %q (oldest completed trimmed)", entries[0].Action, "B")
	}
}

func TestActivityLog_end_marks_done(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "scan 1", "scheduled")
	al.End(id)

	if n := len(al.Entries()); n != 1 {
		t.Fatalf("entries count = %d, want 1", n)
	}
	if e, _ := al.Get(id); !e.Done {
		t.Errorf("entry %q should be done after end()", id)
	}
}

func TestActivityLog_end_only_marks_matching_id(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id1 := al.Start("Scan", "scan 1", "scheduled")
	id2 := al.Start("Upgrade", "upgrade 1", "scheduled")

	al.End(id1)

	if e, _ := al.Get(id1); !e.Done {
		t.Errorf("entry %q should be done", id1)
	}
	if e, _ := al.Get(id2); e.Done {
		t.Errorf("entry %q should not be done", id2)
	}
}

func TestActivityLog_end_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "scan 1", "scheduled")

	// Should not panic or modify anything.
	al.End("nonexistent-id")

	if al.Entries()[0].Done {
		t.Error("entry should not be marked done by nonexistent ID")
	}
}

func TestAlertLog_record_adds_alert(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.Record("sonarr", "Search failed for Series X")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1", len(al.AlertsUnsafe()))
	}
	alert := al.AlertsUnsafe()[0]
	if alert.Source != "sonarr" {
		t.Errorf("alert.Source = %q, want %q", alert.Source, "sonarr")
	}
	if alert.Message != "Search failed for Series X" {
		t.Errorf("alert.Message = %q, want %q", alert.Message, "Search failed for Series X")
	}
	if alert.Level != "error" {
		t.Errorf("alert.Level = %q, want %q", alert.Level, "error")
	}
	if alert.Time.IsZero() {
		t.Error("alert.Time is zero")
	}
}

func TestAlertLog_record_trims_to_max(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(2)

	al.Record("a", "first")
	al.Record("b", "second")
	al.Record("c", "third")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 2 {
		t.Fatalf("alerts count = %d, want 2 (trimmed)", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Source != "b" {
		t.Errorf("alerts[0].Source = %q, want %q (oldest trimmed)", al.AlertsUnsafe()[0].Source, "b")
	}
}

func TestAlertLog_record_multiple_sources(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(100)

	al.Record("sonarr", "error 1")
	al.Record("radarr", "error 2")
	al.Record("config", "error 3")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 3 {
		t.Fatalf("alerts count = %d, want 3", len(al.AlertsUnsafe()))
	}
}

// --- visibleAlerts ---

func TestVisibleAlerts_excludes_expired_transient(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	// Add a transient alert that expired (older than default TTL).
	al.Lock()
	al.AppendAlert(activity.Alert{
		ID: 999, Level: "error", Source: "old", Message: "old error",
		Kind: activity.AlertTransient, Time: time.Now().Add(-2 * time.Hour),
	})
	al.Unlock()

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (expired transient excluded)",
			len(visible))
	}
}

func TestVisibleAlerts_includes_persistent_regardless_of_age(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	// Add a persistent alert that is very old.
	al.Lock()
	al.AppendAlert(activity.Alert{
		ID: 998, Level: "error", Source: "startup", Message: "old persistent",
		Kind: activity.AlertPersistent, Time: time.Now().Add(-72 * time.Hour),
	})
	al.Unlock()

	visible := al.VisibleAlerts()
	if len(visible) != 1 {
		t.Fatalf("visibleAlerts() returned %d alerts, want 1 (persistent always visible)",
			len(visible))
	}
	if visible[0].Kind != activity.AlertPersistent {
		t.Errorf("visible[0].Kind = %q, want %q", visible[0].Kind, activity.AlertPersistent)
	}
}

func TestVisibleAlerts_excludes_dismissed(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.Record("sonarr", "recent error")
	al.RLock()
	id := al.AlertsUnsafe()[0].ID
	al.RUnlock()
	al.Dismiss(id)

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (dismissed excluded)",
			len(visible))
	}
}

func TestVisibleAlerts_empty_returns_empty_slice(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	visible := al.VisibleAlerts()
	if visible == nil {
		t.Fatal("visibleAlerts() returned nil, want non-nil empty slice")
	}
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0", len(visible))
	}
}

func TestVisibleAlerts_mixed_types_and_states(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	// 1. Recent transient (visible).
	al.Record("sonarr", "recent error")

	// 2. Old transient (expired, hidden).
	al.Lock()
	al.AppendAlert(activity.Alert{
		ID: 997, Level: "error", Source: "old-transient", Message: "expired",
		Kind: activity.AlertTransient, Time: time.Now().Add(-48 * time.Hour),
	})
	al.Unlock()

	// 3. Old persistent (visible regardless of age).
	al.Lock()
	al.AppendAlert(activity.Alert{
		ID: 996, Level: "error", Source: "startup", Message: "persistent",
		Kind: activity.AlertPersistent, Time: time.Now().Add(-72 * time.Hour),
	})
	al.Unlock()

	// 4. Dismissed recent transient (hidden).
	al.Record("radarr", "dismissed error")
	al.RLock()
	dismissID := al.AlertsUnsafe()[len(al.AlertsUnsafe())-1].ID
	al.RUnlock()
	al.Dismiss(dismissID)

	visible := al.VisibleAlerts()
	if len(visible) != 2 {
		t.Fatalf("visibleAlerts() returned %d alerts, want 2 (recent transient + old persistent)",
			len(visible))
	}

	sources := map[string]bool{}
	for _, a := range visible {
		sources[a.Source] = true
	}
	if !sources["sonarr"] {
		t.Error("expected recent transient alert from 'sonarr' to be visible")
	}
	if !sources["startup"] {
		t.Error("expected old persistent alert from 'startup' to be visible")
	}
}

// --- readBounded ---

func TestReadBounded(t *testing.T) {
	t.Parallel()

	t.Run("reads file within limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := atomicfile.ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("atomicfile.ReadBounded(%q, 1024) error: %v", path, err)
		}
		if string(got) != "hello world" {
			t.Errorf("atomicfile.ReadBounded(%q, 1024) = %q, want %q", path, got, "hello world")
		}
	})

	t.Run("rejects file over limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.txt")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := atomicfile.ReadBounded(context.Background(), path, 50)
		if err == nil {
			t.Fatal("atomicfile.ReadBounded() expected error for oversized file, got nil")
		}
		if !strings.Contains(err.Error(), "file too large") {
			t.Errorf("atomicfile.ReadBounded() error = %q, want to contain %q", err.Error(), "file too large")
		}
	})

	t.Run("file exactly at limit is accepted", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "exact.txt")
		content := strings.Repeat("x", 50)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := atomicfile.ReadBounded(context.Background(), path, 50)
		if err != nil {
			t.Fatalf("atomicfile.ReadBounded(%q, 50) error: %v", path, err)
		}
		if string(got) != content {
			t.Errorf("atomicfile.ReadBounded(%q, 50) = %q, want %q", path, got, content)
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := atomicfile.ReadBounded(context.Background(), "/nonexistent/path/file.txt", 1024)
		if err == nil {
			t.Fatal("atomicfile.ReadBounded(nonexistent) expected error, got nil")
		}
	})

	t.Run("empty file returns empty bytes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := atomicfile.ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("atomicfile.ReadBounded(%q, 1024) error: %v", path, err)
		}
		if len(got) != 0 {
			t.Errorf("atomicfile.ReadBounded(empty) = %q, want empty", got)
		}
	})
}

// The mergeSecrets, stripYAMLComment, isRedactedPlaceholder,
// findClosingQuote, and atomicWriteConfig tests formerly in this file
// moved to internal/server/confighandlers/secrets_test.go, next to the
// production code they exercise.
