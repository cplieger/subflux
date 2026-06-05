package server

import (
	"testing"
	"time"
)

func FuzzSessionDebounceShouldUpdate(f *testing.F) {
	f.Add("abc123", int64(0))
	f.Add("", int64(1000000000))
	f.Add("session-xyz", int64(60000000000))

	f.Fuzz(func(t *testing.T, hash string, nanoOffset int64) {
		d := newSessionActivityDebouncer()
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		// First call should always return true for a new hash
		if !d.shouldUpdate(hash, base) {
			t.Fatal("first shouldUpdate must return true")
		}
		// Second call with offset
		if nanoOffset < 0 {
			nanoOffset = -nanoOffset
		}
		t2 := base.Add(time.Duration(nanoOffset % int64(5*time.Minute)))
		_ = d.shouldUpdate(hash, t2)
	})
}
