package statedb

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/bolttable"
)

// Store is subflux's state store over one bolttable file.
type Store struct {
	db *bolttable.DB
	t  *coreTables
	// changed is the current generation's Changed channel; sig guards it.
	changed chan struct{}
	// workReady holds at most one pending wake for the work drainer.
	workReady chan struct{}
	// active is the activation in force, nil before the first Activate.
	active atomic.Pointer[activation]
	// now is the clock orphan and expiry times are read from.
	now    func() time.Time
	health bolttable.WriteHealth
	sig    sync.Mutex
	// actMu is the activation lock: Activate holds it exclusively from its
	// read of the stored activation to installing the next; every write
	// judged under the activation holds it shared, so none commits under an
	// activation that is being replaced.
	actMu sync.RWMutex
}

// config is what open varies: the schema's tables, the bolttable options
// and the clock.
type config struct {
	tables *coreTables
	now    func() time.Time
	opts   bolttable.Options
}

// productionConfig is design section 3's one Open call: a second process
// waits 5 s for the lock, a format change resets every listed name keeping
// two .old copies, and a file half free and 64 MiB free compacts.
func productionConfig() config {
	return config{tables: core, now: time.Now, opts: bolttable.Options{
		LockWait:  5 * time.Second,
		Compact:   bolttable.CompactPolicy{MinFreeRatio: 0.5, MinFreeBytes: 64 << 20},
		Reset:     bolttable.ResetPolicy{Allow: func(bolttable.NamespaceDigest) bool { return true }, Keep: 2},
		Retention: 20_000,
	}}
}

// Open opens or creates the store at path, holding path.lock until Close. A
// format change resets the changed namespaces after keeping the whole file
// as path.<8 hex>.<stamp>.old; another holder of the lock fails it with
// bolttable.ErrLocked after 5 s. Every error leaves the live file unreset.
func Open(path string) (*Store, error) {
	cfg := productionConfig()
	return open(path, &cfg)
}

func open(path string, cfg *config) (*Store, error) {
	s := &Store{t: cfg.tables, now: cfg.now, changed: make(chan struct{}), workReady: make(chan struct{}, 1)}
	opts := cfg.opts
	opts.BeforeCommit = s.beforeCommit
	opts.OnCommit = s.onCommit
	db, err := bolttable.Open(path, cfg.tables.schema, opts)
	if err != nil {
		return nil, fmt.Errorf("statedb: open %s: %w", path, err)
	}
	s.db = db
	report := db.OpenReport()
	logOpenReport(&report)
	if err := s.wakeForStoredWork(); err != nil {
		return nil, errors.Join(fmt.Errorf("statedb: open %s: %w", path, err), db.Close())
	}
	return s, nil
}

// Close closes the file and releases its lock; a second Close returns nil.
func (s *Store) Close() error { return s.db.Close() }

func logOpenReport(r *bolttable.OpenReport) {
	if len(r.Reset) > 0 {
		names := make([]string, 0, len(r.Reset))
		for _, n := range r.Reset {
			names = append(names, n.Name+" stored "+shortDigest(n.Stored)+" recreated "+shortDigest(n.Want))
		}
		slog.Warn("statedb: store format changed, reset", "namespaces", names, "kept", r.Kept)
	}
	if r.Compacted {
		slog.Info("statedb: store compacted")
	}
	if r.CompactErr != nil {
		slog.Warn("statedb: store compaction failed", "error", r.CompactErr)
	}
}

// shortDigest matches the 8 hex digits bolttable names a .old copy by.
func shortDigest(d [32]byte) string {
	if d == ([32]byte{}) {
		return "none"
	}
	return hex.EncodeToString(d[:4])
}
