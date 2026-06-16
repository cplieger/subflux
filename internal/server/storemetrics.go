package server

import (
	"context"
	"log/slog"
	"time"
)

// storeStatsProvider is the narrow capability the store-metrics collector needs
// from the database. The concrete *boltstore.DB satisfies this; the server
// type-asserts s.db to check availability.
type storeStatsProvider interface {
	StoreFileStats() (fileBytes, freelistBytes int64)
}

// storeMetricsInterval is how often the store file-size and freelist gauges are
// refreshed. 5 minutes keeps the /metrics scrape cheap (no per-request View tx)
// while still catching file growth early enough for alerting.
const storeMetricsInterval = 5 * time.Minute

// runStoreMetrics periodically reads the bbolt file size and freelist stats and
// records them as Prometheus gauges. It exits when ctx is cancelled.
func (s *Server) runStoreMetrics(ctx context.Context) {
	sp, ok := s.db.(storeStatsProvider)
	if !ok {
		slog.Debug("store does not implement StoreFileStats; store metrics disabled")
		return
	}

	// Record once immediately at startup so the gauges are populated before
	// the first scrape.
	fileBytes, freelistBytes := sp.StoreFileStats()
	s.metrics.RecordStoreFileSize(fileBytes)
	s.metrics.RecordStoreFreelistBytes(freelistBytes)

	ticker := time.NewTicker(storeMetricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fb, fl := sp.StoreFileStats()
			s.metrics.RecordStoreFileSize(fb)
			s.metrics.RecordStoreFreelistBytes(fl)
		}
	}
}
