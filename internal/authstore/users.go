package authstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/store/kv"
	"go.etcd.io/bbolt"
)

// This file holds the UserStore half of AuthStore: the durable auth_users
// bucket plus the ix_user_name and ix_user_oidc indexes. It mirrors the old
// SQLite store/authdb/users.go behaviour exactly (Requirements 9.3, 9.4, 16.1,
// 16.2).
//
// auth_users is keyed by the be64(surrogate id) the create path allocates from
// bbolt's NextSequence; the full account lives in the JSON value (userRec). A
// separate userRec is REQUIRED rather than persisting auth.User directly:
// auth.User marks PasswordHash, OIDCSub, OIDCIssuer, and Enabled as json:"-",
// so a direct marshal would silently drop the credential and identity fields.
//
// Two index buckets give the non-id access paths, both carrying the row's
// be64(id) as their value so a lookup is a single index walk plus one
// dereference (no full-bucket scan):
//
//   - ix_user_name: asciiFold(username) -> be64(user_id). Case-insensitive
//     username lookup AND the username uniqueness constraint. asciiFold matches
//     SQLite's COLLATE NOCASE (ASCII A-Z only).
//   - ix_user_oidc: issuer 0x00 sub -> be64(user_id), present only when
//     oidc_sub != "" (mirrors the SQLite partial unique index
//     `WHERE oidc_sub != ''`). The (issuer, sub) lookup and uniqueness.
//
// email has no index (it is not unique in the SQLite schema), so the
// case-insensitive email lookup is a fail-closed bucket scan.
//
// Every durable mutation runs in one s.update transaction: uniqueness is
// checked against the index buckets BEFORE the put, then the primary and its
// indexes are written together, so the write is crash-durable on commit and a
// failure rolls back leaving no partial primary/index state (Requirements 9.2,
// 9.3). DeleteUser additionally cascades to the user's passkeys and API keys
// (durable, same tx) and sessions (in-memory, after commit) (Requirement 9.4).

// userRec is the JSON value stored in the auth_users bucket. It carries every
// auth.User field — including the json:"-" credential/identity fields — so the
// account round-trips through the bbolt codec without loss.
type userRec struct {
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	PasswordHash string    `json:"password_hash,omitempty"`
	Role         string    `json:"role"`
	OIDCSub      string    `json:"oidc_sub,omitempty"`
	OIDCIssuer   string    `json:"oidc_issuer,omitempty"`
	ID           int64     `json:"id"`
	Enabled      bool      `json:"enabled"`
}

// toUserRec projects an auth.User into its persisted form.
func toUserRec(u *auth.User) userRec {
	return userRec{
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
		Username:     u.Username,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		PasswordHash: u.PasswordHash,
		Role:         string(u.Role),
		OIDCSub:      u.OIDCSub,
		OIDCIssuer:   u.OIDCIssuer,
		ID:           u.ID,
		Enabled:      u.Enabled,
	}
}

// toUser reconstructs the auth.User from its persisted form.
func (r *userRec) toUser() *auth.User {
	return &auth.User{
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		Username:     r.Username,
		Email:        r.Email,
		DisplayName:  r.DisplayName,
		PasswordHash: r.PasswordHash,
		Role:         auth.Role(r.Role),
		OIDCSub:      r.OIDCSub,
		OIDCIssuer:   r.OIDCIssuer,
		ID:           r.ID,
		Enabled:      r.Enabled,
	}
}

// --- key builders ---

// userKey builds the auth_users primary key: be64(surrogate id). It is also the
// value stored in the ix_user_name / ix_user_oidc index buckets, so a lookup
// dereferences straight back into auth_users.
func userKey(id int64) []byte {
	return kv.Be64(uint64(id)) //nolint:gosec // G115: positive surrogate id from a monotonic sequence
}

// userNameIndexKey builds the ix_user_name key: asciiFold(username). The fold
// gives the case-insensitive username lookup and uniqueness, matching SQLite's
// COLLATE NOCASE.
func userNameIndexKey(username string) []byte {
	return []byte(asciiFold(username))
}

// userOIDCIndexKey builds the ix_user_oidc key: issuer 0x00 sub. Only written
// when oidc_sub is non-empty (mirrors the SQLite partial unique index).
func userOIDCIndexKey(issuer, sub string) []byte {
	return kv.Join(issuer, sub)
}

// asciiFold lower-cases the ASCII letters A-Z and leaves every other byte
// untouched, matching SQLite's default COLLATE NOCASE folding (ASCII only, not
// full Unicode case folding). It operates on bytes, so multi-byte UTF-8
// sequences pass through unchanged.
func asciiFold(s string) string {
	var changed bool
	for i := range len(s) {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			changed = true
			break
		}
	}
	if !changed {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// idxPut writes val at key in the named index bucket within tx.
func idxPut(tx *bbolt.Tx, bucket string, key, val []byte) error {
	b := tx.Bucket([]byte(bucket))
	if b == nil {
		return fmt.Errorf("authstore: index bucket %q not found", bucket)
	}
	if err := b.Put(key, val); err != nil {
		return fmt.Errorf("authstore: put index %q: %w", bucket, err)
	}
	return nil
}

// idxDelete removes key from the named index bucket within tx. Deleting an
// absent key is a no-op, so callers stay idempotent.
func idxDelete(tx *bbolt.Tx, bucket string, key []byte) error {
	b := tx.Bucket([]byte(bucket))
	if b == nil {
		return fmt.Errorf("authstore: index bucket %q not found", bucket)
	}
	if err := b.Delete(key); err != nil {
		return fmt.Errorf("authstore: delete index %q: %w", bucket, err)
	}
	return nil
}

// CreateUser inserts a new user, enforcing case-insensitive username uniqueness
// and (issuer, sub) uniqueness against the index buckets before the put, and
// sets the surrogate ID on the supplied struct (Requirements 9.2, 9.3). A
// duplicate username (case-insensitive) or duplicate (issuer, sub) returns
// errConflict and writes nothing. CreatedAt is stamped to now when zero and
// UpdatedAt is set equal to it, mirroring the SQLite CURRENT_TIMESTAMP defaults.
func (s *Store) CreateUser(_ context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("authstore: CreateUser: nil user")
	}
	err := s.update(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return fmt.Errorf("authstore: %q bucket not found", bucketAuthUsers)
		}
		if err := checkCreateUserUniqueness(tx, user); err != nil {
			return err
		}
		return insertUser(tx, ub, user)
	})
	if err != nil {
		return err
	}
	slog.Info("user created", "username", user.Username, "role", user.Role)
	return nil
}

// checkCreateUserUniqueness enforces case-insensitive username uniqueness and,
// when the user carries an OIDC identity, (issuer, sub) uniqueness against the
// index buckets before any write, yielding errConflict on a duplicate
// (Requirement 9.3).
func checkCreateUserUniqueness(tx *bbolt.Tx, user *auth.User) error {
	if err := uniqueCheck(tx, bucketIxUserName, userNameIndexKey(user.Username)); err != nil {
		return err
	}
	if user.OIDCSub != "" {
		if err := uniqueCheck(tx, bucketIxUserOIDC, userOIDCIndexKey(user.OIDCIssuer, user.OIDCSub)); err != nil {
			return err
		}
	}
	return nil
}

// insertUser allocates the surrogate id, stamps CreatedAt (when zero) and
// UpdatedAt, encodes the record, and writes the user row plus its ix_user_name
// and (when present) ix_user_oidc entries within tx. CreatedAt defaults to now
// and UpdatedAt is set equal to it, mirroring the SQLite CURRENT_TIMESTAMP
// defaults. Callers run checkCreateUserUniqueness first so the index puts cannot
// clobber another user's entry.
func insertUser(tx *bbolt.Tx, ub *bbolt.Bucket, user *auth.User) error {
	id, err := nextAuthID(ub)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = user.CreatedAt
	user.ID = id

	rec := toUserRec(user)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	key := userKey(id)
	if err := ub.Put(key, enc); err != nil {
		return fmt.Errorf("authstore: put user: %w", err)
	}
	if err := idxPut(tx, bucketIxUserName, userNameIndexKey(user.Username), key); err != nil {
		return err
	}
	if user.OIDCSub != "" {
		return idxPut(tx, bucketIxUserOIDC, userOIDCIndexKey(user.OIDCIssuer, user.OIDCSub), key)
	}
	return nil
}

// GetUserByID looks up a user by surrogate id, returning (nil, nil) when no
// such user exists (matching the old store's sql.ErrNoRows -> nil mapping).
func (s *Store) GetUserByID(_ context.Context, id int64) (*auth.User, error) {
	var out *auth.User
	err := s.view(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		key := userKey(id)
		data := ub.Get(key)
		if data == nil {
			return nil
		}
		var rec userRec
		if err := decodeAuthRecord(bucketAuthUsers, key, data, &rec); err != nil {
			return err
		}
		out = rec.toUser()
		return nil
	})
	return out, err
}

// GetUserByUsername looks up a user case-insensitively by username via
// ix_user_name, returning (nil, nil) when not found (Requirement 16.1).
func (s *Store) GetUserByUsername(_ context.Context, username string) (*auth.User, error) {
	return s.userByIndex(bucketIxUserName, userNameIndexKey(username))
}

// GetUserByEmail looks up a user case-insensitively by email, returning
// (nil, nil) when not found (Requirement 16.1). email is not indexed (it is not
// unique in the schema), so this is a fail-closed scan of auth_users comparing
// the ASCII-folded email of each row.
func (s *Store) GetUserByEmail(_ context.Context, email string) (*auth.User, error) {
	target := asciiFold(email)
	var out *auth.User
	err := s.view(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		c := ub.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec userRec
			if err := decodeAuthRecord(bucketAuthUsers, k, v, &rec); err != nil {
				return err
			}
			if asciiFold(rec.Email) == target {
				out = rec.toUser()
				return nil
			}
		}
		return nil
	})
	return out, err
}

// GetUserByOIDCSub looks up a user by (issuer, sub) via ix_user_oidc, returning
// (nil, nil) when not found. An empty sub never matches, mirroring the SQLite
// partial index keyed only on rows with oidc_sub != ”. Matching on subject
// alone would be unsafe: a subject is unique only within its issuer.
func (s *Store) GetUserByOIDCSub(_ context.Context, issuer, sub string) (*auth.User, error) {
	if sub == "" {
		return nil, nil
	}
	return s.userByIndex(bucketIxUserOIDC, userOIDCIndexKey(issuer, sub))
}

// userByIndex resolves a user through an index bucket whose value is the user's
// be64(id) primary key. It returns (nil, nil) on a missing index entry or a
// dangling reference, and fails closed on an undecodable user record.
func (s *Store) userByIndex(indexBucket string, indexKey []byte) (*auth.User, error) {
	var out *auth.User
	err := s.view(func(tx *bbolt.Tx) error {
		ib, ok := authBucket(tx, indexBucket)
		if !ok {
			return nil
		}
		idv := ib.Get(indexKey)
		if idv == nil {
			return nil
		}
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		data := ub.Get(idv)
		if data == nil {
			return nil // dangling index entry; treat as not found
		}
		var rec userRec
		if err := decodeAuthRecord(bucketAuthUsers, idv, data, &rec); err != nil {
			return err
		}
		out = rec.toUser()
		return nil
	})
	return out, err
}

// ListUsers returns all users ordered by username (case-insensitive, then by
// id), matching the old store's `ORDER BY username` over a COLLATE NOCASE
// column. Decoding fails closed (auth bucket).
func (s *Store) ListUsers(_ context.Context) ([]auth.User, error) {
	var out []auth.User
	err := s.view(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		c := ub.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec userRec
			if err := decodeAuthRecord(bucketAuthUsers, k, v, &rec); err != nil {
				return err
			}
			out = append(out, *rec.toUser())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := asciiFold(out[i].Username), asciiFold(out[j].Username)
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateUser updates all mutable fields for the user, re-keying ix_user_name
// and ix_user_oidc when the username or (issuer, sub) change (delete-old,
// add-new), and re-checking uniqueness for the new keys (Requirement 9.3). It
// preserves the original CreatedAt and stamps UpdatedAt to now. An update for a
// non-existent id is a no-op returning nil, matching the SQLite UPDATE that
// affects zero rows.
func (s *Store) UpdateUser(_ context.Context, user *auth.User) error {
	if user == nil {
		return errors.New("authstore: UpdateUser: nil user")
	}
	return s.update(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		key := userKey(user.ID)
		oldData := ub.Get(key)
		if oldData == nil {
			return nil // no such user; mirror UPDATE ... WHERE id=? affecting 0 rows
		}
		var oldRec userRec
		if err := decodeAuthRecord(bucketAuthUsers, key, oldData, &oldRec); err != nil {
			return err
		}

		idx, err := checkUpdateUserUniqueness(tx, user, &oldRec)
		if err != nil {
			return err
		}

		user.CreatedAt = oldRec.CreatedAt
		user.UpdatedAt = time.Now().UTC()
		rec := toUserRec(user)
		enc, err := kv.Encode(&rec)
		if err != nil {
			return err
		}
		if err := ub.Put(key, enc); err != nil {
			return fmt.Errorf("authstore: put user: %w", err)
		}

		if err := reindexUserName(tx, idx.nameChanged, idx.oldNameKey, idx.newNameKey, key); err != nil {
			return err
		}
		return reindexUserOIDC(tx, idx.oidcChanged, idx.oldOIDCKey, idx.newOIDCKey, key)
	})
}

// userIndexUpdate is the index re-keying plan computed for an UpdateUser:
// whether the username and/or OIDC identity changed, plus the old/new index
// keys to delete/add. A nil OIDC key means "no OIDC identity on that side".
type userIndexUpdate struct {
	oldNameKey  []byte
	newNameKey  []byte
	oldOIDCKey  []byte
	newOIDCKey  []byte
	nameChanged bool
	oidcChanged bool
}

// checkUpdateUserUniqueness computes the index re-keying plan for an update and
// enforces uniqueness for a newly-taken username or (issuer, sub) before any
// write: a username whose fold changed, or a new OIDC identity, is checked
// against its index and yields errConflict on a collision (Requirement 9.3). An
// unchanged username/identity is not re-checked, so a user keeps its own keys.
func checkUpdateUserUniqueness(tx *bbolt.Tx, user *auth.User, oldRec *userRec) (userIndexUpdate, error) {
	idx := userIndexUpdate{
		newNameKey: userNameIndexKey(user.Username),
		oldNameKey: userNameIndexKey(oldRec.Username),
	}
	idx.nameChanged = !bytes.Equal(idx.newNameKey, idx.oldNameKey)
	if idx.nameChanged {
		if err := uniqueCheck(tx, bucketIxUserName, idx.newNameKey); err != nil {
			return userIndexUpdate{}, err
		}
	}

	if oldRec.OIDCSub != "" {
		idx.oldOIDCKey = userOIDCIndexKey(oldRec.OIDCIssuer, oldRec.OIDCSub)
	}
	if user.OIDCSub != "" {
		idx.newOIDCKey = userOIDCIndexKey(user.OIDCIssuer, user.OIDCSub)
	}
	idx.oidcChanged = !bytes.Equal(idx.oldOIDCKey, idx.newOIDCKey)
	if idx.newOIDCKey != nil && idx.oidcChanged {
		if err := uniqueCheck(tx, bucketIxUserOIDC, idx.newOIDCKey); err != nil {
			return userIndexUpdate{}, err
		}
	}
	return idx, nil
}

// reindexUserName rewrites the ix_user_name entry when the username changed:
// it deletes the old fold-key and adds the new one pointing at the user's
// primary key. A no-op (nil) when the username did not change.
func reindexUserName(tx *bbolt.Tx, changed bool, oldKey, newKey, primaryKey []byte) error {
	if !changed {
		return nil
	}
	if err := idxDelete(tx, bucketIxUserName, oldKey); err != nil {
		return err
	}
	return idxPut(tx, bucketIxUserName, newKey, primaryKey)
}

// reindexUserOIDC rewrites the ix_user_oidc entry when the (issuer, sub)
// changed: it drops the old entry (when one existed) and adds the new one (when
// the user still has an OIDC identity), both pointing at the user's primary
// key. A no-op (nil) when the identity did not change.
func reindexUserOIDC(tx *bbolt.Tx, changed bool, oldKey, newKey, primaryKey []byte) error {
	if !changed {
		return nil
	}
	if oldKey != nil {
		if err := idxDelete(tx, bucketIxUserOIDC, oldKey); err != nil {
			return err
		}
	}
	if newKey != nil {
		return idxPut(tx, bucketIxUserOIDC, newKey, primaryKey)
	}
	return nil
}

// DeleteUser removes the user and cascades to its passkeys, API keys, and
// sessions (Requirement 9.4). The durable parts — the user row, its
// ix_user_name / ix_user_oidc entries, and every child passkey / API key with
// their ix_passkey_user / ix_apikey_user entries — are deleted in one Update so
// the cascade is atomic and crash-durable on commit; the user's in-memory
// sessions are dropped after commit. Deleting a non-existent user is a no-op
// returning nil, matching the SQLite DELETE affecting zero rows.
func (s *Store) DeleteUser(_ context.Context, id int64) error {
	err := s.update(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		key := userKey(id)
		data := ub.Get(key)
		if data == nil {
			return nil // no such user
		}
		var rec userRec
		if err := decodeAuthRecord(bucketAuthUsers, key, data, &rec); err != nil {
			return err
		}
		if err := deleteUserAndIndexes(tx, ub, key, &rec); err != nil {
			return err
		}
		return cascadeUserChildren(tx, id)
	})
	if err != nil {
		return err
	}
	// Ephemeral sessions are dropped only after the durable cascade commits.
	s.deleteUserSessions(id)
	slog.Info("user deleted", "user_id", id)
	return nil
}

// deleteUserAndIndexes removes the user row and its uniqueness-index entries
// (ix_user_name always, ix_user_oidc when the user had an OIDC identity) within
// tx. A leftover ix_user_name entry would block recreating the same admin
// username, breaking the documented clean-break recovery path, so the index
// entries must go with the row.
func deleteUserAndIndexes(tx *bbolt.Tx, ub *bbolt.Bucket, key []byte, rec *userRec) error {
	if err := ub.Delete(key); err != nil {
		return fmt.Errorf("authstore: delete user: %w", err)
	}
	if err := idxDelete(tx, bucketIxUserName, userNameIndexKey(rec.Username)); err != nil {
		return err
	}
	if rec.OIDCSub != "" {
		return idxDelete(tx, bucketIxUserOIDC, userOIDCIndexKey(rec.OIDCIssuer, rec.OIDCSub))
	}
	return nil
}

// cascadeUserChildren deletes the user's durable child records — passkeys and
// API keys, each with its user-scoped index entry — within tx, walking each
// index prefix directly via cascadeDeleteByUser.
func cascadeUserChildren(tx *bbolt.Tx, userID int64) error {
	if err := cascadeDeleteByUser(tx, bucketIxPasskeyUser, bucketAuthPasskeys, userID); err != nil {
		return err
	}
	return cascadeDeleteByUser(tx, bucketIxAPIKeyUser, bucketAuthAPIKeys, userID)
}

// cascadeDeleteByUser deletes every child record owned by userID and its
// user-scoped index entry. The index bucket is keyed be64(user_id) 0x00
// childPrimaryKey -> (empty), so a prefix scan on be64(user_id) 0x00 yields the
// child primary keys. Keys are collected before deletion to avoid bbolt's
// cursor-skip-after-delete hazard.
func cascadeDeleteByUser(tx *bbolt.Tx, indexBucket, primaryBucket string, userID int64) error {
	ib, ok := authBucket(tx, indexBucket)
	if !ok {
		return nil
	}
	pb, ok := authBucket(tx, primaryBucket)
	if !ok {
		return nil
	}
	prefix := append(kv.Be64(uint64(userID)), kv.Sep) //nolint:gosec // G115: positive surrogate id

	type victim struct{ indexKey, primaryKey []byte }
	var victims []victim
	c := ib.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		victims = append(victims, victim{
			indexKey:   append([]byte(nil), k...),
			primaryKey: append([]byte(nil), k[len(prefix):]...),
		})
	}
	for i := range victims {
		if err := ib.Delete(victims[i].indexKey); err != nil {
			return fmt.Errorf("authstore: delete index %q: %w", indexBucket, err)
		}
		if err := pb.Delete(victims[i].primaryKey); err != nil {
			return fmt.Errorf("authstore: delete %q: %w", primaryBucket, err)
		}
	}
	return nil
}

// deleteUserSessions drops every in-memory session belonging to userID. It is
// called after the durable cascade commits. It delegates to the shared
// keep-one helper (sessions.go) with an empty exceptHash so the guarded
// map-walk pattern is not duplicated; no real session carries an empty hash, so
// every one of the user's sessions is removed.
func (s *Store) deleteUserSessions(userID int64) {
	s.deleteUserSessionsExcept(userID, "")
}

// UserCount returns the number of user accounts, used for first-boot detection
// (Requirement 16.2). The auth_users bucket is small, so this is a cursor count
// rather than a maintained counter.
func (s *Store) UserCount(_ context.Context) (int, error) {
	count := 0
	err := s.view(func(tx *bbolt.Tx) error {
		ub, ok := authBucket(tx, bucketAuthUsers)
		if !ok {
			return nil
		}
		c := ub.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			count++
		}
		return nil
	})
	return count, err
}
