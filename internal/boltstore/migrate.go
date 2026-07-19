package boltstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// This file is the schema-migration FRAMEWORK: the versioned forward-only
// migration ladders run at Open. The registered ladders themselves live in
// migrations.go (empty by design until a post-v1 breaking change ships).
//
// Shape (proven in production by etcd's storage/schema migration plan and
// LND's channeldb ladder; SQLite's user_version validates the app-owned-stamp
// pattern): each domain (core, auth) carries a sequential ladder of
// migration{from, to, kind, ...} steps. Open reads both stamps strictly,
// refuses a newer-than-binary file, validates the registries, snapshots the
// file before the first pending step, then executes steps one at a time —
// re-reading the stamps after every committed step so a crash resumes exactly
// where it stopped. Per-step commit-with-stamp is a deliberate subflux
// divergence (LND commits its whole ladder in one tx; etcd runs its plan in
// one locked batch tx): migrations are rare, the engine is single-writer, and
// restartable per-step commits make every crash window a consistent
// previous-version database.
//
// Two step kinds exist:
//
//   - In-place: one write transaction performing a targeted transform; the
//     step's OWN domain stamp is advanced in the SAME transaction, so a crash
//     between steps leaves a consistent previous-version database that
//     re-runs cleanly. The other domain's stamp is never touched.
//   - Copy-to-new-file: an offline swap state machine (below) for
//     format-level rewrites that cannot be expressed as in-place transforms.
//
// The destructive-step fallback for the core domain (export the irreplaceable
// set, reset the core buckets, restore) is resetCorePreserving below; the
// auth-domain equivalent is owned by internal/authstore (ResetPreserving),
// wired into a ladder entry as a plain transaction callback so this
// orchestrator stays domain-blind.

// --- Step and domain model ---

// migrationKind selects how a ladder step executes.
type migrationKind int

const (
	// migrateInPlace runs the step as a single write transaction on the live
	// handle; the framework advances the domain stamp in the same transaction.
	migrateInPlace migrationKind = iota
	// migrateCopy runs the step as the offline copy-to-new-file swap state
	// machine: the whole file is rewritten through the step's transform into a
	// temp file, both stamps are written into the destination, and the temp is
	// atomically renamed over the live file.
	migrateCopy
)

// String implements fmt.Stringer for log attributes.
func (k migrationKind) String() string {
	switch k {
	case migrateInPlace:
		return "in-place"
	case migrateCopy:
		return "copy"
	default:
		return fmt.Sprintf("migrationKind(%d)", int(k))
	}
}

// copyTransform rewrites one key/value pair during a copy-kind step's bucket
// walk. bucketPath is the SOURCE nested bucket name chain, outermost first
// (a bucket relocated by the step's bucket map still presents its source
// path here). Returning a nil newKey drops the record. k and v alias
// bbolt-owned memory in the SOURCE read transaction, which the swap machine
// keeps open until the destination commits, so returning them unchanged (or
// slices derived from them) is safe; retaining them beyond the transform call
// is not.
type copyTransform func(bucketPath []string, k, v []byte) (newKey, newValue []byte, err error)

// copyBucketMap gives a copy-kind step controlled BUCKET-level rewrite access
// during the tree walk — the format-rewrite capability record transforms alone
// cannot express (Requirement 4.1: rename, re-parent, merge, or drop whole
// buckets). It is consulted once per SOURCE bucket (named by its full path,
// outermost first) and is the total authority on where that bucket lands:
//
//   - return the input path to copy the bucket in place,
//   - return a different path to rename or re-parent it,
//   - return a path another source bucket also maps to, to merge the two
//     (records combined — on a key collision the later-walked source wins;
//     the destination keeps the HIGHEST merged sequence so future
//     allocations cannot collide with any merged-in surrogate key),
//   - return nil to drop the bucket, its records, and its entire subtree.
//
// Nested buckets are consulted with their own full source path: a step that
// relocates a parent must map its descendants consistently too. Creating a
// brand-new EMPTY bucket is not a mapping concern — bucket bootstrap
// (CreateBucketIfNotExists after the ladder) or an in-place step owns that.
// The record transform still sees the SOURCE path, so mapping and record
// rewriting compose independently.
type copyBucketMap func(bucketPath []string) ([]string, error)

// migration is one forward step of a domain's ladder: it migrates a database
// stamped `from` to `to` (always from+1; validated at Open). run is the
// in-place body (migrateInPlace only); transform and mapBucket are the
// copy-kind record and bucket rewrites (migrateCopy only; both nil means a
// pure identity rewrite).
type migration struct {
	run       func(tx *bolt.Tx) error // in-place step body (nil for copy kind)
	transform copyTransform           // copy-kind record transform (nil = identity)
	mapBucket copyBucketMap           // copy-kind bucket-level rewrite (nil = identity layout)
	from      uint64
	to        uint64
	kind      migrationKind
}

// migrationDomain describes one independently versioned schema domain (core or
// auth): its stamp key in the meta bucket, the version this binary writes and
// understands, the domain's base (first-ever) version, its registered ladder,
// and the buckets whose DATA marks the domain as populated (used to
// distinguish a genuinely fresh file from a populated file that lost its
// stamp). Production domains come from coreDomain/authDomain (migrations.go);
// tests inject custom domains.
type migrationDomain struct {
	name        string
	stampKey    []byte
	dataBuckets [][]byte
	ladder      []migration
	// current is the schema version this build writes and understands.
	current uint64
	// base is the domain's FIRST shipped schema version: the floor every
	// registered ladder must start from. It never changes once a domain
	// exists; only current moves. validateLadder accepts an empty ladder
	// only while current == base — a version bump without a registered
	// migration path fails the open loudly (Requirement 1.6).
	base uint64
}

// --- Refusal sentinels ---

// Sentinel errors for the two open-refusal classes, matchable with errors.Is;
// the wrapping errors name the concrete domain and versions.
var (
	// errSchemaNewer is the downgrade guard: the stored stamp was written by a
	// newer build and no downgrade migration exists.
	errSchemaNewer = errors.New("database schema is newer than this build (no downgrade path exists; restore the pre-migration snapshot or a backup written by a matching version, or run a newer subflux)")

	// errSchemaStampInvalid is the fail-closed guard: a populated database
	// whose stamp is missing or malformed has an unknowable schema version, so
	// guessing (and migrating or overwriting) could destroy data.
	errSchemaStampInvalid = errors.New("database schema stamp is missing or malformed (refusing to guess the schema version; restore from a backup or pre-migration snapshot)")
)

// --- Strict stamp reads ---

// stampState classifies a strict stamp read. Unlike kv.GetUint64 (which
// treats a malformed scalar as absent — fine for counters, never for stamps),
// missing and malformed are distinguished so malformed can fail closed.
type stampState int

const (
	stampPresent stampState = iota
	stampMissing
	stampMalformed
)

// readStamp strictly reads a schema-version stamp from the meta bucket:
// an absent meta bucket or key is stampMissing, a non-8-byte value is
// stampMalformed (never treated as absent), and only an 8-byte value decodes.
func readStamp(tx *bolt.Tx, key []byte) (uint64, stampState) {
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return 0, stampMissing
	}
	raw := mb.Get(key)
	if raw == nil {
		return 0, stampMissing
	}
	v, ok := kv.DecodeBe64(raw)
	if !ok {
		return 0, stampMalformed
	}
	return v, stampPresent
}

// domainState is the resolved schema position of one domain at open time.
type domainState struct {
	// stored is the on-disk version; meaningful only when fresh is false.
	stored uint64
	// fresh means the domain has no stamp AND no data: bootstrap it at the
	// current version and run zero migrations (never "run the ladder from 0").
	fresh bool
}

// readRequiredVersion resolves a domain's schema position, failing closed per
// Requirement 1.2:
//
//   - present + newer than the binary -> errSchemaNewer (downgrade guard),
//   - present otherwise -> the stored version (equal = fast path, older =
//     ladder input),
//   - malformed -> errSchemaStampInvalid, ALWAYS (malformed is never absent),
//   - missing + domain data present -> errSchemaStampInvalid (a populated
//     file's version must never be guessed),
//   - missing + no domain data -> fresh (bootstrap at current, no migrations).
func readRequiredVersion(tx *bolt.Tx, d *migrationDomain) (domainState, error) {
	v, st := readStamp(tx, d.stampKey)
	switch st {
	case stampPresent:
		if v > d.current {
			return domainState{}, fmt.Errorf("%s schema version %d on disk is newer than this build's %d: %w",
				d.name, v, d.current, errSchemaNewer)
		}
		return domainState{stored: v}, nil
	case stampMalformed:
		return domainState{}, fmt.Errorf("%s schema stamp %q is malformed: %w",
			d.name, d.stampKey, errSchemaStampInvalid)
	default: // stampMissing
		if domainHasData(tx, d) {
			return domainState{}, fmt.Errorf("%s schema stamp %q is missing but %s data exists: %w",
				d.name, d.stampKey, d.name, errSchemaStampInvalid)
		}
		return domainState{fresh: true}, nil
	}
}

// domainHasData reports whether any of the domain's data buckets holds at
// least one key. It is the populated-vs-fresh probe for a missing stamp: a
// bucket that merely exists but is empty carries no data to lose, so it does
// not make the domain populated.
func domainHasData(tx *bolt.Tx, d *migrationDomain) bool {
	for _, name := range d.dataBuckets {
		b := tx.Bucket(name)
		if b == nil {
			continue
		}
		if k, _ := b.Cursor().First(); k != nil {
			return true
		}
	}
	return false
}

// readDomainStates resolves both domains' schema positions in one read
// transaction. Any refusal (newer, malformed, missing-in-populated) surfaces
// here, before anything is written. BOTH domains are classified before the
// view returns, so a file whose core AND auth stamps refuse produces one
// combined diagnostic naming every stored/current version pair plus the
// recovery path (Requirement 1.4) — never just the first domain's half.
func readDomainStates(db *bolt.DB, core, auth *migrationDomain) (coreState, authState domainState, err error) {
	err = db.View(func(tx *bolt.Tx) error {
		var coreErr, authErr error
		coreState, coreErr = readRequiredVersion(tx, core)
		authState, authErr = readRequiredVersion(tx, auth)
		// errors.Join keeps both refusal chains matchable (errors.Is) and
		// both messages — each already names its domain's stored/current
		// versions and the snapshot/backup restore path — in one error.
		return errors.Join(coreErr, authErr)
	})
	return coreState, authState, err
}

// --- Ladder validation ---

// validateLadder checks a domain's registered ladder at Open — runtime, not
// test-only, so a shipped malformed registry fails the open loudly before any
// user data is touched (Requirement 1.6): every step advances exactly one
// version (to == from+1), steps are contiguous (from == prev.to — no gaps, no
// duplicates, no reordering), each step carries the body its kind requires,
// the ladder starts at the domain's base version, and it terminates exactly
// at the binary's current version. An empty ladder is valid ONLY while
// current still equals the domain's base (no breaking change has shipped);
// a bumped schema constant without a registered path from base to current is
// exactly the malformed registry this validation exists to refuse.
func validateLadder(d *migrationDomain) error {
	if len(d.ladder) == 0 {
		if d.current != d.base {
			return fmt.Errorf("boltstore: invalid %s migration registry: ladder is empty but this build's version v%d is past the domain base v%d (a schema bump must register its migration path)",
				d.name, d.current, d.base)
		}
		return nil
	}
	if first := d.ladder[0].from; first != d.base {
		return fmt.Errorf("boltstore: invalid %s migration registry: ladder starts at v%d, want the domain base v%d",
			d.name, first, d.base)
	}
	for i := range d.ladder {
		if err := validateStep(d, i); err != nil {
			return err
		}
	}
	if last := d.ladder[len(d.ladder)-1].to; last != d.current {
		return fmt.Errorf("boltstore: invalid %s migration registry: ladder ends at v%d but this build's version is v%d",
			d.name, last, d.current)
	}
	return nil
}

// validateStep checks one ladder entry for validateLadder: a single-version
// advance, contiguity with the previous step, and the body its kind requires.
func validateStep(d *migrationDomain, i int) error {
	step := &d.ladder[i]
	if step.to != step.from+1 {
		return fmt.Errorf("boltstore: invalid %s migration registry: step %d migrates v%d->v%d, want single-version steps (to == from+1)",
			d.name, i, step.from, step.to)
	}
	if i > 0 && step.from != d.ladder[i-1].to {
		return fmt.Errorf("boltstore: invalid %s migration registry: step %d starts at v%d but the previous step ends at v%d (gap, duplicate, or misorder)",
			d.name, i, step.from, d.ladder[i-1].to)
	}
	switch step.kind {
	case migrateInPlace:
		if step.run == nil {
			return fmt.Errorf("boltstore: invalid %s migration registry: in-place step v%d->v%d has no run body",
				d.name, step.from, step.to)
		}
	case migrateCopy:
		// A nil transform / nil bucket map is a valid identity rewrite.
	default:
		return fmt.Errorf("boltstore: invalid %s migration registry: step v%d->v%d has unknown kind %d",
			d.name, step.from, step.to, int(step.kind))
	}
	return nil
}

// pendingStep returns the domain's next pending ladder step for the given
// state. A fresh or current domain has none. An older domain must have a
// registered step starting exactly at its stored version; a stored version the
// ladder cannot bridge refuses the open (with restore guidance — never "delete
// the database").
func (d *migrationDomain) pendingStep(st domainState) (migration, bool, error) {
	if st.fresh || st.stored == d.current {
		return migration{}, false, nil
	}
	for _, step := range d.ladder {
		if step.from == st.stored {
			return step, true, nil
		}
	}
	return migration{}, false, fmt.Errorf(
		"boltstore: no registered %s migration from schema v%d (this build's version is v%d); restore a backup or pre-migration snapshot written by a compatible version",
		d.name, st.stored, d.current)
}

// nextPendingStep picks the next step across both domains: the core ladder
// drains first, then the auth ladder (a deterministic order so crash-resume
// re-picks the same step).
func nextPendingStep(core, auth *migrationDomain, coreState, authState domainState) (migration, *migrationDomain, bool, error) {
	if step, ok, err := core.pendingStep(coreState); err != nil || ok {
		return step, core, ok, err
	}
	step, ok, err := auth.pendingStep(authState)
	return step, auth, ok, err
}

// --- Crash failpoints (test-only, env-gated) ---

// migrateCrashPointEnv names the environment variable the subprocess crash
// fixtures set to abort the process at a specific migration boundary. This is
// the one sanctioned env read outside the config layer: the approved design
// requires crash-safety to be proven with subprocess failpoints, and the hook
// must live in the production sequence to fail at the REAL boundaries. Unset
// (production), every crashPoint call is an inert string compare.
const migrateCrashPointEnv = "SUBFLUX_MIGRATE_CRASHPOINT"

// crashPointExitCode is the child's abrupt-death exit code; the parent test
// asserts it to distinguish a deliberate crash from an ordinary failure.
const crashPointExitCode = 86

// Failpoint names, one per crash boundary the design requires proving
// (Requirement 5.3).
const (
	crashInPlacePreCommit = "in-place-pre-commit"   // inside a step's tx, before commit
	crashBetweenSteps     = "between-steps"         // after a committed step, before the next
	crashCopyAfterCommit  = "copy-after-dst-commit" // DST committed+checked, before rename
	crashCopyAfterRename  = "copy-after-rename"     // renamed, before directory fsync
	crashCopyBeforeReopen = "copy-before-reopen"    // directory fsynced, before reopen
)

// crashPoint aborts the process abruptly (skipping every deferred close and
// commit, like a crash) when the env-gated failpoint names this boundary. A
// no-op in production, where the variable is unset.
func crashPoint(name string) {
	if os.Getenv(migrateCrashPointEnv) == name {
		os.Exit(crashPointExitCode)
	}
}

// --- Runner ---

// runMigrations is the Open-time migration orchestrator. It resolves both
// domains' schema positions (refusing newer/malformed/missing-in-populated
// stamps), validates both registries, and — when the file already matches this
// build — returns on the fast path having only read two meta keys. Otherwise
// it writes the pre-migration snapshot (aborting if that fails: never migrate
// without the safety copy) and executes pending steps one at a time, core
// ladder first, re-reading the stamps after every committed step so a crash
// resumes exactly where it stopped.
//
// A copy-kind step swaps the underlying file, so runMigrations returns the
// live handle, which may differ from the one passed in. A nil returned handle
// means every handle is already closed (a failure after the atomic rename);
// the caller must not reuse or re-close it.
func runMigrations(db *bolt.DB, core, auth *migrationDomain) (*bolt.DB, error) {
	coreState, authState, err := readDomainStates(db, core, auth)
	if err != nil {
		return db, err
	}
	if verr := validateLadder(core); verr != nil {
		return db, verr
	}
	if verr := validateLadder(auth); verr != nil {
		return db, verr
	}
	pending, err := hasPending(core, auth, coreState, authState)
	if err != nil {
		return db, err
	}
	if !pending {
		return db, nil // fast path: stamps match this build (or fresh file)
	}

	snapshot, err := writeMigrationSnapshot(db, core, auth, coreState, authState)
	if err != nil {
		return db, err
	}
	return runPendingSteps(db, core, auth, snapshot, stampOrCurrent(coreState, core), stampOrCurrent(authState, auth))
}

// hasPending reports whether either domain has at least one ladder step to
// run. It also surfaces the unbridgeable-version refusal (an older stamp no
// registered step starts from) before any snapshot is written.
func hasPending(core, auth *migrationDomain, coreState, authState domainState) (bool, error) {
	_, _, ok, err := nextPendingStep(core, auth, coreState, authState)
	return ok, err
}

// writeMigrationSnapshot writes the pre-migration safety snapshot next to the
// live file and logs it at WARN (visible at the default log level) with both
// domains' from->to versions. Snapshot failure — including disk-full — aborts
// the migration: the open fails loudly and nothing has been modified.
// Snapshots accumulate one full-size file per attempt and are operator-owned;
// pruning guidance lives in the restore runbook.
func writeMigrationSnapshot(db *bolt.DB, core, auth *migrationDomain, coreState, authState domainState) (string, error) {
	snapshot := migrationSnapshotPath(db.Path(), stampOrCurrent(coreState, core), stampOrCurrent(authState, auth))
	if err := backupDB(context.Background(), db, snapshot); err != nil {
		return "", fmt.Errorf("pre-migration snapshot %q: %w (migration aborted; the database was not modified)", snapshot, err)
	}
	slog.Warn("schema migration starting; pre-migration snapshot written",
		"snapshot", snapshot,
		"core_from", stampOrCurrent(coreState, core), "core_to", core.current,
		"auth_from", stampOrCurrent(authState, auth), "auth_to", auth.current)
	return snapshot, nil
}

// runPendingSteps drains both ladders, re-reading the stamps before every
// step (so pending state is always derived from committed truth, and a
// copy-swap's reopened handle is re-verified), and logs the WARN summary —
// snapshot path plus both domains' from->to — on completion. coreFrom and
// authFrom are this attempt's starting versions, carried only for that
// summary.
func runPendingSteps(db *bolt.DB, core, auth *migrationDomain, snapshot string, coreFrom, authFrom uint64) (*bolt.DB, error) {
	for {
		coreState, authState, err := readDomainStates(db, core, auth)
		if err != nil {
			return db, err
		}
		step, domain, ok, err := nextPendingStep(core, auth, coreState, authState)
		if err != nil {
			return db, err
		}
		if !ok {
			slog.Warn("schema migration complete",
				"snapshot", snapshot,
				"core_from", coreFrom, "core_to", core.current,
				"auth_from", authFrom, "auth_to", auth.current)
			return db, nil
		}
		otherKey, otherState := otherDomainStamp(domain, core, auth, coreState, authState)
		switch step.kind {
		case migrateCopy:
			db, err = runCopyStep(db, domain, step, otherKey, otherState, snapshot)
		default:
			err = runInPlaceStep(db, domain, step, otherKey, snapshot)
		}
		if err != nil {
			return db, err
		}
		crashPoint(crashBetweenSteps)
	}
}

// stampOrCurrent labels a domain's from-version for snapshot naming and logs:
// the stored stamp, or the binary's current version for a fresh domain (which
// has nothing to migrate and will simply bootstrap at current).
func stampOrCurrent(st domainState, d *migrationDomain) uint64 {
	if st.fresh {
		return d.current
	}
	return st.stored
}

// otherDomainStamp resolves the NON-migrating domain's stamp key and state for
// a copy-kind step, which must carry that stamp into the destination file
// unchanged.
func otherDomainStamp(migrating, core, auth *migrationDomain, coreState, authState domainState) ([]byte, domainState) {
	if migrating == core {
		return auth.stampKey, authState
	}
	return core.stampKey, coreState
}

// runInPlaceStep executes one in-place ladder step: the step's transform and
// the advance of its OWN domain stamp commit in the SAME write transaction, so
// a crash mid-step rolls back to a consistent previous-version database that
// re-runs cleanly, and a crash after commit resumes at the next step. The
// other domain's stamp is never touched (Requirement 1.3) — and that
// invariant is ENFORCED, not assumed: the other stamp's raw bytes are
// captured before the step body runs and re-verified after it, so a callback
// that advances, rewrites, or deletes the other domain's stamp fails the
// step and rolls the whole transaction back.
func runInPlaceStep(db *bolt.DB, d *migrationDomain, step migration, otherStampKey []byte, snapshot string) error {
	err := db.Update(func(tx *bolt.Tx) error {
		before, beforePresent := rawStampBytes(tx, otherStampKey)
		if err := step.run(tx); err != nil {
			return err
		}
		if err := verifyOtherStampUntouched(tx, otherStampKey, before, beforePresent); err != nil {
			return err
		}
		if err := writeSchemaVersion(tx, d.stampKey, step.to); err != nil {
			return err
		}
		crashPoint(crashInPlacePreCommit)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%s schema migration v%d->v%d failed: %w (database left at v%d; pre-migration snapshot: %s)",
			d.name, step.from, step.to, err, step.from, snapshot)
	}
	slog.Info("schema migration step applied",
		"domain", d.name, "from", step.from, "to", step.to, "kind", step.kind.String())
	return nil
}

// rawStampBytes reads the raw bytes of one stamp key from the meta bucket,
// DEEP-COPIED so the snapshot stays valid after the transaction mutates pages
// (bbolt slices are valid only until the next mutation). present is false when
// the meta bucket or the key is absent.
func rawStampBytes(tx *bolt.Tx, key []byte) (value []byte, present bool) {
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return nil, false
	}
	v := mb.Get(key)
	if v == nil {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// verifyOtherStampUntouched asserts the non-migrating domain's stamp has
// exactly the presence and bytes it had before the step body ran. Any change
// — an advance, a rewrite, a delete, or conjuring a stamp that did not exist
// — errors, rolling the step's whole transaction back (Requirement 1.3: a
// step stamps exactly its OWN domain).
func verifyOtherStampUntouched(tx *bolt.Tx, otherStampKey, before []byte, beforePresent bool) error {
	after, afterPresent := rawStampBytes(tx, otherStampKey)
	if afterPresent == beforePresent && bytes.Equal(after, before) {
		return nil
	}
	return fmt.Errorf("step modified the other domain's schema stamp %q (present %t -> %t, bytes %x -> %x): a migration step must never advance or drop the other domain's stamp",
		otherStampKey, beforePresent, afterPresent, before, after)
}

// --- Copy-to-new-file step (offline swap state machine) ---
//
// A copy step is NOT a db.Update on the live handle: the live file is mmap'd
// and exclusively flock'd, so renaming another file over it would leave this
// process reading the old inode. The machine instead builds the new file
// offline and swaps it in with the handles closed:
//
//	(snapshot already taken by the ladder preamble)
//	1. remove a stale temp left by a previous crashed attempt, then create
//	   the temp bolt file at the DETERMINISTIC sibling path <db>.migrate in
//	   the same directory, mode 0600 (deterministic is safe: migration runs
//	   under the live file's exclusive lock, so no two attempts overlap —
//	   and it means repeated crash-retry loops reuse ONE temp path instead
//	   of accumulating a full-size copy per attempt)
//	2. recursive bucket walk src->dst through the step's bucket map and
//	   record transform (nested buckets included, Bucket.Sequence preserved
//	   per destination bucket — the highest merged-in sequence wins)
//	3. write BOTH stamps into the DST meta (own = step.to, other = unchanged)
//	   — a crash AFTER the rename must not re-run the step
//	4. commit the DST, then run bbolt's Tx.Check on it before swapping
//	5. close BOTH handles
//	6. fsync the temp file, os.Rename it over the live path, fsync the parent
//	   directory
//	7. reopen; the ladder resumes from the re-read stamps
//
// Failure before the rename removes the temp file and leaves the original
// untouched (the open fails, pointing at the snapshot). Failure after the
// rename refuses to continue silently: the error names both the live file and
// the snapshot, and no handle is returned.
//
// Swap semantics are scoped to Linux (the shipped container): POSIX
// same-directory rename atomicity plus an explicit directory fsync. Portable
// os.Rename guarantees are NOT assumed; on other platforms the swap fails
// loudly rather than corrupting.

// runCopyStep executes one copy-kind ladder step and returns the reopened
// live handle. otherStampKey/otherState carry the non-migrating domain's stamp
// into the destination unchanged. On a post-rename failure the returned handle
// is nil (everything is closed).
func runCopyStep(db *bolt.DB, d *migrationDomain, step migration, otherStampKey []byte, otherState domainState, snapshot string) (*bolt.DB, error) {
	livePath := db.Path()
	tempPath := copyTempPath(livePath)

	if err := removeStaleCopyTemp(tempPath); err != nil {
		return db, fmt.Errorf("%s schema migration v%d->v%d (copy): %w (database unchanged; pre-migration snapshot: %s)",
			d.name, step.from, step.to, err, snapshot)
	}
	if err := buildCopyDestination(db, d, step, otherStampKey, otherState, tempPath); err != nil {
		removeTempFile(tempPath) // pre-rename failure: original untouched
		return db, fmt.Errorf("%s schema migration v%d->v%d (copy) failed before swap: %w (database unchanged; pre-migration snapshot: %s)",
			d.name, step.from, step.to, err, snapshot)
	}
	crashPoint(crashCopyAfterCommit)

	if err := db.Close(); err != nil {
		removeTempFile(tempPath)
		return nil, fmt.Errorf("%s schema migration v%d->v%d (copy): close live handle: %w (database unchanged; pre-migration snapshot: %s)",
			d.name, step.from, step.to, err, snapshot)
	}

	if err := swapCopyDestination(tempPath, livePath); err != nil {
		return nil, fmt.Errorf("%s schema migration v%d->v%d (copy): %w (pre-migration snapshot: %s)",
			d.name, step.from, step.to, err, snapshot)
	}
	crashPoint(crashCopyBeforeReopen)

	reopened, err := bolt.Open(livePath, 0o600, openOptions())
	if err != nil {
		// Post-rename: the swap is durable, so the migrated file IS the live
		// database; refuse to continue silently and name both files.
		return nil, fmt.Errorf("%s schema migration v%d->v%d (copy): swap committed but reopening %q failed: %w (the file is migrated; pre-migration snapshot: %s)",
			d.name, step.from, step.to, livePath, err, snapshot)
	}
	slog.Info("schema migration step applied",
		"domain", d.name, "from", step.from, "to", step.to, "kind", step.kind.String())
	return reopened, nil
}

// buildCopyDestination creates the temp destination file, copies every bucket
// through the step's transform, writes both stamps, commits, and runs bbolt's
// consistency check on the result. The source read transaction stays open
// until the destination commits, so transform outputs may safely alias source
// memory. Any error leaves the temp file for the caller to remove; the live
// file is untouched.
func buildCopyDestination(db *bolt.DB, d *migrationDomain, step migration, otherStampKey []byte, otherState domainState, tempPath string) error {
	dst, err := bolt.Open(tempPath, 0o600, openOptions())
	if err != nil {
		return fmt.Errorf("create temp file %q: %w", tempPath, err)
	}
	defer func() { _ = dst.Close() }() // second Close after the success path's is a no-op error we ignore

	// Outer View on the source, inner Update on the destination: bbolt requires
	// values passed to Put to stay valid until the transaction COMMITS, and
	// the copy hands source-owned slices straight to dst.Put, so the source
	// read transaction must outlive the destination commit.
	walker := copyWalker{mapBucket: step.mapBucket, transform: step.transform}
	err = db.View(func(src *bolt.Tx) error {
		return dst.Update(func(dtx *bolt.Tx) error {
			if cerr := walker.walkTree(src, dtx); cerr != nil {
				return cerr
			}
			return writeCopyStamps(dtx, d, step, otherStampKey, otherState)
		})
	})
	if err != nil {
		return fmt.Errorf("copy into %q: %w", tempPath, err)
	}

	if err := checkDB(dst); err != nil {
		return fmt.Errorf("consistency check on %q: %w", tempPath, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tempPath, err)
	}
	return nil
}

// writeCopyStamps writes both domain stamps into the destination meta bucket:
// the migrating domain advances to step.to, the other domain keeps its stored
// value byte-for-byte (a fresh other domain stays unstamped — bootstrap stamps
// it after the ladder). Writing the stamps into the DST before the swap is
// what makes a crash after the rename resume correctly instead of re-running
// the step.
func writeCopyStamps(dtx *bolt.Tx, d *migrationDomain, step migration, otherStampKey []byte, otherState domainState) error {
	mb, err := dtx.CreateBucketIfNotExists([]byte(bucketMeta))
	if err != nil {
		return fmt.Errorf("create meta bucket: %w", err)
	}
	if err := kv.PutUint64(mb, d.stampKey, step.to); err != nil {
		return err
	}
	if !otherState.fresh {
		return kv.PutUint64(mb, otherStampKey, otherState.stored)
	}
	return nil
}

// copyWalker carries a copy step's two rewrite callbacks — the bucket-level
// map and the record-level transform — through the recursive tree walk. Both
// nil is a pure identity rewrite of the whole file.
type copyWalker struct {
	mapBucket copyBucketMap
	transform copyTransform
}

// walkTree copies every top-level bucket of the source transaction into the
// destination transaction, recursing into nested buckets, routing each bucket
// through the step's bucket map (rename/re-parent/merge/drop) and every
// key/value pair through the step's record transform.
func (w copyWalker) walkTree(src, dst *bolt.Tx) error {
	return src.ForEach(func(name []byte, b *bolt.Bucket) error {
		return w.copySubtree(dst, []string{string(name)}, b)
	})
}

// copySubtree copies one source bucket — its sequence, records, and nested
// buckets — into the destination resolved by the bucket map. A nil-mapped
// bucket is dropped with its entire subtree. The destination keeps the
// HIGHEST sequence routed into it, so merged buckets stay collision-free for
// future surrogate allocations.
func (w copyWalker) copySubtree(dst *bolt.Tx, srcPath []string, src *bolt.Bucket) error {
	dstPath, err := w.resolveBucketPath(srcPath)
	if err != nil {
		return err
	}
	if dstPath == nil {
		return nil // mapped to nil: drop the bucket, its records, and its subtree
	}
	db, err := ensureDstBucket(dst, dstPath)
	if err != nil {
		return err
	}
	if seq := src.Sequence(); seq > db.Sequence() {
		if serr := db.SetSequence(seq); serr != nil {
			return fmt.Errorf("set sequence on %q: %w", dstPath, serr)
		}
	}
	return src.ForEach(func(k, v []byte) error {
		if v == nil { // nested bucket (bbolt reports bucket values as nil)
			childPath := append(append(make([]string, 0, len(srcPath)+1), srcPath...), string(k))
			return w.copySubtree(dst, childPath, src.Bucket(k))
		}
		return w.copyRecord(srcPath, db, k, v)
	})
}

// resolveBucketPath routes one source bucket path through the step's bucket
// map: identity when no map is registered; otherwise the map is the total
// authority (nil result = drop).
func (w copyWalker) resolveBucketPath(srcPath []string) ([]string, error) {
	if w.mapBucket == nil {
		return srcPath, nil
	}
	dstPath, err := w.mapBucket(srcPath)
	if err != nil {
		return nil, fmt.Errorf("bucket map %q: %w", srcPath, err)
	}
	return dstPath, nil
}

// ensureDstBucket creates — or, for a merge target, reuses — the destination
// bucket at path, creating parent buckets as needed.
func ensureDstBucket(dst *bolt.Tx, path []string) (*bolt.Bucket, error) {
	if len(path) == 0 {
		return nil, errors.New("bucket map returned an empty destination path (return nil to drop a bucket)")
	}
	b, err := dst.CreateBucketIfNotExists([]byte(path[0]))
	if err != nil {
		return nil, fmt.Errorf("create bucket %q: %w", path[0], err)
	}
	for _, name := range path[1:] {
		if b, err = b.CreateBucketIfNotExists([]byte(name)); err != nil {
			return nil, fmt.Errorf("create nested bucket %q: %w", path, err)
		}
	}
	return b, nil
}

// copyRecord routes one key/value pair through the transform (nil = identity;
// a nil returned key drops the record) and writes the result into dst.
// bucketPath is the record's SOURCE bucket path (see copyTransform).
func (w copyWalker) copyRecord(bucketPath []string, dst *bolt.Bucket, k, v []byte) error {
	nk, nv := k, v
	if w.transform != nil {
		var err error
		nk, nv, err = w.transform(bucketPath, k, v)
		if err != nil {
			return fmt.Errorf("transform %q key %x: %w", bucketPath, k, err)
		}
		if nk == nil {
			return nil // transform dropped the record
		}
	}
	if err := dst.Put(nk, nv); err != nil {
		return fmt.Errorf("put %q key %x: %w", bucketPath, nk, err)
	}
	return nil
}

// checkDB runs bbolt's full consistency check (Tx.Check) over the database and
// returns the first inconsistency, collecting the count of any further ones.
func checkDB(db *bolt.DB) error {
	return db.View(func(tx *bolt.Tx) error {
		var first error
		extra := 0
		for cerr := range tx.Check() {
			if first == nil {
				first = cerr
			} else {
				extra++
			}
		}
		if first != nil && extra > 0 {
			return fmt.Errorf("%w (and %d more inconsistencies)", first, extra)
		}
		return first
	})
}

// swapCopyDestination makes the destination durable and atomically swaps it
// over the live path: fsync the temp file, rename, fsync the parent directory
// so the rename itself is durable. Linux-scoped semantics (see the state
// machine comment above).
func swapCopyDestination(tempPath, livePath string) error {
	if err := fsyncFile(tempPath); err != nil {
		removeTempFile(tempPath) // still pre-rename: original untouched
		return fmt.Errorf("fsync temp file: %w (database unchanged)", err)
	}
	//nolint:gosec // G703: both paths derived from the trusted live db path, not external input
	if err := os.Rename(tempPath, livePath); err != nil {
		removeTempFile(tempPath)
		return fmt.Errorf("rename %q over %q: %w (database unchanged)", tempPath, livePath, err)
	}
	crashPoint(crashCopyAfterRename)
	if err := fsyncDir(filepath.Dir(livePath)); err != nil {
		// Post-rename: the new file is in place but the rename may not be
		// durable; refusing to continue silently is the contract.
		return fmt.Errorf("swap committed but directory fsync failed: %w (the live file %q is migrated)", err, livePath)
	}
	return nil
}

// copyTempPath builds the swap machine's temp destination path: the
// DETERMINISTIC sibling <livePath>.migrate. Determinism (rather than a
// per-attempt unique name) is safe because migration runs under the live
// file's exclusive bbolt lock, and it is what keeps a crash-retry loop from
// accumulating one full-size database copy per attempt: every retry targets
// the same path and clears the previous attempt's leftover first.
func copyTempPath(livePath string) string {
	return livePath + ".migrate"
}

// removeStaleCopyTemp clears a leftover temp destination from a previous
// attempt that crashed after building it but before the rename. The stale
// file is a fully-committed but never-swapped copy — the live database is
// still authoritative — so removing it and rebuilding is the correct
// recovery. An absent temp is the normal case; any other removal failure
// aborts the step (the destination path must be buildable).
func removeStaleCopyTemp(tempPath string) error {
	err := os.Remove(tempPath) //nolint:gosec // G703: path derived from the trusted live db path, not external input
	switch {
	case err == nil:
		slog.Warn("removed stale migration temp file from a previous interrupted copy step", "path", tempPath)
		return nil
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("remove stale temp file %q: %w", tempPath, err)
	}
}

// removeTempFile best-effort removes a swap temp file after a pre-rename
// failure. The path was built by runCopyStep from the live handle's own
// db.Path() — never from external input.
func removeTempFile(path string) {
	_ = os.Remove(path) //nolint:gosec // G703: path derived from the trusted live db path, not external input
}

// fsyncFile fsyncs the file at path (a swap temp file the machine itself
// created next to the live database). bbolt already fsyncs on commit; this is
// the swap machine's explicit pre-rename durability barrier.
func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // G703: path derived from the trusted live db path
	if err != nil {
		return err
	}
	serr := f.Sync()
	cerr := f.Close()
	if serr != nil {
		return serr
	}
	return cerr
}

// fsyncDir fsyncs a directory (the live database's parent) so a
// just-committed rename in it is durable.
func fsyncDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // G703: directory of the trusted live db path
	if err != nil {
		return err
	}
	serr := f.Sync()
	cerr := f.Close()
	if serr != nil {
		return serr
	}
	return cerr
}

// migrationSnapshotPath builds the pre-migration snapshot path next to the
// live file: <db>.pre-c<coreFrom>-a<authFrom>-<ts>. The from-versions are
// re-read per open attempt, so a crash-looping partial ladder produces
// correctly labeled intermediate snapshots; the nanosecond timestamp keeps
// each attempt's snapshot distinct.
func migrationSnapshotPath(dbPath string, coreFrom, authFrom uint64) string {
	ts := time.Now().UTC().Format("20060102T150405.000000000")
	return fmt.Sprintf("%s.pre-c%d-a%d-%s", dbPath, coreFrom, authFrom, ts)
}

// --- Destructive-core step helper ---

// resetCorePreserving is the reusable export -> reset -> restore routine for a
// future core-domain ladder step where a targeted in-place transform is
// impossible (Requirement 3.1). It runs entirely inside the step's single
// write transaction, so it is crash-atomic, and preserves the irreplaceable
// core set by construction: manual subtitle_state rows (locks + manual
// history) and sync_offsets.
//
//  1. EXPORT: walk subtitle_state decoding every row FAIL-CLOSED, retaining
//     the Manual == true rows — BOTH the decoded record (for the Manual check
//     and index derivation) and the row's raw JSON bytes, so additive fields
//     written by a build this binary does not know about survive the
//     round-trip (Requirement 3.1(a): FULL raw-row preservation, not
//     "whatever this build's struct happens to model"); copy sync_offsets
//     wholesale. The fail-closed choice is deliberate: a row cannot be proven
//     non-manual without decoding it, and the pre-migration snapshot exists —
//     so one undecodable row aborts the migration. Do NOT "optimize" this to
//     a tolerant skip: that would silently drop manual locks. All retained
//     bytes are deep-copied BEFORE any mutation (bbolt slices are valid only
//     until the transaction mutates pages; DeleteBucket invalidates them).
//  2. RESET: delete and recreate ONLY the buckets named in coreBuckets, minus
//     meta. The meta bucket is NEVER dropped: the auth stamp and any unrelated
//     keys stay physically untouched; only the core-owned counters are
//     cleared (the restore rebuilds the downloads counter; the file and
//     attempt counters restart at zero against the emptied buckets). The
//     poll_state bucket is emptied like the rest, so the poll cursor falls
//     back to the documented full-scan-on-restart behavior.
//  3. RESTORE: reinsert the manual rows in old-ID order, each with a FRESH
//     surrogate id allocated via kv.NextID (putState does not allocate),
//     through the putState chokepoint — so ix_state_quad, ix_state_imported,
//     ix_state_video, and the downloads counter are rebuilt in-transaction —
//     then overwrite each restored primary VALUE with the exported raw JSON,
//     surgically rewritten to carry only the fresh id, so unknown additive
//     fields survive byte-for-byte (the index keys derive from fields the
//     raw and decoded representations share identically, so index and value
//     cannot disagree); finally re-put the copied sync_offsets. Restored
//     offsets are momentarily orphaned while subtitle_files is empty; they
//     reattach when the next scan rebuilds the file inventory.
//
// The core stamp itself is advanced by the framework in the same transaction
// (runInPlaceStep), not here.
func resetCorePreserving(tx *bolt.Tx) error {
	manualRows, err := exportManualStateRows(tx)
	if err != nil {
		return err
	}
	offsets, err := exportSyncOffsets(tx)
	if err != nil {
		return err
	}
	if err := resetCoreBuckets(tx); err != nil {
		return err
	}
	return restoreCoreIrreplaceable(tx, manualRows, offsets)
}

// manualRow is one exported manual subtitle_state row: the decoded record
// (for the Manual check and canonical index derivation) PAIRED with the row's
// deep-copied raw JSON value, so the restore can preserve additive JSON
// fields this build's stateRec does not model (Requirement 3.1(a)).
type manualRow struct {
	raw []byte
	rec stateRec
}

// exportManualStateRows decodes every subtitle_state row fail-closed and
// returns the manual rows in old-ID order (the primary's be64 key order),
// each carrying both its decoded record and its raw value bytes. The raw
// bytes are deep-copied before any mutation (bbolt slices are valid only
// until the transaction mutates pages); the decoded structs are deep copies
// by construction (JSON decoding allocates fresh memory for every field).
func exportManualStateRows(tx *bolt.Tx) ([]manualRow, error) {
	sb := tx.Bucket([]byte(bucketSubtitleState))
	if sb == nil {
		return nil, nil // no bucket, nothing to preserve
	}
	var rows []manualRow
	err := sb.ForEach(func(k, v []byte) error {
		var sr stateRec
		if derr := kv.Decode(v, &sr); derr != nil {
			return fmt.Errorf("boltstore: reset-preserving export: undecodable subtitle_state row %x (cannot prove it is not a manual lock; restore the snapshot): %w", k, derr)
		}
		if sr.Manual {
			rows = append(rows, manualRow{raw: append([]byte(nil), v...), rec: sr})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// rawKV is one deep-copied key/value pair exported before a bucket reset.
type rawKV struct{ k, v []byte }

// exportSyncOffsets copies the sync_offsets bucket wholesale, deep-copying
// every key and value before any mutation invalidates the bbolt-owned slices.
func exportSyncOffsets(tx *bolt.Tx) ([]rawKV, error) {
	ob := tx.Bucket([]byte(bucketSyncOffsets))
	if ob == nil {
		return nil, nil
	}
	var out []rawKV
	err := ob.ForEach(func(k, v []byte) error {
		out = append(out, rawKV{
			k: append([]byte(nil), k...),
			v: append([]byte(nil), v...),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// resetCoreBuckets deletes and recreates every core bucket EXCEPT meta, then
// clears the core-owned counters inside meta. The meta bucket itself — and
// with it the auth stamp and any unrelated keys — is never dropped.
func resetCoreBuckets(tx *bolt.Tx) error {
	for _, name := range coreBuckets {
		if string(name) == bucketMeta {
			continue // meta is never dropped
		}
		if err := tx.DeleteBucket(name); err != nil && !errors.Is(err, bolterrors.ErrBucketNotFound) {
			return fmt.Errorf("boltstore: reset core bucket %q: %w", name, err)
		}
		if _, err := tx.CreateBucket(name); err != nil {
			return fmt.Errorf("boltstore: recreate core bucket %q: %w", name, err)
		}
	}
	mb := tx.Bucket([]byte(bucketMeta))
	if mb == nil {
		return fmt.Errorf("boltstore: reset core buckets: meta bucket %q not found", bucketMeta)
	}
	for _, key := range coreCounterKeys() {
		if err := mb.Delete(key); err != nil {
			return fmt.Errorf("boltstore: clear core counter %q: %w", key, err)
		}
	}
	return nil
}

// restoreCoreIrreplaceable reinserts the exported manual rows (fresh ids, old
// order) and re-puts the copied sync_offsets. Each row goes through the
// putState chokepoint FIRST — so every index and the downloads counter
// rebuild in-transaction from the decoded record — and its primary value is
// then overwritten with the raw-preserving rewrite (the exported raw JSON
// with only the id field replaced), so additive fields unknown to this build
// survive. The order matters: putState's reindex step decodes the PRIOR
// value, so the raw overwrite must come after it, and both representations
// carry identical index-relevant fields, so key and value cannot disagree.
func restoreCoreIrreplaceable(tx *bolt.Tx, manualRows []manualRow, offsets []rawKV) error {
	sb := tx.Bucket([]byte(bucketSubtitleState))
	if sb == nil {
		return errors.New("boltstore: restore: subtitle_state bucket not found")
	}
	for i := range manualRows {
		if err := restoreManualRow(tx, sb, &manualRows[i]); err != nil {
			return err
		}
	}
	ob := tx.Bucket([]byte(bucketSyncOffsets))
	if ob == nil {
		return errors.New("boltstore: restore: sync_offsets bucket not found")
	}
	for i := range offsets {
		if err := ob.Put(offsets[i].k, offsets[i].v); err != nil {
			return fmt.Errorf("boltstore: restore sync offset: %w", err)
		}
	}
	return nil
}

// restoreManualRow reinserts one exported manual row: allocate the fresh id,
// rebuild indexes and counter through putState, then swap the primary value
// for the raw-preserving rewrite carrying the same fresh id.
func restoreManualRow(tx *bolt.Tx, sb *bolt.Bucket, row *manualRow) error {
	id, key, err := kv.NextID(sb)
	if err != nil {
		return err
	}
	row.rec.ID = int64(id) //nolint:gosec // G115: positive surrogate id from a fresh monotonic sequence
	if perr := putState(tx, &row.rec); perr != nil {
		return perr
	}
	patched, err := rewriteStateID(row.raw, row.rec.ID)
	if err != nil {
		return fmt.Errorf("boltstore: restore manual row %x: %w", key, err)
	}
	if err := sb.Put(key, patched); err != nil {
		return fmt.Errorf("boltstore: restore manual row %x raw value: %w", key, err)
	}
	return nil
}

// rewriteStateID returns raw — one subtitle_state row's JSON object — with
// ONLY its top-level "id" field replaced by newID. Every other field, known
// to this build or not, is preserved byte-for-byte in its original order: the
// object is re-emitted token by token with each value captured verbatim as
// json.RawMessage, never round-tripped through a struct or map that would
// drop or reorder fields. A row missing an "id" field (never produced by this
// build's encoder) gains one at the end.
func rewriteStateID(raw []byte, newID int64) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("rewrite id: %w", err)
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("rewrite id: not a JSON object (leading token %v)", tok)
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	idValue := strconv.FormatInt(newID, 10)
	sawID := false
	for dec.More() {
		isID, err := rewriteNextField(dec, &buf, idValue)
		if err != nil {
			return nil, err
		}
		sawID = sawID || isID
	}
	if !sawID {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		buf.WriteString(`"id":`)
		buf.WriteString(idValue)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// rewriteNextField re-emits the decoder's next object field into buf:
// the key re-encoded, then either idValue (for the top-level "id" field) or
// the value's verbatim raw bytes. It reports whether the field was the id.
func rewriteNextField(dec *json.Decoder, buf *bytes.Buffer, idValue string) (isID bool, err error) {
	keyTok, err := dec.Token()
	if err != nil {
		return false, fmt.Errorf("rewrite id: read key: %w", err)
	}
	key, ok := keyTok.(string)
	if !ok {
		return false, fmt.Errorf("rewrite id: non-string object key %v", keyTok)
	}
	var val json.RawMessage
	if derr := dec.Decode(&val); derr != nil {
		return false, fmt.Errorf("rewrite id: read %q value: %w", key, derr)
	}
	if buf.Len() > 1 {
		buf.WriteByte(',')
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return false, fmt.Errorf("rewrite id: encode key %q: %w", key, err)
	}
	buf.Write(keyJSON)
	buf.WriteByte(':')
	if key == "id" {
		buf.WriteString(idValue)
		return true, nil
	}
	buf.Write(val)
	return false, nil
}
