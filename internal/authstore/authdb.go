package authstore

import (
	"errors"
	"sync"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/store/kv"
	"go.etcd.io/bbolt"
)

// Compile-time assertion: *Store implements the composite AuthStore interface
// (UserStore + SessionPersister + PasskeyStore + KeyStore + OIDCStateStore).
var _ AuthStore = (*Store)(nil)

// oidcRec is an in-flight OIDC login state held only in memory. It is never
// persisted: an in-progress login that does not survive a restart simply
// retries.
type oidcRec struct {
	expiresAt    time.Time
	nonce        string
	codeVerifier string
	redirectURI  string
}

// Store is the auth persistence layer. Durable state (users, passkeys, API
// keys) lives in bbolt buckets in the SAME file as the core store; ephemeral
// state (sessions, OIDC login states) lives only in the in-memory maps below.
//
// The *bbolt.DB handle is SHARED with the core store (internal/boltstore),
// which owns it. The auth store never closes the handle — its Close only stops
// the background sweeper.
//
// Bucket bootstrap is owned by the core store, which creates the auth buckets
// alongside the core buckets in one Update on Open.
type Store struct {
	db       *bbolt.DB                // shared with the core store (one file, one lock)
	sessions map[string]*auth.Session // ephemeral, never persisted
	oidc     map[string]*oidcRec      // ephemeral, never persisted
	stop     chan struct{}            // sweeper shutdown signal (closed by Close)
	done     chan struct{}            // closed by the sweeper goroutine on exit; nil until Open

	// Sweeper timings, set in New to the package defaults (sweeper.go). They
	// live on the Store rather than as Open arguments because Open takes none
	// (fixed by the AuthStore wiring); the composition root MAY override them
	// with configured values without changing the New(db) signature.
	sweepInterval time.Duration
	idleTimeout   time.Duration
	absTimeout    time.Duration
	oidcTTL       time.Duration

	started   bool         // guards against a double Open (exactly one sweeper)
	closeOnce sync.Once    // guards against a double-close panic on stop
	mu        sync.RWMutex // guards the ephemeral maps and the sweeper lifecycle fields
}

// New constructs a Store over a *bbolt.DB handle shared with the core store.
// It allocates the in-memory ephemeral maps and the sweeper stop channel and
// seeds the sweeper timings with the package defaults, but does not start the
// sweeper; call Open for that.
func New(db *bbolt.DB) *Store {
	return &Store{
		db:            db,
		sessions:      make(map[string]*auth.Session),
		oidc:          make(map[string]*oidcRec),
		stop:          make(chan struct{}),
		sweepInterval: defaultSweepInterval,
		idleTimeout:   defaultSessionIdleTimeout,
		absTimeout:    defaultSessionAbsoluteTimeout,
		oidcTTL:       defaultOIDCStateTTL,
	}
}

// --- Foundation: bucket schema, durable-write, uniqueness, and decode helpers ---
//
// These are the shared primitives the durable-domain files (users.go,
// passkeys.go, apikeys.go — tasks 8.2-8.4) build on. The recipe for every
// durable auth mutation is one s.update transaction that
//
//  1. checks the relevant uniqueness index with uniqueCheck,
//  2. allocates a surrogate id with nextAuthID when the record is
//     surrogate-keyed, and
//  3. writes the record and maintains its index buckets via kv.PutIndexed.
//
// Because a bbolt Update commit fsyncs, a mutation that returns nil is
// crash-durable on commit and a failed Update rolls back atomically leaving no
// partial primary/index state (Requirements 9.1, 9.2).

// Auth bucket names. The CORE store (internal/boltstore) is the single
// bucket-schema OWNER: boltstore.Open bootstraps every primary and index bucket
// below — alongside the core buckets — in one Update (spec task 2.3). The auth
// store only ever SHARES the already-bootstrapped handle, so it never creates a
// bucket here. These constants MUST hold the same string values as the auth
// bucket names in internal/boltstore/keys.go; the foundation test cross-checks
// that by asserting every name here exists in a boltstore-opened file.
const (
	bucketAuthUsers    = "auth_users"    // be64(id) -> userRec
	bucketAuthPasskeys = "auth_passkeys" //nolint:gosec // G101: bbolt bucket name, not a credential
	bucketAuthAPIKeys  = "auth_api_keys" //nolint:gosec // G101: bbolt bucket name, not a credential

	bucketIxUserName    = "ix_user_name"    // lower(username) -> be64(user_id)
	bucketIxUserOIDC    = "ix_user_oidc"    // issuer 0x00 sub -> be64(user_id)
	bucketIxPasskeyUser = "ix_passkey_user" // be64(user_id) 0x00 credential_id -> (empty)
	bucketIxAPIKeyUser  = "ix_apikey_user"  //nolint:gosec // G101: bbolt bucket name, not a credential
)

// authBuckets lists every auth primary and index bucket the core store
// bootstraps and the auth store uses. Kept adjacent to the name constants so a
// drift between this list and the owner's schema is caught by the foundation
// test.
var authBuckets = []string{
	bucketAuthUsers, bucketAuthPasskeys, bucketAuthAPIKeys,
	bucketIxUserName, bucketIxUserOIDC, bucketIxPasskeyUser, bucketIxAPIKeyUser,
}

// errConflict is returned by uniqueCheck when a uniqueness index already holds
// the candidate key (duplicate username, (issuer, sub), credential id, or
// API-key hash). The durable-domain methods (tasks 8.2-8.4) surface it on the
// create/update paths (Requirement 9.3).
var errConflict = errors.New("authstore: unique constraint violation")

// update runs fn in a single bbolt read-write transaction on the SHARED handle.
// Every durable auth mutation goes through here, so the whole (uniqueness
// check, put, index maintenance) sequence is one all-or-nothing transaction
// that is crash-durable on commit; a failed Update rolls back atomically
// leaving no partial state (Requirement 9.2).
func (s *Store) update(fn func(tx *bbolt.Tx) error) error {
	return s.db.Update(fn)
}

// view runs fn in a single bbolt read-only transaction on the shared handle.
// Read transactions are kept short and never perform filesystem I/O while open
// (Requirement 13.3).
func (s *Store) view(fn func(tx *bbolt.Tx) error) error {
	return s.db.View(fn)
}

// authBucket returns the named bucket from tx. ok is false when the bucket is
// absent, which the read paths treat as an empty first boot (Requirement 9.6)
// rather than an error — an absent bucket simply has no rows. Writes go through
// kv.PutIndexed, which returns a descriptive "bucket not found" error if
// the owner (boltstore) somehow did not bootstrap; in production that never
// happens because boltstore.Open always bootstraps before authstore.New runs.
func authBucket(tx *bbolt.Tx, name string) (b *bbolt.Bucket, ok bool) {
	b = tx.Bucket([]byte(name))
	return b, b != nil
}

// uniqueCheck enforces a uniqueness constraint inside a write transaction: it
// returns errConflict when indexBucket already holds indexKey, and nil
// otherwise. An absent index bucket is treated as empty (no conflict),
// consistent with the empty-first-boot rule. Callers run uniqueCheck BEFORE the
// put so a duplicate is rejected without writing partial state (Requirement
// 9.3).
func uniqueCheck(tx *bbolt.Tx, indexBucket string, indexKey []byte) error {
	ib, ok := authBucket(tx, indexBucket)
	if !ok {
		return nil
	}
	if ib.Get(indexKey) != nil {
		return errConflict
	}
	return nil
}

// decodeAuthRecord decodes a durable auth record value into v, FAILING CLOSED on
// a decode error (Requirement 13.4): auth records are identity- and
// credential-bearing, so an undecodable row must surface a descriptive error
// rather than be silently skipped the way a derived core bucket would be. It is
// the auth read path's single decode chokepoint; bucket names the source bucket
// for the error message.
func decodeAuthRecord[T any](bucket string, key, data []byte, v *T) error {
	// FailClosed never returns skip=true, so the boolean is discarded.
	_, err := kv.DecodeOrHandle(kv.FailClosed, bucket, key, data, v)
	return err
}

// nextAuthID allocates the next surrogate id for an auth bucket via bbolt's
// monotonic sequence. Users, passkeys, and API keys all carry a surrogate id
// (so DeleteUser / DeletePasskey / DeleteAPIKey can address and ownership-check
// a row by id) even though passkeys and API keys are PRIMARY-keyed by
// credential id / key hash. Must be called inside an update transaction.
func nextAuthID(b *bbolt.Bucket) (id int64, err error) {
	seq, _, err := kv.NextID(b)
	if err != nil {
		return 0, err
	}
	return int64(seq), nil //nolint:gosec // G115: positive surrogate id from a monotonic sequence
}
