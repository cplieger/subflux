package authstore

import (
	"errors"
	"fmt"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/store/kv"
	"go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// This file is the auth side of the schema-migration boundary. The migration
// LADDER and its orchestration live in internal/boltstore (migrate.go /
// migrations.go), but this package's record types and index/sequence helpers
// are unexported by design, so boltstore cannot implement a typed auth
// restore. A destructive auth-domain ladder step is therefore implemented
// HERE — a future step is an exported func (e.g. MigrationV1toV2(tx) error)
// composed over ResetPreserving below — and registered in boltstore's auth
// ladder as a plain transaction callback, keeping the orchestrator
// domain-blind.
//
// By construction the helper touches ONLY the seven auth buckets named by
// internal/store/buckets: core buckets and the meta bucket — and with it both
// schema stamps — stay physically untouched. The framework advances the auth
// stamp itself, in the same transaction as the step.

// Rowset is the typed snapshot of the durable auth domain handed to a
// destructive migration step's transform: every user, passkey, and API key,
// fully decoded (including the json:"-" credential material the persisted
// records carry). The transform mutates it in place — the new-schema rewrite —
// and the restore reinserts exactly what the transform left, preserving
// surrogate IDs (and with them the passkey/API-key -> user ownership links),
// rebuilding every uniqueness index, and keeping bucket sequences collision
// free.
type Rowset struct {
	Users    []auth.User
	Passkeys []auth.PasskeyCredential
	Keys     []auth.Key
}

// RowsetTransform rewrites the exported auth rowset between export and
// restore. It is REQUIRED: ResetPreserving refuses a nil transform, so a
// destructive step must state its rewrite explicitly — pass a func that
// returns nil unchanged to keep every row as-is.
type RowsetTransform func(*Rowset) error

// ResetPreserving is the auth-domain export -> reset -> restore routine for a
// destructive migration step, run entirely inside the step's single write
// transaction (crash-atomic):
//
//  1. EXPORT: decode every user, passkey, and API key FAIL-CLOSED (an
//     undecodable identity or credential record aborts the migration — the
//     pre-migration snapshot exists) and capture each primary bucket's
//     sequence. Decoding deep-copies all record data out of bbolt-owned
//     memory, so the rowset survives the bucket deletion that follows.
//  2. TRANSFORM: apply the step's explicit rowset transform.
//  3. RESET: delete and recreate ONLY the seven auth buckets named by
//     internal/store/buckets (primaries and indexes).
//  4. RESTORE: reinsert every row preserving its surrogate ID and primary key
//     (credential_id / key_hash), re-derive the ix_user_name, ix_user_oidc,
//     ix_passkey_user, and ix_apikey_user index entries, enforce the same
//     uniqueness constraints as the live write paths (a transform that
//     produced a duplicate aborts the whole transaction), refuse a
//     non-positive or family-duplicate surrogate id in EVERY credential
//     family (users, passkeys, API keys — id-addressed methods and the
//     sequence bump rely on positive unique ids), refuse a passkey or API
//     key whose owner is not in the restored user set (a dangling credential
//     must never survive silently), and restore each bucket's sequence
//     (bumped past the highest restored ID so future allocations cannot
//     collide).
//
// The auth schema stamp is advanced by the boltstore framework in the same
// transaction, not here.
func ResetPreserving(tx *bbolt.Tx, transform RowsetTransform) error {
	if transform == nil {
		return errors.New("authstore: ResetPreserving: nil rowset transform (a destructive auth step must state its rewrite explicitly; pass a transform returning nil to keep rows unchanged)")
	}
	rs, seqs, err := exportAuthRowset(tx)
	if err != nil {
		return err
	}
	if err := transform(rs); err != nil {
		return fmt.Errorf("authstore: ResetPreserving: rowset transform: %w", err)
	}
	if err := resetAuthBuckets(tx); err != nil {
		return err
	}
	return restoreAuthRowset(tx, rs, seqs)
}

// exportAuthRowset decodes the three durable auth primaries fail-closed and
// captures their bucket sequences. Absent buckets export as empty (nothing to
// preserve).
func exportAuthRowset(tx *bbolt.Tx) (*Rowset, map[string]uint64, error) {
	rs := &Rowset{}
	seqs := make(map[string]uint64, 3)
	if err := exportBucket(tx, bucketAuthUsers, seqs, func(rec *userRec) {
		rs.Users = append(rs.Users, *rec.toUser())
	}); err != nil {
		return nil, nil, err
	}
	if err := exportBucket(tx, bucketAuthPasskeys, seqs, func(rec *pkRec) {
		rs.Passkeys = append(rs.Passkeys, *rec.toPasskey())
	}); err != nil {
		return nil, nil, err
	}
	if err := exportBucket(tx, bucketAuthAPIKeys, seqs, func(rec *keyRec) {
		rs.Keys = append(rs.Keys, *rec.toKey())
	}); err != nil {
		return nil, nil, err
	}
	return rs, seqs, nil
}

// exportBucket walks one auth primary bucket, decoding every record
// fail-closed into R and handing it to collect, and records the bucket's
// sequence in seqs. An absent bucket contributes nothing.
func exportBucket[R any](tx *bbolt.Tx, bucket string, seqs map[string]uint64, collect func(*R)) error {
	b, ok := authBucket(tx, bucket)
	if !ok {
		return nil
	}
	seqs[bucket] = b.Sequence()
	return b.ForEach(func(k, v []byte) error {
		var rec R
		if derr := decodeAuthRecord(bucket, k, v, &rec); derr != nil {
			return derr
		}
		collect(&rec)
		return nil
	})
}

// resetAuthBuckets deletes and recreates exactly the seven auth buckets named
// by the shared internal/store/buckets owner. Core buckets and meta are
// untouched by construction — this loop can only ever name auth buckets.
func resetAuthBuckets(tx *bbolt.Tx) error {
	for _, name := range authBuckets {
		if err := tx.DeleteBucket([]byte(name)); err != nil && !errors.Is(err, bolterrors.ErrBucketNotFound) {
			return fmt.Errorf("authstore: reset auth bucket %q: %w", name, err)
		}
		if _, err := tx.CreateBucket([]byte(name)); err != nil {
			return fmt.Errorf("authstore: recreate auth bucket %q: %w", name, err)
		}
	}
	return nil
}

// restoreAuthRowset reinserts the (transformed) rowset into the freshly reset
// buckets: users first (building the owner set), then the owned passkeys and
// API keys. Every write re-derives its index entries and enforces the same
// uniqueness the live write paths do; any violation errors, aborting the
// step's whole transaction.
func restoreAuthRowset(tx *bbolt.Tx, rs *Rowset, seqs map[string]uint64) error {
	owners, err := restoreUsers(tx, rs.Users, seqs[bucketAuthUsers])
	if err != nil {
		return err
	}
	if err := restorePasskeys(tx, rs.Passkeys, owners, seqs[bucketAuthPasskeys]); err != nil {
		return err
	}
	return restoreAPIKeys(tx, rs.Keys, owners, seqs[bucketAuthAPIKeys])
}

// restoreUsers reinserts every user at its preserved surrogate ID, rebuilds
// ix_user_name (uniqueness-checked) and ix_user_oidc (when the user carries an
// OIDC identity, uniqueness-checked), restores the bucket sequence, and
// returns the restored ID set for ownership validation.
func restoreUsers(tx *bbolt.Tx, users []auth.User, seq uint64) (map[int64]struct{}, error) {
	ub := tx.Bucket([]byte(bucketAuthUsers))
	owners := make(map[int64]struct{}, len(users))
	for i := range users {
		if err := restoreOneUser(tx, ub, &users[i]); err != nil {
			return nil, err
		}
		owners[users[i].ID] = struct{}{}
	}
	if err := restoreSequence(ub, seq, maxUserID(users)); err != nil {
		return nil, fmt.Errorf("authstore: restore %q sequence: %w", bucketAuthUsers, err)
	}
	return owners, nil
}

// restoreOneUser validates and reinserts a single user row with its
// ix_user_name and (when present) ix_user_oidc entries, enforcing the same
// uniqueness the live create path does.
func restoreOneUser(tx *bbolt.Tx, ub *bbolt.Bucket, u *auth.User) error {
	if u.ID <= 0 {
		return fmt.Errorf("authstore: restore user %q: invalid surrogate id %d (ownership links require preserved ids)", u.Username, u.ID)
	}
	key := userKey(u.ID)
	if ub.Get(key) != nil {
		return fmt.Errorf("authstore: restore user %q: duplicate surrogate id %d: %w", u.Username, u.ID, errConflict)
	}
	if err := uniqueCheck(tx, bucketIxUserName, userNameIndexKey(u.Username)); err != nil {
		return fmt.Errorf("authstore: restore user %q: duplicate username: %w", u.Username, err)
	}
	rec := toUserRec(u)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	if err := ub.Put(key, enc); err != nil {
		return fmt.Errorf("authstore: restore user: %w", err)
	}
	if err := idxPut(tx, bucketIxUserName, userNameIndexKey(u.Username), key); err != nil {
		return err
	}
	if u.OIDCSub == "" {
		return nil
	}
	oidcKey := userOIDCIndexKey(u.OIDCIssuer, u.OIDCSub)
	if err := uniqueCheck(tx, bucketIxUserOIDC, oidcKey); err != nil {
		return fmt.Errorf("authstore: restore user %q: duplicate OIDC identity: %w", u.Username, err)
	}
	return idxPut(tx, bucketIxUserOIDC, oidcKey, key)
}

// restorePasskeys reinserts every passkey at its credential-id primary key,
// refusing invalid or duplicate surrogate ids, duplicate credential ids, and
// unowned credentials, rebuilds ix_passkey_user, and restores the bucket
// sequence. The surrogate-id guards mirror the user restore: the
// id-and-owner-addressed methods (RenamePasskey, DeletePasskey) and the
// sequence bump both rely on every restored id being positive and unique
// within the family, so a transform that corrupts one aborts before any Put.
func restorePasskeys(tx *bbolt.Tx, creds []auth.PasskeyCredential, owners map[int64]struct{}, seq uint64) error {
	pb := tx.Bucket([]byte(bucketAuthPasskeys))
	seenIDs := make(map[int64]struct{}, len(creds))
	var maxID int64
	for i := range creds {
		c := &creds[i]
		if err := restoreOnePasskey(tx, pb, c, owners, seenIDs); err != nil {
			return err
		}
		maxID = max(maxID, c.ID)
	}
	if err := restoreSequence(pb, seq, maxID); err != nil {
		return fmt.Errorf("authstore: restore %q sequence: %w", bucketAuthPasskeys, err)
	}
	return nil
}

// restoreOnePasskey validates and reinserts a single passkey row with its
// ix_passkey_user entry: surrogate id positive and unique within the family,
// credential id present and unique, owner in the restored user set.
func restoreOnePasskey(tx *bbolt.Tx, pb *bbolt.Bucket, c *auth.PasskeyCredential, owners, seenIDs map[int64]struct{}) error {
	if c.ID <= 0 {
		return fmt.Errorf("authstore: restore passkey %q (user %d): invalid surrogate id %d (id-addressed rename/delete and the sequence bump require positive preserved ids)", c.Name, c.UserID, c.ID)
	}
	if _, dup := seenIDs[c.ID]; dup {
		return fmt.Errorf("authstore: restore passkey %q: duplicate surrogate id %d: %w", c.Name, c.ID, errConflict)
	}
	seenIDs[c.ID] = struct{}{}
	if len(c.CredentialID) == 0 {
		return fmt.Errorf("authstore: restore passkey %q (user %d): empty credential id", c.Name, c.UserID)
	}
	if _, ok := owners[c.UserID]; !ok {
		return fmt.Errorf("authstore: restore passkey %q: owner user %d is not in the restored rowset (a dangling credential must not survive a migration)", c.Name, c.UserID)
	}
	if pb.Get(c.CredentialID) != nil {
		return fmt.Errorf("authstore: restore passkey %q: duplicate credential id: %w", c.Name, errConflict)
	}
	rec := toPasskeyRec(c)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	if err := pb.Put(c.CredentialID, enc); err != nil {
		return fmt.Errorf("authstore: restore passkey: %w", err)
	}
	return idxPut(tx, bucketIxPasskeyUser, passkeyUserIndexKey(c.UserID, c.CredentialID), nil)
}

// restoreAPIKeys reinserts every API key at its key-hash primary key, refusing
// invalid or duplicate surrogate ids, duplicate hashes, and unowned keys,
// rebuilds ix_apikey_user, and restores the bucket sequence. The surrogate-id
// guards match restorePasskeys: DeleteAPIKey addresses rows by (id, user) and
// the sequence bump assumes positive unique ids, so corruption aborts before
// any Put.
func restoreAPIKeys(tx *bbolt.Tx, keys []auth.Key, owners map[int64]struct{}, seq uint64) error {
	kb := tx.Bucket([]byte(bucketAuthAPIKeys))
	seenIDs := make(map[int64]struct{}, len(keys))
	var maxID int64
	for i := range keys {
		k := &keys[i]
		if err := restoreOneAPIKey(tx, kb, k, owners, seenIDs); err != nil {
			return err
		}
		maxID = max(maxID, k.ID)
	}
	if err := restoreSequence(kb, seq, maxID); err != nil {
		return fmt.Errorf("authstore: restore %q sequence: %w", bucketAuthAPIKeys, err)
	}
	return nil
}

// restoreOneAPIKey validates and reinserts a single API-key row with its
// ix_apikey_user entry: surrogate id positive and unique within the family,
// key hash present and unique, owner in the restored user set.
func restoreOneAPIKey(tx *bbolt.Tx, kb *bbolt.Bucket, k *auth.Key, owners, seenIDs map[int64]struct{}) error {
	if k.ID <= 0 {
		return fmt.Errorf("authstore: restore api key %q (user %d): invalid surrogate id %d (id-addressed delete and the sequence bump require positive preserved ids)", k.Label, k.UserID, k.ID)
	}
	if _, dup := seenIDs[k.ID]; dup {
		return fmt.Errorf("authstore: restore api key %q: duplicate surrogate id %d: %w", k.Label, k.ID, errConflict)
	}
	seenIDs[k.ID] = struct{}{}
	if k.KeyHash == "" {
		return fmt.Errorf("authstore: restore api key %q (user %d): empty key hash", k.Label, k.UserID)
	}
	if _, ok := owners[k.UserID]; !ok {
		return fmt.Errorf("authstore: restore api key %q: owner user %d is not in the restored rowset (a dangling credential must not survive a migration)", k.Label, k.UserID)
	}
	hashKey := []byte(k.KeyHash)
	if kb.Get(hashKey) != nil {
		return fmt.Errorf("authstore: restore api key %q: duplicate key hash: %w", k.Label, errConflict)
	}
	rec := toKeyRec(k)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	if err := kb.Put(hashKey, enc); err != nil {
		return fmt.Errorf("authstore: restore api key: %w", err)
	}
	return idxPut(tx, bucketIxAPIKeyUser, apiKeyUserIndexKey(k.UserID, k.KeyHash), nil)
}

// restoreSequence restores a recreated bucket's NextSequence high-water mark:
// the captured pre-reset sequence, bumped past the highest restored surrogate
// id in case a transform introduced rows above it, so a future allocation can
// never collide with a restored id.
func restoreSequence(b *bbolt.Bucket, captured uint64, maxRestoredID int64) error {
	seq := captured
	if maxRestoredID > 0 {
		seq = max(seq, uint64(maxRestoredID))
	}
	return b.SetSequence(seq)
}

// maxUserID returns the highest surrogate id in the restored user set (0 when
// empty), for the sequence bump.
func maxUserID(users []auth.User) int64 {
	var m int64
	for i := range users {
		m = max(m, users[i].ID)
	}
	return m
}
