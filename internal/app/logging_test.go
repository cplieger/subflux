package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestBootLoggingFollowsServerSection(t *testing.T) {
	t.Run("failed_file_keeps_text", func(t *testing.T) {
		out := &syncBuffer{}
		boot(t, "server:\n  log_format: text\nbogus: 1\n", func(c *Config) { c.LogOutput = out })
		got := out.String()
		if !strings.Contains(got, "level=WARN") || !strings.Contains(got, "msg=\"settings failed to load") || strings.Contains(got, "{\"time\"") {
			t.Errorf("a Failed file with log_format text logged:\n%s", got)
		}
		if !strings.Contains(got, "fields=bogus") {
			t.Errorf("the failure line does not name the path:\n%s", got)
		}
	})
	t.Run("broken_server_section_logs_json_at_info", func(t *testing.T) {
		out := &syncBuffer{}
		boot(t, "server:\n  log_level: debug\n  log_format: yaml\n", func(c *Config) { c.LogOutput = out })
		slog.Debug("probe-debug")
		got := out.String()
		if !strings.HasPrefix(got, "{") || !strings.Contains(got, `"level":"WARN"`) {
			t.Errorf("a broken server section logged:\n%s", got)
		}
		if strings.Contains(got, "probe-debug") {
			t.Error("a broken server section's debug level was applied")
		}
	})
	t.Run("captured_logger_follows_a_save", func(t *testing.T) {
		out := &syncBuffer{}
		b := boot(t, "", func(c *Config) { c.LogOutput = out })
		captured := slog.Default()
		if captured.Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("DEBUG enabled before the save")
		}
		saveText(t, b.svc, "server:\n  log_level: debug\n")
		if !eventually(func() bool { return captured.Enabled(context.Background(), slog.LevelDebug) }) {
			t.Fatal("a logger captured before the save never reached DEBUG")
		}
		captured.Debug("probe-after-save")
		if !strings.Contains(out.String(), "probe-after-save") {
			t.Errorf("the captured logger's DEBUG line is missing:\n%s", out.String())
		}
	})
}
