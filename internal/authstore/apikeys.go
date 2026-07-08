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

// This file holds the KeyStore half of AuthStore: the durable auth_api_keys
// bucket plus the ix_apikey_user index. It mirrors the old SQLite
// store/authdb/apikeys.go behaviour exactly (Requirements 16.4, 16.6, 16.7).
//
// auth_api_keys is PRIMARY-keyed by the raw key_hash, so the API-auth hot path
// — GetAPIKeyByHash — is a single point get (matching how passkeys are keyed by
// credential_id). The surrogate id allocated from bbolt's NextSequence lives in
// the JSON value (keyRec.ID) so the id-and-owner addressed method (DeleteAPIKey)
// can ownership-check a row; it resolves the key_hash by a user-scoped prefix
// walk of ix_apikey_user, which is bounded by the user's key count and
// inherently enforces ownership (it only ever visits rows owned by the supplied
// user id).
//
// ix_apikey_user is keyed be64(user_id) 0x00 key_hash -> (empty). The fixed
// 8-byte big-endian user-id prefix plus the 0x00 separator make the 9-byte
// prefix scan unambiguous. This EXACT layout is what users.go's
// cascadeDeleteByUser relies on (it derives the child primary key as
// indexKey[9:]), so the user-delete cascade keeps deleting API keys correctly.
// A separate keyRec is required rather than persisting auth.Key directly: that
// struct marks KeyHash json:"-", so a direct marshal would silently drop the
// credential material the hot-path lookup keys on.
//
// Every durable mutation runs in one s.update transaction (uniqueness check,
// put, index maintenance), so it is crash-durable on commit and a failure rolls
// back leaving no partial primary/index state (Requirements 9.1, 9.2).

// keyRec is the JSON value stored in the auth_api_keys bucket. It carries every
// auth.Key field — including the json:"-" KeyHash — so the API key round-trips
// through the bbolt codec without loss.
type keyRec struct {
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	KeyHash   string     `json:"key_hash"`
	KeyPrefix string     `json:"key_prefix"`
	KeySuffix string     `json:"key_suffix"`
	Label     string     `json:"label"`
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
}

// toKeyRec projects an auth.Key into its persisted form.
func toKeyRec(k *auth.Key) keyRec {
	return keyRec{
		CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt,
		KeyHash:   k.KeyHash,
		KeyPrefix: k.KeyPrefix,
		KeySuffix: k.KeySuffix,
		Label:     k.Label,
		ID:        k.ID,
		UserID:    k.UserID,
	}
}

// toKey reconstructs the auth.Key from its persisted form.
func (r *keyRec) toKey() *auth.Key {
	return &auth.Key{
		CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt,
		KeyHash:   r.KeyHash,
		KeyPrefix: r.KeyPrefix,
		KeySuffix: r.KeySuffix,
		Label:     r.Label,
		ID:        r.ID,
		UserID:    r.UserID,
	}
}

// --- key builders ---

// apiKeyUserPrefix builds the ix_apikey_user prefix for one user:
// be64(user_id) 0x00. The 9-byte fixed-width prefix is what bounds a
// user-scoped scan and what users.go's cascade uses; a key_hash appended after
// it is unambiguous because the separator sits at the fixed offset 8.
func apiKeyUserPrefix(userID int64) []byte {
	return append(kv.Be64(uint64(userID)), kv.Sep) //nolint:gosec // G115: positive surrogate id
}

// apiKeyUserIndexKey builds the full ix_apikey_user key:
// be64(user_id) 0x00 key_hash.
func apiKeyUserIndexKey(userID int64, hash string) []byte {
	prefix := apiKeyUserPrefix(userID)
	out := make([]byte, 0, len(prefix)+len(hash))
	out = append(out, prefix...)
	out = append(out, hash...)
	return out
}

// CreateAPIKey inserts a new API key, rejecting a duplicate key hash with
// errConflict before any write (Requirement 9.3 hash uniqueness), and sets the
// surrogate ID on the supplied struct. CreatedAt is stamped to now when zero,
// mirroring the SQLite CURRENT_TIMESTAMP default. The primary row and its
// ix_apikey_user entry are written together in one Update, crash-durable on
// commit (Requirements 9.1, 9.2); on a conflict nothing is written.
func (s *Store) CreateAPIKey(_ context.Context, key *auth.Key) error {
	if key == nil {
		return errors.New("authstore: CreateAPIKey: nil key")
	}
	if key.KeyHash == "" {
		return errors.New("authstore: CreateAPIKey: empty key hash")
	}
	err := s.update(func(tx *bbolt.Tx) error {
		kb, ok := authBucket(tx, bucketAuthAPIKeys)
		if !ok {
			return fmt.Errorf("authstore: %q bucket not found", bucketAuthAPIKeys)
		}
		// key_hash IS the primary key, so a present row is the duplicate.
		hashKey := []byte(key.KeyHash)
		if kb.Get(hashKey) != nil {
			return errConflict
		}
		return insertAPIKey(tx, kb, key, hashKey)
	})
	if err != nil {
		return err
	}
	slog.Info("api key created", "user_id", key.UserID, "label", key.Label)
	return nil
}

// insertAPIKey allocates the surrogate id, stamps CreatedAt when zero, encodes,
// and writes the key row (primary-keyed by hashKey) plus its ix_apikey_user
// entry within tx. CreatedAt defaults to now, mirroring the SQLite
// CURRENT_TIMESTAMP default.
func insertAPIKey(tx *bbolt.Tx, kb *bbolt.Bucket, key *auth.Key, hashKey []byte) error {
	id, err := nextAuthID(kb)
	if err != nil {
		return err
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	key.ID = id

	rec := toKeyRec(key)
	enc, err := kv.Encode(&rec)
	if err != nil {
		return err
	}
	if err := kb.Put(hashKey, enc); err != nil {
		return fmt.Errorf("authstore: put api key: %w", err)
	}
	return idxPut(tx, bucketIxAPIKeyUser, apiKeyUserIndexKey(key.UserID, key.KeyHash), nil)
}

// GetAPIKeyByHash looks up an API key by its hash (the API-auth hot path),
// returning (nil, nil) when not found (matching the old store's sql.ErrNoRows
// -> nil mapping). Decoding fails closed (auth bucket).
func (s *Store) GetAPIKeyByHash(_ context.Context, hash string) (*auth.Key, error) {
	var out *auth.Key
	err := s.view(func(tx *bbolt.Tx) error {
		kb, ok := authBucket(tx, bucketAuthAPIKeys)
		if !ok {
			return nil
		}
		key := []byte(hash)
		data := kb.Get(key)
		if data == nil {
			return nil
		}
		var rec keyRec
		if err := decodeAuthRecord(bucketAuthAPIKeys, key, data, &rec); err != nil {
			return err
		}
		out = rec.toKey()
		return nil
	})
	return out, err
}

// ListAPIKeysByUserID returns all API keys for a user, ordered by creation date
// descending then surrogate id descending (newest first), matching the old
// store's `ORDER BY created_at DESC`. It walks ix_apikey_user for the user and
// dereferences each key hash; decoding fails closed (auth bucket).
func (s *Store) ListAPIKeysByUserID(_ context.Context, userID int64) ([]auth.Key, error) {
	var out []auth.Key
	err := s.view(func(tx *bbolt.Tx) error {
		var ferr error
		out, ferr = collectAPIKeysByUser(tx, userID)
		return ferr
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// collectAPIKeysByUser walks ix_apikey_user for userID and returns the decoded
// keys in index order (unsorted). A dangling index entry is skipped; decoding
// fails closed (auth bucket); an absent bucket yields no keys (empty first
// boot).
func collectAPIKeysByUser(tx *bbolt.Tx, userID int64) ([]auth.Key, error) {
	ib, ok := authBucket(tx, bucketIxAPIKeyUser)
	if !ok {
		return nil, nil
	}
	kb, ok := authBucket(tx, bucketAuthAPIKeys)
	if !ok {
		return nil, nil
	}
	var out []auth.Key
	prefix := apiKeyUserPrefix(userID)
	c := ib.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		hashKey := k[len(prefix):]
		data := kb.Get(hashKey)
		if data == nil {
			continue // dangling index entry; treat as absent
		}
		var rec keyRec
		if err := decodeAuthRecord(bucketAuthAPIKeys, hashKey, data, &rec); err != nil {
			return nil, err
		}
		out = append(out, *rec.toKey())
	}
	return out, nil
}

// DeleteAPIKey removes the API key identified by surrogate id, but only when it
// belongs to userID (Requirement 16.4, mirroring the SQLite
// `DELETE ... WHERE id=? AND user_id=?`). It resolves the key hash via a
// user-scoped index walk, so it can only ever delete the supplied user's own
// key. It deletes the primary row and its ix_apikey_user entry in one Update; a
// non-matching (id, userID) is a no-op returning nil.
func (s *Store) DeleteAPIKey(_ context.Context, id, userID int64) error {
	var deleted bool
	err := s.update(func(tx *bbolt.Tx) error {
		kb, ok := authBucket(tx, bucketAuthAPIKeys)
		if !ok {
			return nil
		}
		hash, found, err := s.findUserAPIKeyByID(tx, userID, id)
		if err != nil || !found {
			return err
		}
		if err := kb.Delete([]byte(hash)); err != nil {
			return fmt.Errorf("authstore: delete api key: %w", err)
		}
		if err := idxDelete(tx, bucketIxAPIKeyUser, apiKeyUserIndexKey(userID, hash)); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return err
	}
	if deleted {
		slog.Info("api key deleted", "key_id", id, "user_id", userID)
	}
	return nil
}

// findUserAPIKeyByID resolves the key hash of the API key with surrogate id
// owned by userID, walking only that user's ix_apikey_user entries (so
// ownership is enforced by construction). It returns found=false with no error
// when the user has no key with that id. The returned hash is a string copy, so
// it is safe to use for a Put/Delete after the cursor advances. Must be called
// inside a transaction. Decoding fails closed.
func (s *Store) findUserAPIKeyByID(tx *bbolt.Tx, userID, id int64) (hash string, found bool, err error) {
	ib, ok := authBucket(tx, bucketIxAPIKeyUser)
	if !ok {
		return "", false, nil
	}
	kb, ok := authBucket(tx, bucketAuthAPIKeys)
	if !ok {
		return "", false, nil
	}
	prefix := apiKeyUserPrefix(userID)
	c := ib.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		hashKey := k[len(prefix):]
		data := kb.Get(hashKey)
		if data == nil {
			continue // dangling index entry
		}
		var rec keyRec
		if derr := decodeAuthRecord(bucketAuthAPIKeys, hashKey, data, &rec); derr != nil {
			return "", false, derr
		}
		if rec.ID == id {
			return string(hashKey), true, nil
		}
	}
	return "", false, nil
}
