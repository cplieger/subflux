package statedb

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/cplieger/bolttable"
)

// healthClass is the one WriteHealth class every store write records under.
const healthClass = "store"

type callKey struct{}

// callToken names one write call across bbolt's reruns of it, so a
// bolttable.Options.CommitFault hook can fail exactly that call. It is not
// zero-size: pointers to distinct zero-size values may compare equal.
type callToken struct{ _ byte }

// runner is bolttable's DB.Update or DB.Batch.
type runner func(context.Context, func(*bolttable.Tx) error) error

// write is the one path every store write takes: run is s.db.Update or
// s.db.Batch, op names the operation in the commit-failure log.
func (s *Store) write(ctx context.Context, op string, run runner, fn func(*bolttable.Tx) error) error {
	err := run(context.WithValue(ctx, callKey{}, &callToken{}), fn)
	s.health.Record(healthClass, err)
	if errors.Is(err, bolttable.ErrCommit) {
		slog.Error("statedb: write did not commit", "op", op, "cause", commitCause(err), "error", err)
	}
	return err
}

// commitCause classifies a failed commit by its text, since bbolt passes a
// file-grow failure's errno as text only (go.etcd.io/bbolt v1.5.0 db.go:1245,
// "file resize error: %s").
func commitCause(err error) string {
	switch msg := err.Error(); {
	case strings.Contains(msg, "no space left on device"):
		return "ENOSPC"
	case strings.Contains(msg, "read-only file system"):
		return "EROFS"
	case strings.Contains(msg, "input/output error"):
		return "EIO"
	}
	return "other"
}

// WriteHealth is the store's write outcomes since Open: every failed commit
// counted, the latest kept, and when a write last failed and last committed.
type WriteHealth struct {
	Last               error
	LastFailed, LastOK time.Time
	Failures           uint64
}

// WriteHealth reports the outcomes of every write since Open. A refusal, a
// unique-index conflict or a context error is not a failure.
func (s *Store) WriteHealth() WriteHealth {
	rec, _ := s.health.Class(healthClass)
	return WriteHealth{Failures: rec.Failures, Last: rec.LastErr, LastFailed: rec.LastFailed, LastOK: rec.LastOK}
}
