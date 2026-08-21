package server

import (
	"log/slog"

	"github.com/cplieger/subflux/internal/boltstore"
)

// recordStoreWriteError checks whether the error indicates a disk-full or
// unrecoverable I/O condition and raises a persistent alert so operators are
// notified before the system crash-loops. Non-disk-full write errors are logged
// at ERROR level with a distinctive message for Loki/Alertmanager matching.
//
// It lives in the composition root because classification needs the concrete
// storage engine (boltstore's check knows bbolt's own error vocabulary, not
// just errno) while the alert lands in the server's AlertLog, and because all
// three store-write paths that escalate — the backup snapshot, the poll
// heartbeat, and the scheduler's reconcile pass — meet only here. The
// scheduler receives it as an injected func rather than importing the engine.
func (s *Server) recordStoreWriteError(err error) {
	if err == nil {
		return
	}
	if boltstore.IsDiskFullError(err) {
		slog.Error("store write failed: disk full or I/O error — persistent alert raised",
			"error", err)
		s.alerts.RecordPersistent("store",
			"Database write failed (disk full or I/O error): "+err.Error()+
				". Free disk space or check filesystem permissions to resume normal operation.")
		return
	}
	// Non-disk-full write error: log distinctively for monitoring.
	slog.Error("store write failed", "error", err)
}
