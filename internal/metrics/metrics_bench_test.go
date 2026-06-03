package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"subflux/internal/api"
)

func BenchmarkRecordSearch(b *testing.B) {
	m := New()

	prePopulated := []api.ProviderID{"opensubtitles", "yify", "betaseries", "hdbits", "anidb"}
	for _, p := range prePopulated {
		m.RecordSearch(p, time.Millisecond, nil)
	}

	b.Run("registered_provider", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				m.RecordSearch("opensubtitles", 150*time.Millisecond, nil)
			}
		})
	})

	b.Run("new_provider", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				i++
				m.RecordSearch(api.ProviderID(fmt.Sprintf("bench-%d", i)), time.Millisecond, nil)
			}
		})
	})
}

func BenchmarkHandler(b *testing.B) {
	m := New()

	providers := []api.ProviderID{"opensubtitles", "yify", "betaseries", "hdbits", "anidb"}
	for _, p := range providers {
		for range 100 {
			m.RecordSearch(p, 150*time.Millisecond, nil)
		}
		for range 10 {
			m.RecordSearch(p, 500*time.Millisecond, errors.New("timeout"))
		}
		for range 50 {
			m.RecordDownload(p, nil)
		}
		for range 5 {
			m.RecordDownload(p, errors.New("fail"))
		}
	}
	m.RecordImport("sonarr")
	m.RecordImport("radarr")
	m.RecordScan(500, 42, 3*time.Second)
	for range 20 {
		m.AdaptiveSkip()
	}

	handler := m.Handler()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
