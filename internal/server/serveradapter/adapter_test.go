package serveradapter_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

// RecordStoreWriteError is the one adapter method with real branching: a
// disk-full / I/O failure must escalate to a persistent operator alert (so the
// server warns before crash-looping), while ordinary or nil errors must not.
// The other adapter methods are pure pass-through to activity/events and are
// covered where those packages are tested.
func TestAlertAdapter_RecordStoreWriteError(t *testing.T) {
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
			al := activity.NewAlertLog(10)
			a := &serveradapter.AlertAdapter{A: al}

			a.RecordStoreWriteError(tt.err)

			visible := al.VisibleAlerts()
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
