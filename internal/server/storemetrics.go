package server

import (
	"context"
	"time"
)

// storeMetricsInterval is how often the store file-size and freelist gauges are
// refreshed. 5 minutes keeps the /metrics scrape cheap (no per-request View tx)
// while still catching file growth early enough for alerting.
const storeMetricsInterval = 5 * time.Minute

// runStoreMetrics periodically reads the bbolt file size and freelist stats and
// records them as Prometheus gauges. It exits when ctx is cancelled.
func (s *Server) runStoreMetrics(ctx context.Context) {
	// Record once immediately at startup so the gauges are populated before
	// the first scrape.
	fileBytes, freelistBytes := s.db.StoreFileStats()
	s.metrics.RecordStoreFileSize(fileBytes)
	s.metrics.RecordStoreFreelistBytes(freelistBytes)

	ticker := time.NewTicker(storeMetricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fb, fl := s.db.StoreFileStats()
			s.metrics.RecordStoreFileSize(fb)
			s.metrics.RecordStoreFreelistBytes(fl)
		}
	}
}
