package store

import (
	"context"
	"testing"
	"time"
)

func BenchmarkCleanupExpiredSessions(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	now := time.Now()
	idle := 24 * time.Hour
	abs := 7 * 24 * time.Hour
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = db.CleanupExpiredSessions(ctx, now, idle, abs)
	}
}
