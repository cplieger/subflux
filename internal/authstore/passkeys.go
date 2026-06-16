package authstore

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"go.etcd.io/bbolt"

	"github.com/cplieger/auth"
	boltkv "github.com/cplieger/subflux/internal/store/kv"
)

// This file holds the PasskeyStore half of AuthStore: the durable
// auth_passkeys bucket plus the ix_passkey_user index. It mirrors the old
// SQLite store/authdb/passkeys.go behaviour exactly, with one deliberate
// hardening: UpdatePasskeyAfterLogin persists a MONOTONIC sign_count
// (max(stored, incoming)) so a replayed or cloned authenticator presenting a
// stale counter cannot regress the stored value and defeat clone detection
// (Requirement 9.5, CVE-2023-45669). The old store overwrote unconditionally.
//
// auth_passkeys is PRIMARY-keyed by the raw credential_id (binary), so the
// WebAuthn login hot path — GetPasskeyByCredentialID — is a single point get.
// The surrogate id allocated from bbolt's NextSequence lives in the JSON value
// (pkRec.ID) so the id-and-owner addressed methods (RenamePasskey,
// DeletePasskey) can ownership-check a row; they resolve the credential_id by a
// user-scoped prefix walk of ix_passkey_user, which is bounded by the user's
// passkey count and inherently enforces ownership (it only ever visits rows
// owned by the supplied user id).
//
// ix_passkey_user is keyed be64(user_id) 0x00 credential_id -> (empty). The
// fixed 8-byte big-endian user-id prefix plus the 0x00 separator make the
// 9-byte prefix scan unambiguous even though credential_id is arbitrary binary
// that may itself contain NUL bytes. This EXACT layout is what users.go's
// cascadeDeleteByUser relies on (it derives the child primary key as
// indexKey[9:]), so the user-delete cascade keeps deleting passkeys correctly.
// A separate pkRec is required rather than persisting auth.PasskeyCredential
// directly: that struct marks CredentialID, PublicKey, AAGUID, RawAttestation,
// SignCount, and several flags json:"-", so a direct marshal would silently
// drop the credential material and the clone-warning flag.
//
// Every durable mutation runs in one s.update transaction (uniqueness check,
// put, index maintenance), so it is crash-durable on commit and a failure
// rolls back leaving no partial primary/index state (Requirements 9.1, 9.2).

// pkRec is the JSON value stored in the auth_passkeys bucket. It carries every
// auth.PasskeyCredential field — including the json:"-" credential material and
// the clone-warning flag — so the credential round-trips through the bbolt
// codec without loss (Requirement 9.5). The []byte fields marshal as base64.
type pkRec struct {
	CreatedAt       time.Time `json:"created_at"`
	AttestationType string    `json:"attestation_type,omitempty"`
	Transport       string    `json:"transport,omitempty"`
	Name            string    `json:"name"`
	CredentialID    []byte    `json:"credential_id"`
	PublicKey       []byte    `json:"public_key"`
	AAGUID          []byte    `json:"aaguid,omitempty"`
	RawAttestation  []byte    `json:"raw_attestation,omitempty"`
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	SignCount       uint32    `json:"sign_count"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	UserPresent     bool      `json:"user_present"`
	UserVerified    bool      `json:"user_verified"`
	CloneWarning    bool      `json:"clone_warning"`
}

// toPasskeyRec projects an auth.PasskeyCredential into its persisted form.
func toPasskeyRec(c *auth.PasskeyCredential) pkRec {
	return pkRec{
		CreatedAt:       c.CreatedAt,
		AttestationType: c.AttestationType,
		Transport:       c.Transport,
		Name:            c.Name,
		CredentialID:    c.CredentialID,
		PublicKey:       c.PublicKey,
		AAGUID:          c.AAGUID,
		RawAttestation:  c.RawAttestation,
		ID:              c.ID,
		UserID:          c.UserID,
		SignCount:       c.SignCount,
		BackupEligible:  c.BackupEligible,
		BackupState:     c.BackupState,
		UserPresent:     c.UserPresent,
		UserVerified:    c.UserVerified,
		CloneWarning:    c.CloneWarning,
	}
}

// toPasskey reconstructs the auth.PasskeyCredential from its persisted form.
func (r *pkRec) toPasskey() *auth.PasskeyCredential {
	return &auth.PasskeyCredential{
		CreatedAt:       r.CreatedAt,
		AttestationType: r.AttestationType,
		Transport:       r.Transport,
		Name:            r.Name,
		CredentialID:    r.CredentialID,
		PublicKey:       r.PublicKey,
		AAGUID:          r.AAGUID,
		RawAttestation:  r.RawAttestation,
		ID:              r.ID,
		UserID:          r.UserID,
		SignCount:       r.SignCount,
		BackupEligible:  r.BackupEligible,
		BackupState:     r.BackupState,
		UserPresent:     r.UserPresent,
		UserVerified:    r.UserVerified,
		CloneWarning:    r.CloneWarning,
	}
}

// --- key builders ---

// passkeyUserPrefix builds the ix_passkey_user prefix for one user:
// be64(user_id) 0x00. The 9-byte fixed-width prefix is what bounds a
// user-scoped scan and what users.go's cascade uses; a credential_id appended
// after it is unambiguous because the separator sits at the fixed offset 8.
func passkeyUserPrefix(userID int64) []byte {
	return append(boltkv.Be64(uint64(userID)), boltkv.Sep) //nolint:gosec // G115: positive surrogate id
}

// passkeyUserIndexKey builds the full ix_passkey_user key:
// be64(user_id) 0x00 credential_id.
func passkeyUserIndexKey(userID int64, credID []byte) []byte {
	prefix := passkeyUserPrefix(userID)
	out := make([]byte, 0, len(prefix)+len(credID))
	out = append(out, prefix...)
	out = append(out, credID...)
	return out
}

// CreatePasskey inserts a new WebAuthn credential, rejecting a duplicate
// credential id with errConflict before any write (Requirement 9.3), and sets
// the surrogate ID on the supplied struct. CreatedAt is stamped to now when
// zero, mirroring the SQLite CURRENT_TIMESTAMP default. The primary row and its
// ix_passkey_user entry are written together in one Update, crash-durable on
// commit (Requirements 9.1, 9.2).
func (s *Store) CreatePasskey(_ context.Context, cred *auth.PasskeyCredential) error {
	if cred == nil {
		return fmt.Errorf("authstore: CreatePasskey: nil credential")
	}
	if len(cred.CredentialID) == 0 {
		return fmt.Errorf("authstore: CreatePasskey: empty credential id")
	}
	err := s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return fmt.Errorf("authstore: %q bucket not found", bucketAuthPasskeys)
		}
		// credential_id IS the primary key, so a present row is the duplicate.
		if pb.Get(cred.CredentialID) != nil {
			return errConflict
		}

		id, err := nextAuthID(pb)
		if err != nil {
			return err
		}
		if cred.CreatedAt.IsZero() {
			cred.CreatedAt = time.Now().UTC()
		}
		cred.ID = id

		rec := toPasskeyRec(cred)
		enc, err := boltkv.Encode(&rec)
		if err != nil {
			return err
		}
		if err := pb.Put(cred.CredentialID, enc); err != nil {
			return fmt.Errorf("authstore: put passkey: %w", err)
		}
		if err := idxPut(tx, bucketIxPasskeyUser, passkeyUserIndexKey(cred.UserID, cred.CredentialID), nil); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.Info("passkey registered", "user_id", cred.UserID, "name", cred.Name)
	return nil
}

// GetPasskeysByUserID returns all of a user's passkeys, ordered by creation
// time then surrogate id (matching the old store's `ORDER BY created_at`). It
// walks ix_passkey_user for the user and dereferences each credential id;
// decoding fails closed (auth bucket).
func (s *Store) GetPasskeysByUserID(_ context.Context, userID int64) ([]auth.PasskeyCredential, error) {
	var out []auth.PasskeyCredential
	err := s.view(func(tx *bbolt.Tx) error {
		ib, ok := authBucket(tx, bucketIxPasskeyUser)
		if !ok {
			return nil
		}
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		prefix := passkeyUserPrefix(userID)
		c := ib.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			credID := k[len(prefix):]
			data := pb.Get(credID)
			if data == nil {
				continue // dangling index entry; treat as absent
			}
			var rec pkRec
			if err := decodeAuthRecord(bucketAuthPasskeys, credID, data, &rec); err != nil {
				return err
			}
			out = append(out, *rec.toPasskey())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// GetPasskeyByCredentialID looks up a passkey by its credential id (the
// WebAuthn login hot path), returning (nil, nil) when not found (matching the
// old store's sql.ErrNoRows -> nil mapping). Decoding fails closed.
func (s *Store) GetPasskeyByCredentialID(_ context.Context, credID []byte) (*auth.PasskeyCredential, error) {
	var out *auth.PasskeyCredential
	err := s.view(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		data := pb.Get(credID)
		if data == nil {
			return nil
		}
		var rec pkRec
		if err := decodeAuthRecord(bucketAuthPasskeys, credID, data, &rec); err != nil {
			return err
		}
		out = rec.toPasskey()
		return nil
	})
	return out, err
}

// UpdatePasskeyAfterLogin persists the post-login authenticator flags and a
// durable, MONOTONIC sign_count: the stored value is set to
// max(stored, incoming) so a lower incoming counter (a replay or a cloned
// authenticator) can never regress it and defeat clone detection (Requirement
// 9.5, CVE-2023-45669). A missing credential is a no-op returning nil, matching
// the SQLite UPDATE that affects zero rows. The single-key Put is crash-durable
// on commit.
func (s *Store) UpdatePasskeyAfterLogin(_ context.Context, credID []byte, signCount uint32, flags auth.PasskeyFlags) error {
	return s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		data := pb.Get(credID)
		if data == nil {
			return nil // no such credential
		}
		var rec pkRec
		if err := decodeAuthRecord(bucketAuthPasskeys, credID, data, &rec); err != nil {
			return err
		}

		if signCount > rec.SignCount {
			rec.SignCount = signCount
		}
		rec.BackupEligible = flags.BackupEligible
		rec.BackupState = flags.BackupState
		rec.UserPresent = flags.UserPresent
		rec.UserVerified = flags.UserVerified
		rec.CloneWarning = flags.CloneWarning

		enc, err := boltkv.Encode(&rec)
		if err != nil {
			return err
		}
		if err := pb.Put(credID, enc); err != nil {
			return fmt.Errorf("authstore: put passkey: %w", err)
		}
		return nil
	})
}

// RenamePasskey sets the friendly name of the passkey identified by surrogate
// id, but only when it belongs to userID (Requirement 16.4). It resolves the
// credential id by a user-scoped walk of ix_passkey_user, so a passkey owned by
// a different user is never visited and cannot be renamed. A non-matching
// (id, userID) is a no-op returning nil, matching the SQLite UPDATE affecting
// zero rows.
func (s *Store) RenamePasskey(_ context.Context, id, userID int64, name string) error {
	return s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		credID, rec, found, err := s.findUserPasskeyByID(tx, userID, id)
		if err != nil || !found {
			return err
		}
		rec.Name = name
		enc, err := boltkv.Encode(rec)
		if err != nil {
			return err
		}
		if err := pb.Put(credID, enc); err != nil {
			return fmt.Errorf("authstore: put passkey: %w", err)
		}
		return nil
	})
}

// DeletePasskey removes the passkey identified by surrogate id, but only when
// it belongs to userID (Requirement 16.4). Like RenamePasskey it resolves the
// credential id via a user-scoped index walk, so it can only ever delete the
// supplied user's own passkey. It deletes the primary row and its
// ix_passkey_user entry in one Update; a non-matching (id, userID) is a no-op
// returning nil.
func (s *Store) DeletePasskey(_ context.Context, id, userID int64) error {
	var deleted bool
	err := s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		credID, _, found, err := s.findUserPasskeyByID(tx, userID, id)
		if err != nil || !found {
			return err
		}
		if err := pb.Delete(credID); err != nil {
			return fmt.Errorf("authstore: delete passkey: %w", err)
		}
		if err := idxDelete(tx, bucketIxPasskeyUser, passkeyUserIndexKey(userID, credID)); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return err
	}
	if deleted {
		slog.Info("passkey deleted", "passkey_id", id, "user_id", userID)
	}
	return nil
}

// PasskeyCountForUser returns the number of passkeys registered for a user
// (Requirement 16.7). It is a key-only prefix count over ix_passkey_user with
// no primary dereference.
func (s *Store) PasskeyCountForUser(_ context.Context, userID int64) (int, error) {
	count := 0
	err := s.view(func(tx *bbolt.Tx) error {
		ib, ok := authBucket(tx, bucketIxPasskeyUser)
		if !ok {
			return nil
		}
		prefix := passkeyUserPrefix(userID)
		c := ib.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			count++
		}
		return nil
	})
	return count, err
}

// findUserPasskeyByID resolves the credential id and decoded record of the
// passkey with surrogate id owned by userID, walking only that user's
// ix_passkey_user entries (so ownership is enforced by construction). It
// returns found=false with no error when the user has no passkey with that id.
// The returned credID is copied so it is safe to use for a Put/Delete after the
// cursor advances. Must be called inside a transaction. Decoding fails closed.
func (s *Store) findUserPasskeyByID(tx *bbolt.Tx, userID, id int64) (credID []byte, rec *pkRec, found bool, err error) {
	ib, ok := authBucket(tx, bucketIxPasskeyUser)
	if !ok {
		return nil, nil, false, nil
	}
	pb, ok := authBucket(tx, bucketAuthPasskeys)
	if !ok {
		return nil, nil, false, nil
	}
	prefix := passkeyUserPrefix(userID)
	c := ib.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		cid := k[len(prefix):]
		data := pb.Get(cid)
		if data == nil {
			continue // dangling index entry
		}
		var r pkRec
		if derr := decodeAuthRecord(bucketAuthPasskeys, cid, data, &r); derr != nil {
			return nil, nil, false, derr
		}
		if r.ID == id {
			return append([]byte(nil), cid...), &r, true, nil
		}
	}
	return nil, nil, false, nil
}
