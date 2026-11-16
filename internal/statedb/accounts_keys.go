package statedb

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
)

// key merges id's key row with its use stamp.
func (r authReader) key(id int64, row *keyRow) (*access.Key, error) {
	use, _, err := r.a.keyUse.Get(r.r, id)
	if err != nil {
		return nil, err
	}
	return &access.Key{
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, LastUsedAt: use.LastUsedAt, PreviousUntil: row.PreviousUntil,
		KeyHash: access.NewHash(row.KeyHash), PreviousHash: access.NewHash(row.PreviousHash), KeyPrefix: row.KeyPrefix,
		KeySuffix: row.KeySuffix, Label: row.Label, Scopes: row.Scopes, LineageIDs: row.LineageIDs, ID: id, UserID: row.UserID,
	}, nil
}

func (r authReader) keyByID(id int64) (*access.Key, bool, error) {
	row, ok, err := r.a.keys.Get(r.r, id)
	if err != nil || !ok {
		return nil, false, err
	}
	k, err := r.key(id, &row)
	return k, err == nil, err
}

// keyByHash finds the key whose current or previous secret is hash.
func (r authReader) keyByHash(hash access.Hash) (*access.Key, bool, error) {
	id, _, ok, err := r.a.keyByHash.Lookup(r.r, appendText(nil, hash.Reveal()))
	if err != nil || !ok {
		return nil, false, err
	}
	return r.keyByID(id)
}

// keyIDsOf lists userID's key IDs through the key_user index.
func (r authReader) keyIDsOf(userID int64) ([]int64, error) {
	ids := []int64{}
	err := r.a.keysByUser.Scan(r.r, bolttable.Exact(idKeys.AppendKey(nil, userID)), func(_ []byte, id int64, _ []byte) (bool, error) {
		ids = append(ids, id)
		return true, nil
	})
	return ids, err
}

// keysOf lists userID's keys in ID order, through the key_user index.
func (r authReader) keysOf(userID int64) ([]access.Key, error) {
	ids, err := r.keyIDsOf(userID)
	if err != nil {
		return nil, err
	}
	out := make([]access.Key, 0, len(ids))
	for _, id := range ids {
		k, ok, err := r.keyByID(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: key_user names key %d, which is not stored", ErrCorrupt, id)
		}
		out = append(out, *k)
	}
	return out, nil
}

func (r authReader) allKeys() ([]access.Key, error) {
	out := []access.Key{}
	err := r.a.keys.Scan(r.r, bolttable.Range{}, func(id int64, row *keyRow) (bool, error) {
		k, err := r.key(id, row)
		if err != nil {
			return false, err
		}
		out = append(out, *k)
		return true, nil
	})
	return out, err
}

// CreateAPIKey assigns key.ID from the keys sequence and appends it to
// key.LineageIDs, so a stored key's path ends with its own ID (rule 17).
func (x *accountTx) CreateAPIKey(_ context.Context, key *access.Key) error {
	if key.KeyHash.IsZero() {
		return errNoHash
	}
	id, err := newID(x.tx, x.s.a.keys)
	if err != nil {
		return err
	}
	lineage := append(slices.Clone(key.LineageIDs), id)
	row := keyRow{
		CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt, PreviousUntil: key.PreviousUntil, KeyHash: key.KeyHash.Reveal(),
		PreviousHash: key.PreviousHash.Reveal(), KeyPrefix: key.KeyPrefix, KeySuffix: key.KeySuffix, Label: key.Label,
		Scopes: key.Scopes, LineageIDs: lineage, UserID: key.UserID,
	}
	if err := x.s.a.keys.Put(x.tx, id, &row); err != nil {
		return conflictErr(err)
	}
	if err := x.s.a.keyUse.Put(x.tx, id, &keyUseRow{LastUsedAt: key.LastUsedAt}); err != nil {
		return err
	}
	set(x, &key.ID, id)
	set(x, &key.LineageIDs, lineage)
	return nil
}

func (x *accountTx) APIKeyByHash(_ context.Context, hash access.Hash) (*access.Key, bool, error) {
	return x.read().keyByHash(hash)
}

func (x *accountTx) ListAPIKeysByUserID(_ context.Context, userID int64) ([]access.Key, error) {
	return x.read().keysOf(userID)
}

func (x *accountTx) ListAPIKeys(context.Context) ([]access.Key, error) { return x.read().allKeys() }

// ownedKey reads ref's key row, or account.ErrNotFound unless ref.UserID
// owns it (rule 6).
func (x *accountTx) ownedKey(ref access.KeyRef) (keyRow, error) {
	row, ok, err := x.s.a.keys.Get(x.tx.Read(), ref.ID)
	if err != nil {
		return keyRow{}, err
	}
	if !ok || row.UserID != ref.UserID {
		return keyRow{}, account.ErrNotFound
	}
	return row, nil
}

// DeleteAPIKey deletes ref's key when ref.UserID owns it, both its hashes
// leaving key_hash with the row (rules 6 and 23).
func (x *accountTx) DeleteAPIKey(_ context.Context, ref access.KeyRef) error {
	if _, err := x.ownedKey(ref); err != nil {
		return err
	}
	_, err := x.s.a.keys.Delete(x.tx, ref.ID)
	return err
}

// UpdateAPIKeySecret writes s over ref's key and zeroes its LastUsedAt in
// the same transaction; the hash s supersedes leaves key_hash with the row
// write (rules 6, 21 and 23).
func (x *accountTx) UpdateAPIKeySecret(_ context.Context, ref access.KeyRef, s access.KeySecret) error {
	if s.KeyHash.IsZero() {
		return errNoHash
	}
	row, err := x.ownedKey(ref)
	if err != nil {
		return err
	}
	row.KeyHash, row.PreviousHash, row.PreviousUntil = s.KeyHash.Reveal(), s.PreviousHash.Reveal(), s.PreviousUntil
	row.KeyPrefix, row.KeySuffix = s.KeyPrefix, s.KeySuffix
	if err := x.s.a.keys.Put(x.tx, ref.ID, &row); err != nil {
		return conflictErr(err)
	}
	return x.s.a.keyUse.Put(x.tx, ref.ID, &keyUseRow{})
}

// UpdateAPIKeyLabel writes label over ref's key and nothing else (rule 6).
func (x *accountTx) UpdateAPIKeyLabel(_ context.Context, ref access.KeyRef, label string) error {
	row, err := x.ownedKey(ref)
	if err != nil {
		return err
	}
	row.Label = label
	return x.s.a.keys.Put(x.tx, ref.ID, &row)
}

// DeleteUserAPIKeys deletes every key of userID and returns their IDs.
func (x *accountTx) DeleteUserAPIKeys(_ context.Context, userID int64) ([]int64, error) {
	ids, err := x.read().keyIDsOf(userID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := x.s.a.keys.Delete(x.tx, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// TouchAPIKey raises keyID's LastUsedAt to at when hash is its current
// secret and at is later, and writes nothing otherwise, an unknown key or a
// previous secret's use included (rules 19 and 21).
func (x *accountTx) TouchAPIKey(_ context.Context, keyID int64, hash access.Hash, at time.Time) error {
	row, ok, err := x.s.a.keys.Get(x.tx.Read(), keyID)
	if err != nil || !ok || !hash.Equal(access.NewHash(row.KeyHash)) {
		return err
	}
	use, ok, err := x.s.a.keyUse.Get(x.tx.Read(), keyID)
	if err != nil || !ok || !at.After(use.LastUsedAt) {
		return err
	}
	return x.s.a.keyUse.Put(x.tx, keyID, &keyUseRow{LastUsedAt: at})
}

// clearRetiredKeySecrets zeroes the previous secret of at most limit keys
// whose previous secret no longer verifies at now, reading each row in this
// transaction (rule 23), and returns how many it cleared.
func (x *accountTx) clearRetiredKeySecrets(now time.Time, limit int) (int, error) {
	type retired struct {
		row keyRow
		id  int64
	}
	var due []retired
	err := x.s.a.keys.Scan(x.tx.Read(), bolttable.Range{}, func(id int64, row *keyRow) (bool, error) {
		if row.PreviousHash == "" {
			return true, nil
		}
		use, _, err := x.s.a.keyUse.Get(x.tx.Read(), id)
		if err != nil {
			return false, err
		}
		if !row.PreviousUntil.After(now) || !use.LastUsedAt.IsZero() {
			due = append(due, retired{row: *row, id: id})
		}
		return len(due) < limit, nil
	})
	if err != nil {
		return 0, err
	}
	for i := range due {
		due[i].row.PreviousHash, due[i].row.PreviousUntil = "", time.Time{}
		if err := x.s.a.keys.Put(x.tx, due[i].id, &due[i].row); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// CreateAPIKey assigns key.ID and appends it to key.LineageIDs once the key
// is stored.
func (a *AccountStore) CreateAPIKey(ctx context.Context, key *access.Key) error {
	return a.single(ctx, "create key", func(x *accountTx) error { return x.CreateAPIKey(ctx, key) })
}

// APIKeyByHash reads the key holding hash, its use stamp merged.
func (a *AccountStore) APIKeyByHash(ctx context.Context, hash access.Hash) (*access.Key, bool, error) {
	return viewFound(ctx, a, func(r authReader) (*access.Key, bool, error) { return r.keyByHash(hash) })
}

// ListAPIKeysByUserID lists userID's keys in ID order.
func (a *AccountStore) ListAPIKeysByUserID(ctx context.Context, userID int64) ([]access.Key, error) {
	return viewList(ctx, a, func(r authReader) ([]access.Key, error) { return r.keysOf(userID) })
}

// ListAPIKeys lists every user's keys in ID order.
func (a *AccountStore) ListAPIKeys(ctx context.Context) ([]access.Key, error) {
	return viewList(ctx, a, authReader.allKeys)
}

// DeleteAPIKey deletes ref's key, or answers account.ErrNotFound.
func (a *AccountStore) DeleteAPIKey(ctx context.Context, ref access.KeyRef) error {
	return a.single(ctx, "delete key", func(x *accountTx) error { return x.DeleteAPIKey(ctx, ref) })
}

// DeleteUserAPIKeys deletes userID's keys and returns their IDs.
func (a *AccountStore) DeleteUserAPIKeys(ctx context.Context, userID int64) ([]int64, error) {
	return singleValue(ctx, a, "delete user keys", func(x *accountTx) ([]int64, error) { return x.DeleteUserAPIKeys(ctx, userID) })
}

// TouchAPIKey raises keyID's LastUsedAt to at when hash is its current
// secret and at is later (rule 21).
func (a *AccountStore) TouchAPIKey(ctx context.Context, keyID int64, hash access.Hash, at time.Time) error {
	return a.single(ctx, "touch key", func(x *accountTx) error { return x.TouchAPIKey(ctx, keyID, hash, at) })
}

// UpdateAPIKeySecret writes a rotation's secret over ref's key, or answers
// account.ErrNotFound.
func (a *AccountStore) UpdateAPIKeySecret(ctx context.Context, ref access.KeyRef, s access.KeySecret) error {
	return a.single(ctx, "rotate key", func(x *accountTx) error { return x.UpdateAPIKeySecret(ctx, ref, s) })
}

// UpdateAPIKeyLabel renames ref's key, or answers account.ErrNotFound.
func (a *AccountStore) UpdateAPIKeyLabel(ctx context.Context, ref access.KeyRef, label string) error {
	return a.single(ctx, "rename key", func(x *accountTx) error { return x.UpdateAPIKeyLabel(ctx, ref, label) })
}

// ClearRetiredAPIKeySecrets zeroes, in one db.Batch, the previous secret of
// at most limit keys whose previous secret no longer verifies at now
// (rule 23).
func (a *AccountStore) ClearRetiredAPIKeySecrets(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, errCleanupLimit
	}
	return singleValue(ctx, a, "clear retired key secrets", func(x *accountTx) (int, error) {
		return x.clearRetiredKeySecrets(now, limit)
	})
}
