package authhandlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Kills audit.go:70:27 ARITHMETIC_BASE (make([]any, 0, 6+len(kvs)), + -> -).
// Calling Audit with 7 extra key/value attrs makes the original capacity
// 6 + 7 = 13 (a valid make). The "-" mutant computes 6 - 7 = -1, and make()
// panics on a negative capacity ("makeslice: cap out of range"). The recover
// turns that panic into a clean test failure; the intact "+" produces no panic.
func Test_gk_subflux_u30_AuditManyAttrsNoPanic(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("Audit panicked with many attrs: %v (capacity must be 6+len(kvs), not 6-len(kvs))", rec)
		}
	}()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	Audit(req, slog.LevelInfo, AuditLoginSuccess, true, "alice",
		slog.String("a", "1"), slog.String("b", "2"), slog.String("c", "3"),
		slog.String("d", "4"), slog.String("e", "5"), slog.String("f", "6"),
		slog.String("g", "7"),
	)
}

// Kills ceremony.go:102:16 INVERT_NEGATIVES (sm.count.Add(-1), -1 -> 1).
// LoadAndDelete decrements the live-session counter. After Store (+1) then
// LoadAndDelete the original counter returns to 0. The "+1" mutant adds instead
// of subtracting, leaving the counter at 2.
func Test_gk_subflux_u30_LoadAndDeleteDecrementsCount(t *testing.T) {
	sm := NewShardedCeremonyMap[*WebAuthnSession]()
	if !sm.Store("k", &WebAuthnSession{CreatedAt: time.Now()}) {
		t.Fatalf("Store returned false, want true")
	}
	if got := sm.count.Load(); got != 1 {
		t.Fatalf("after Store, count = %d, want 1", got)
	}
	if _, ok := sm.LoadAndDelete("k"); !ok {
		t.Fatalf("LoadAndDelete found = false, want true")
	}
	if got := sm.count.Load(); got != 0 {
		t.Errorf("after LoadAndDelete, count = %d, want 0", got)
	}
}
