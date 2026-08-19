package server

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/server/activity"
)

// recordStoreWriteError is the one branching member the deleted serveradapter
// package carried: a disk-full / I/O failure must escalate to a persistent
// operator alert (so the server warns before crash-looping), while ordinary or
// nil errors must not. Ported here with the function itself — the three store
// write paths that call it (backup snapshot, poll heartbeat, and the
// scheduler's reconcile via the injected recorder) all meet in this package.
func TestRecordStoreWriteError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantPersistent bool
	}{
		{"disk/permission error escalates to a persistent alert", os.ErrPermission, true},
		{"nil error is a no-op", nil, false},
		{"ordinary write error does not escalate", errors.New("transient write glitch"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{alerts: activity.NewAlertLog(10)}

			s.recordStoreWriteError(tt.err)

			visible := s.alerts.VisibleAlerts()
			if !tt.wantPersistent {
				if len(visible) != 0 {
					t.Fatalf("got %d alerts, want 0 (no escalation expected)", len(visible))
				}
				return
			}
			if len(visible) != 1 {
				t.Fatalf("got %d alerts, want exactly 1 persistent alert", len(visible))
			}
			got := visible[0]
			if got.Kind != activity.AlertPersistent {
				t.Errorf("Kind = %q, want %q", got.Kind, activity.AlertPersistent)
			}
			if got.Source != "store" {
				t.Errorf("Source = %q, want %q", got.Source, "store")
			}
			if got.Level != activity.LevelError {
				t.Errorf("Level = %q, want %q", got.Level, activity.LevelError)
			}
			if !strings.Contains(got.Message, "disk full") {
				t.Errorf("Message = %q, want it to mention disk full", got.Message)
			}
		})
	}
}
