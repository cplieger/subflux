package authstore

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/store/kv"
	"go.etcd.io/bbolt"
)

// This file holds the PasskeyStore half of AuthStore: the durable
// auth_passkeys bucket plus the ix_passkey_user index. It mirrors the old
// SQLite store/authdb/passkeys.go behaviour exactly, with one deliberate
// hardening: UpdatePasskeyAfterLogin persists a MONOTONIC sign_count
// (max(stored, incoming)) so a replayed or cloned authenticator presenting a
// stale counter cannot regress the stored value and defeat clone detection
// (CVE-2023-45669). The old store overwrote unconditionally.
//
// auth_passkeys is PRIMARY-keyed by the raw credential_id (binary), so the
// WebAuthn login hot path — PasskeyByCredentialID — is a single point get.
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
// rolls back leaving no partial primary/index state.

// pkRec is the JSON value stored in the auth_passkeys bucket. It carries every
// auth.PasskeyCredential field — including the json:"-" credential material and
// the clone-warning flag — so the credential round-trips through the bbolt
// codec without loss. The []byte fields marshal as base64.
type pkRec struct {
	CreatedAt time.Time `json:"created_at"`

	// Discoverable is the credProps.rk output. A nil pointer means the client
	// reported nothing, which the library treats as distinct from a reported
	// false, so the pointer is persisted rather than flattened to a bool.
	Discoverable *bool `json:"discoverable,omitempty"`

	AttestationType string `json:"attestation_type,omitempty"`

	// AttestationFormat is the attestation statement format identifier. Read by
	// the library's metadata validation and by the FIDO AppID extension; both
	// paths gate out early when unset, so a dropped value is latent rather than
	// breaking, but it is still the authenticator's own report.
	AttestationFormat string `json:"attestation_format,omitempty"`

	Transport string `json:"transport,omitempty"`
	Name      string `json:"name"`

	// RPID is the relying party the credential was registered against. Stored so
	// an RP-ID change can be audited against the credentials it orphans instead
	// of silently invalidating them.
	RPID string `json:"rp_id,omitempty"`

	// Attachment is how the authenticator was attached at registration.
	Attachment string `json:"attachment,omitempty"`

	CredentialID   []byte `json:"credential_id"`
	PublicKey      []byte `json:"public_key"`
	AAGUID         []byte `json:"aaguid,omitempty"`
	RawAttestation []byte `json:"raw_attestation,omitempty"`

	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	SignCount uint32 `json:"sign_count"`

	// RawFlags is the authenticator-data flags octet and is the ONLY flag input
	// the library reads. The four booleans below are the decoded view a consumer
	// displays and filters on; dropping the octet restores every credential with
	// all-false flags, and go-webauthn then refuses any assertion whose
	// backup-eligible flag disagrees — which every synced passkey asserts.
	RawFlags uint8 `json:"raw_flags,omitempty"`

	BackupEligible bool `json:"backup_eligible"`
	BackupState    bool `json:"backup_state"`
	UserPresent    bool `json:"user_present"`
	UserVerified   bool `json:"user_verified"`
	CloneWarning   bool `json:"clone_warning"`
}

// Authenticator-data flag bit positions (WebAuthn §6.1, "Authenticator Data").
// Declared locally rather than imported from go-webauthn: the auth v5 boundary
// keeps that module out of this repo's own imports, and only these four bits are
// ever reconstructed.
const (
	flagUserPresent    uint8 = 1 << 0
	flagUserVerified   uint8 = 1 << 2
	flagBackupEligible uint8 = 1 << 3
	flagBackupState    uint8 = 1 << 4
)

// effectiveRawFlags returns the stored flags octet, rebuilt from the decoded
// booleans when the row predates the field. A real registration always sets user
// presence, so a zero octet means the row was written before the octet was
// persisted, never that the authenticator reported no flags.
//
// Only the AT and ED bits are unrecoverable, and no decision reads them. The
// branch is self-limiting: a row rewritten after this lands carries the octet,
// so it can go once no pre-field rows remain.
func (r *pkRec) effectiveRawFlags() uint8 {
	if r.RawFlags != 0 {
		return r.RawFlags
	}
	var f uint8
	if r.UserPresent {
		f |= flagUserPresent
	}
	if r.UserVerified {
		f |= flagUserVerified
	}
	if r.BackupEligible {
		f |= flagBackupEligible
	}
	if r.BackupState {
		f |= flagBackupState
	}
	return f
}

// toPasskeyRec projects an auth.PasskeyCredential into its persisted form.
func toPasskeyRec(c *auth.PasskeyCredential) pkRec {
	return pkRec{
		CreatedAt:         c.CreatedAt,
		AttestationType:   c.AttestationType,
		AttestationFormat: c.AttestationFormat,
		Transport:         c.Transport,
		Name:              c.Name,
		RPID:              c.RPID,
		Attachment:        string(c.Attachment),
		CredentialID:      c.CredentialID,
		PublicKey:         c.PublicKey,
		AAGUID:            c.AAGUID,
		RawAttestation:    c.RawAttestation,
		Discoverable:      c.Discoverable,
		ID:                c.ID,
		UserID:            c.UserID,
		SignCount:         c.SignCount,
		RawFlags:          c.RawFlags,
		BackupEligible:    c.BackupEligible,
		BackupState:       c.BackupState,
		UserPresent:       c.UserPresent,
		UserVerified:      c.UserVerified,
		CloneWarning:      c.CloneWarning,
	}
}

// toPasskey reconstructs the auth.PasskeyCredential from its persisted form.
func (r *pkRec) toPasskey() *auth.PasskeyCredential {
	return &auth.PasskeyCredential{
		CreatedAt:         r.CreatedAt,
		AttestationType:   r.AttestationType,
		AttestationFormat: r.AttestationFormat,
		Transport:         r.Transport,
		Name:              r.Name,
		RPID:              r.RPID,
		Attachment:        auth.AuthenticatorAttachment(r.Attachment),
		CredentialID:      r.CredentialID,
		PublicKey:         r.PublicKey,
		AAGUID:            r.AAGUID,
		RawAttestation:    r.RawAttestation,
		Discoverable:      r.Discoverable,
		ID:                r.ID,
		UserID:            r.UserID,
		SignCount:         r.SignCount,
		RawFlags:          r.effectiveRawFlags(),
		BackupEligible:    r.BackupEligible,
		BackupState:       r.BackupState,
		UserPresent:       r.UserPresent,
		UserVerified:      r.UserVerified,
		CloneWarning:      r.CloneWarning,
	}
}

// --- key builders ---

// passkeyUserPrefix builds the ix_passkey_user prefix for one user:
// be64(user_id) 0x00. The 9-byte fixed-width prefix is what bounds a
// user-scoped scan and what users.go's cascade uses; a credential_id appended
// after it is unambiguous because the separator sits at the fixed offset 8.
func passkeyUserPrefix(userID int64) []byte {
	return append(kv.Be64(uint64(userID)), kv.Sep) //nolint:gosec // G115: positive surrogate id
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
// credential id with errConflict before any write, and sets
// the surrogate ID on the supplied struct. CreatedAt is stamped to now when
// zero, mirroring the SQLite CURRENT_TIMESTAMP default. The primary row and its
// ix_passkey_user entry are written together in one Update, crash-durable on
// commit.
func (s *Store) CreatePasskey(_ context.Context, cred *auth.PasskeyCredential) error {
	if cred == nil {
		return errors.New("authstore: CreatePasskey: nil credential")
	}
	if len(cred.CredentialID) == 0 {
		return errors.New("authstore: CreatePasskey: empty credential id")
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
		return insertPasskey(tx, pb, cred)
	})
	if err != nil {
		return err
	}
	slog.Info("passkey registered", "user_id", cred.UserID, "name", cred.Name)
	return nil
}

// insertPasskey allocates the surrogate id, stamps CreatedAt when zero, encodes,
// and writes the credential row (primary-keyed by credential id) plus its
// ix_passkey_user entry within tx. CreatedAt defaults to now, mirroring the
// SQLite CURRENT_TIMESTAMP default.
func insertPasskey(tx *bbolt.Tx, pb *bbolt.Bucket, cred *auth.PasskeyCredential) error {
	id, err := nextAuthID(pb)
	if err != nil {
		return err
	}
	if cred.CreatedAt.IsZero() {
		cred.CreatedAt = time.Now().UTC()
	}
	cred.ID = id

	rec := toPasskeyRec(cred)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	if err := pb.Put(cred.CredentialID, enc); err != nil {
		return fmt.Errorf("authstore: put passkey: %w", err)
	}
	return idxPut(tx, bucketIxPasskeyUser, passkeyUserIndexKey(cred.UserID, cred.CredentialID), nil)
}

// PasskeysByUserID returns all of a user's passkeys, ordered by creation
// time then surrogate id (matching the old store's `ORDER BY created_at`). It
// walks ix_passkey_user for the user and dereferences each credential id;
// decoding fails closed (auth bucket).
func (s *Store) PasskeysByUserID(_ context.Context, userID int64) ([]auth.PasskeyCredential, error) {
	var out []auth.PasskeyCredential
	err := s.view(func(tx *bbolt.Tx) error {
		var ferr error
		out, ferr = collectPasskeysByUser(tx, userID)
		return ferr
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out, func(a, b auth.PasskeyCredential) int {
		return cmp.Or(
			a.CreatedAt.Compare(b.CreatedAt),
			cmp.Compare(a.ID, b.ID),
		)
	})
	return out, nil
}

// collectPasskeysByUser walks ix_passkey_user for userID and returns the decoded
// credentials in index order (unsorted). A dangling index entry is skipped;
// decoding fails closed (auth bucket); an absent bucket yields no credentials
// (empty first boot).
func collectPasskeysByUser(tx *bbolt.Tx, userID int64) ([]auth.PasskeyCredential, error) {
	ib, ok := authBucket(tx, bucketIxPasskeyUser)
	if !ok {
		return nil, nil
	}
	pb, ok := authBucket(tx, bucketAuthPasskeys)
	if !ok {
		return nil, nil
	}
	var out []auth.PasskeyCredential
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
			return nil, err
		}
		out = append(out, *rec.toPasskey())
	}
	return out, nil
}

// PasskeyByCredentialID looks up a passkey by its credential id (the
// WebAuthn login hot path), reporting absence through found rather than a nil
// credential with a nil error. Decoding fails closed.
func (s *Store) PasskeyByCredentialID(_ context.Context, credID []byte) (*auth.PasskeyCredential, bool, error) {
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
	return out, out != nil, err
}

// UpdatePasskeyAfterLogin persists the post-login authenticator flags and a
// durable, MONOTONIC sign_count: the stored value is set to
// max(stored, incoming) so a lower incoming counter (a replay or a cloned
// authenticator) can never regress it and defeat clone detection (CVE-2023-45669). A missing credential is a no-op returning nil, matching
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

		enc, err := kv.Encode(&rec)
		if err != nil {
			return err
		}
		if err := pb.Put(credID, enc); err != nil {
			return fmt.Errorf("authstore: put passkey: %w", err)
		}
		return nil
	})
}

// RenamePasskey sets the friendly name of the passkey ref identifies, but only
// when it belongs to ref.UserID. It resolves the credential
// id by a user-scoped walk of ix_passkey_user, so a passkey owned by a
// different user is never visited and cannot be renamed. A ref matching no row
// is a no-op returning nil, matching the SQLite UPDATE affecting zero rows.
func (s *Store) RenamePasskey(_ context.Context, ref auth.PasskeyRef, name string) error {
	return s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		credID, rec, found, err := findUserPasskeyByID(tx, ref.UserID, ref.ID)
		if err != nil || !found {
			return err
		}
		rec.Name = name
		enc, err := kv.Encode(rec)
		if err != nil {
			return err
		}
		if err := pb.Put(credID, enc); err != nil {
			return fmt.Errorf("authstore: put passkey: %w", err)
		}
		return nil
	})
}

// DeletePasskey removes the passkey ref identifies, but only when it belongs to
// ref.UserID. Like RenamePasskey it resolves the credential
// id via a user-scoped index walk, so it can only ever delete the supplied
// user's own passkey. It deletes the primary row and its ix_passkey_user entry
// in one Update; a ref matching no row is a no-op returning nil.
func (s *Store) DeletePasskey(_ context.Context, ref auth.PasskeyRef) error {
	var deleted bool
	err := s.update(func(tx *bbolt.Tx) error {
		pb, ok := authBucket(tx, bucketAuthPasskeys)
		if !ok {
			return nil
		}
		credID, _, found, err := findUserPasskeyByID(tx, ref.UserID, ref.ID)
		if err != nil || !found {
			return err
		}
		if err := pb.Delete(credID); err != nil {
			return fmt.Errorf("authstore: delete passkey: %w", err)
		}
		if err := idxDelete(tx, bucketIxPasskeyUser, passkeyUserIndexKey(ref.UserID, credID)); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return err
	}
	if deleted {
		slog.Info("passkey deleted", "passkey_id", ref.ID, "user_id", ref.UserID)
	}
	return nil
}

// PasskeyCountForUser returns the number of passkeys registered for a user
// . It is a key-only prefix count over ix_passkey_user with
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
func findUserPasskeyByID(tx *bbolt.Tx, userID, id int64) (credID []byte, rec *pkRec, found bool, err error) {
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
