package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/fsutil"
	"github.com/cplieger/subflux/internal/server/activity"
)

func TestActivityLog_start_records_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "Full library scan", "scheduled")

	if id == "" {
		t.Fatal("start() returned empty ID")
	}

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Fatalf("entries count = %d, want 1", len(al.EntriesUnsafe()))
	}
	entry := al.EntriesUnsafe()[0]
	if entry.ID != id {
		t.Errorf("entry.ID = %q, want %q", entry.ID, id)
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

	al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")
	al.Start("C", "third", "scheduled")
	al.Start("D", "fourth", "scheduled")

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 3 {
		t.Fatalf("entries count = %d, want 3 (trimmed)", len(al.EntriesUnsafe()))
	}
	// First entry should have been trimmed.
	if al.EntriesUnsafe()[0].Action != "B" {
		t.Errorf("entries[0].Action = %q, want %q (oldest trimmed)", al.EntriesUnsafe()[0].Action, "B")
	}
}

func TestActivityLog_end_marks_done(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "scan 1", "scheduled")
	al.End(id)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Fatalf("entries count = %d, want 1", len(al.EntriesUnsafe()))
	}
	if !al.EntriesUnsafe()[0].Done {
		t.Errorf("entry %q should be done after end()", id)
	}
}

func TestActivityLog_end_only_marks_matching_id(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	// Manually insert entries with known distinct IDs to test
	// that end() only marks the matching entry.
	al.Lock()
	al.AppendEntry(activity.Entry{ID: "id-1", Action: "Scan", Detail: "scan 1"})
	al.AppendEntry(activity.Entry{ID: "id-2", Action: "Upgrade", Detail: "upgrade 1"})
	al.Unlock()

	al.End("id-1")

	al.RLock()
	defer al.RUnlock()

	for _, e := range al.EntriesUnsafe() {
		if e.ID == "id-1" && !e.Done {
			t.Error("entry id-1 should be done")
		}
		if e.ID == "id-2" && e.Done {
			t.Error("entry id-2 should not be done")
		}
	}
}

func TestActivityLog_end_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "scan 1", "scheduled")

	// Should not panic or modify anything.
	al.End("nonexistent-id")

	al.RLock()
	defer al.RUnlock()

	if al.EntriesUnsafe()[0].Done {
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
		got, err := fsutil.ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("fsutil.ReadBounded(%q, 1024) error: %v", path, err)
		}
		if string(got) != "hello world" {
			t.Errorf("fsutil.ReadBounded(%q, 1024) = %q, want %q", path, got, "hello world")
		}
	})

	t.Run("rejects file over limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "big.txt")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := fsutil.ReadBounded(context.Background(), path, 50)
		if err == nil {
			t.Fatal("fsutil.ReadBounded() expected error for oversized file, got nil")
		}
		if !strings.Contains(err.Error(), "file too large") {
			t.Errorf("fsutil.ReadBounded() error = %q, want to contain %q", err.Error(), "file too large")
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
		got, err := fsutil.ReadBounded(context.Background(), path, 50)
		if err != nil {
			t.Fatalf("fsutil.ReadBounded(%q, 50) error: %v", path, err)
		}
		if string(got) != content {
			t.Errorf("fsutil.ReadBounded(%q, 50) = %q, want %q", path, got, content)
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := fsutil.ReadBounded(context.Background(), "/nonexistent/path/file.txt", 1024)
		if err == nil {
			t.Fatal("fsutil.ReadBounded(nonexistent) expected error, got nil")
		}
	})

	t.Run("empty file returns empty bytes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := fsutil.ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("fsutil.ReadBounded(%q, 1024) error: %v", path, err)
		}
		if len(got) != 0 {
			t.Errorf("fsutil.ReadBounded(empty) = %q, want empty", got)
		}
	})
}

// --- mergeSecrets ---

func TestMergeSecrets(t *testing.T) {
	t.Run("merges empty secret from existing config", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("sonarr:\n  api_key: \"\"\n"), existingPath)
		if !strings.Contains(string(got), "real-secret-key") {
			t.Errorf("mergeSecrets() = %q, want to contain %q", string(got), "real-secret-key")
		}
	})

	t.Run("preserves non-empty secret in new config", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: old-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("sonarr:\n  api_key: new-key\n"), existingPath)
		if !strings.Contains(string(got), "new-key") {
			t.Errorf("mergeSecrets() = %q, want to contain %q", string(got), "new-key")
		}
		if strings.Contains(string(got), "old-key") {
			t.Errorf("mergeSecrets() = %q, should not contain old key", string(got))
		}
	})

	t.Run("returns newData when no existing file", func(t *testing.T) {
		newData := []byte("sonarr:\n  api_key: \"\"\n")
		got := mergeSecrets(newData, "/nonexistent/config.yaml")
		if !bytes.Equal(got, newData) {
			t.Errorf("mergeSecrets(no existing) = %q, want %q", string(got), string(newData))
		}
	})

	t.Run("returns newData when existing has no secrets", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  url: http://sonarr:8989\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		newData := []byte("sonarr:\n  api_key: \"\"\n")
		got := mergeSecrets(newData, existingPath)
		if !bytes.Equal(got, newData) {
			t.Errorf("mergeSecrets(no secrets in existing) = %q, want %q", string(got), string(newData))
		}
	})

	t.Run("merges multiple secrets from different sections", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: sonarr-key\nradarr:\n  api_key: radarr-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("sonarr:\n  api_key: \"\"\nradarr:\n  api_key: \"\"\n"), existingPath)
		if !strings.Contains(string(got), "sonarr-key") {
			t.Errorf("mergeSecrets() missing sonarr key in %q", string(got))
		}
		if !strings.Contains(string(got), "radarr-key") {
			t.Errorf("mergeSecrets() missing radarr key in %q", string(got))
		}
	})

	t.Run("bare key colon without value is not matched", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		newData := []byte("sonarr:\n  api_key: \n")
		got := mergeSecrets(newData, existingPath)
		if !bytes.Equal(got, newData) {
			t.Errorf("mergeSecrets(bare key) = %q, want unchanged %q", string(got), string(newData))
		}
	})

	t.Run("merges single-quoted empty value", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("sonarr:\n  api_key: ''\n"), existingPath)
		if !strings.Contains(string(got), "real-key") {
			t.Errorf("mergeSecrets(single-quoted empty) = %q, want to contain %q", string(got), "real-key")
		}
	})

	t.Run("merges password key", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("providers:\n  os:\n    password: hunter2\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("providers:\n  os:\n    password: \"\"\n"), existingPath)
		if !strings.Contains(string(got), "hunter2") {
			t.Errorf("mergeSecrets(password) = %q, want to contain %q", string(got), "hunter2")
		}
	})

	t.Run("preserves indentation", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("providers:\n  os:\n    settings:\n      api_key: deep-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := mergeSecrets([]byte("providers:\n  os:\n    settings:\n      api_key: \"\"\n"), existingPath)
		if !strings.Contains(string(got), "      api_key: deep-key") {
			t.Errorf("mergeSecrets() indentation wrong in %q", string(got))
		}
	})
}

// --- stripYAMLComment ---

func TestStripYAMLComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty input", "", ""},
		{"unquoted no comment", "my-value", "my-value"},
		{"unquoted with comment", "my-value # comment", "my-value"},
		{"unquoted hash without space prefix", "my-value#notcomment", "my-value#notcomment"},
		{"double-quoted no comment", `"my-value"`, `"my-value"`},
		{"double-quoted with comment after", `"my-value" # comment`, `"my-value"`},
		{"double-quoted with escaped quote", `"my-\"value"`, `"my-\"value"`},
		{"double-quoted with escaped quote and comment", `"my-\"value" # comment`, `"my-\"value"`},
		{"single-quoted no comment", "'my-value'", "'my-value'"},
		{"single-quoted with comment after", "'my-value' # comment", "'my-value'"},
		{"single-quoted backslash not escape", "'my-\\'value'", "'my-\\'value'"},
		{"unterminated double quote", `"no-close`, `"no-close`},
		{"unterminated single quote", "'no-close", "'no-close"},
		{"double-quoted contains space-hash", `"abc #123"`, `"abc #123"`},
		{"double-quoted contains space-hash with trailing comment", `"abc #123" # real comment`, `"abc #123"`},
		{"unquoted multiple space-hash", "val # first # second", "val"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(stripYAMLComment([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("stripYAMLComment(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- isRedactedPlaceholder ---

func TestIsRedactedPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"all asterisks", "********", true},
		{"single asterisk", "*", true},
		{"REDACTED tag", "[REDACTED]", true},
		{"real value", "my-secret-key", false},
		{"asterisks with text", "***abc", false},
		{"text with asterisks", "abc***", false},
		{"redacted lowercase", "[redacted]", false},
		{"partial redacted", "REDACTED", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isRedactedPlaceholder([]byte(tt.input))
			if got != tt.want {
				t.Errorf("isRedactedPlaceholder(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- mergeSecrets (redacted placeholder paths) ---

func TestMergeSecrets_restores_redacted_placeholder(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := mergeSecrets([]byte("sonarr:\n  api_key: \"********\"\n"), existingPath)
	if !strings.Contains(string(got), "real-secret-key") {
		t.Errorf("mergeSecrets(redacted placeholder) = %q, want to contain %q",
			string(got), "real-secret-key")
	}
	if strings.Contains(string(got), "********") {
		t.Errorf("mergeSecrets(redacted placeholder) = %q, should not contain asterisks",
			string(got))
	}
}

func TestMergeSecrets_restores_REDACTED_tag(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := mergeSecrets([]byte("sonarr:\n  api_key: \"[REDACTED]\"\n"), existingPath)
	if !strings.Contains(string(got), "real-secret-key") {
		t.Errorf("mergeSecrets([REDACTED]) = %q, want to contain %q",
			string(got), "real-secret-key")
	}
}

// --- atomicWriteConfig ---

func TestAtomicWriteConfig_writes_content(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("key: value\n")

	if err := atomicWriteConfig(context.Background(), path, data, ".test-*.tmp"); err != nil {
		t.Fatalf("atomicWriteConfig(%q) error = %v", path, err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(got) != string(data) {
		t.Errorf("atomicWriteConfig content = %q, want %q", got, data)
	}
}

func TestAtomicWriteConfig_sets_permissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := atomicWriteConfig(context.Background(), path, []byte("test"), ".test-*.tmp"); err != nil {
		t.Fatalf("atomicWriteConfig error = %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	perm := fi.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("atomicWriteConfig permissions = %o, want 600", perm)
	}
}

func TestAtomicWriteConfig_overwrites_existing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	newData := []byte("new content")
	if err := atomicWriteConfig(context.Background(), path, newData, ".test-*.tmp"); err != nil {
		t.Fatalf("atomicWriteConfig error = %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(newData) {
		t.Errorf("atomicWriteConfig overwrite content = %q, want %q", got, newData)
	}
}

func TestAtomicWriteConfig_nonexistent_dir_returns_error(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nonexistent", "config.yaml")

	err := atomicWriteConfig(context.Background(), path, []byte("test"), ".test-*.tmp")
	if err == nil {
		t.Error("atomicWriteConfig(nonexistent dir) error = nil, want error")
	}
}

func TestAtomicWriteConfig_empty_data(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := atomicWriteConfig(context.Background(), path, []byte{}, ".test-*.tmp"); err != nil {
		t.Fatalf("atomicWriteConfig(empty) error = %v", err)
	}

	got, _ := os.ReadFile(path)
	if len(got) != 0 {
		t.Errorf("atomicWriteConfig(empty) content len = %d, want 0", len(got))
	}
}

func TestFindClosingQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		quote byte
		want  int
	}{
		{"empty after opening double quote", `"`, '"', -1},
		{"simple double quoted", `"abc"`, '"', 4},
		{"simple single quoted", `'abc'`, '\'', 4},
		{"escaped double quote inside", `"ab\"cd"`, '"', 7},
		{"backslash not escape in single quote", `'ab\'`, '\'', 4},
		{"double quote with backslash at end", `"ab\"`, '"', -1},
		{"two escaped backslashes then quote", `"a\\\\"`, '"', 6},
		{"empty quoted string", `""`, '"', 1},
		{"single char quoted", `"x"`, '"', 2},
		{"no closing quote", `"abcdef`, '"', -1},
		{"escaped backslash then quote", `"a\\"`, '"', 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := findClosingQuote([]byte(tt.input), tt.quote)
			if got != tt.want {
				t.Errorf("findClosingQuote(%q, %q) = %d, want %d",
					tt.input, string(tt.quote), got, tt.want)
			}
		})
	}
}
